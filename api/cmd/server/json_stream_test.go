package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func streamTestBootstrap() domain.Bootstrap {
	now := time.Date(2026, 11, 11, 8, 30, 0, 123456789, time.UTC)
	target := "2026-12-01"
	parent := "comment-0"
	user := domain.User{ID: "user-1", Name: "Ada <admin> & co", Email: "ada@example.test"}
	data := domain.Bootstrap{
		Workspace:  domain.Workspace{ID: "workspace-1", Name: "Flow & <Friends>", URLKey: "flow"},
		Viewer:     user,
		ViewerRole: "admin",
		Users:      []domain.User{user, {ID: "user-2", Name: "Grace"}},
		Teams:      []domain.Team{{ID: "team-1", Name: "Core", Key: "COR"}},
		Projects: []domain.Project{
			{ID: "project-1", Name: "Alpha   line", Description: "<script>alert(1)</script>", TeamIDs: []string{"team-1"}, TargetDate: &target, CreatedAt: now,
				Comments:        []domain.Comment{{ID: "comment-1", Body: "hi", ParentID: &parent, User: user, CreatedAt: now, Reactions: map[string][]string{"+1": {"user-2"}}}},
				ProgressHistory: []domain.ProjectProgressHistoryPoint{{Date: now, Value: 0.5}}},
			{ID: "project-2", Name: "Beta", MemberIDs: nil, LabelIDs: []string{}},
		},
		Trash:     []domain.TrashEntry{{ID: "trash-1", ResourceType: "document", Payload: json.RawMessage(`{"id":"doc-9","content":"x<y"}`), DeletedBy: user, DeletedAt: now}},
		Documents: []domain.Document{{ID: "doc-1", Title: "Notes", Content: "a & b", ContentData: map[string]any{"type": "doc", "n": 1.5}, Creator: user}},
		// A nil slice (null), an empty one ([]) and an empty map.
		Labels:          nil,
		States:          []domain.WorkflowState{},
		TeamSettings:    map[string]domain.TeamSettings{},
		ProjectUpdates:  map[string][]domain.ProjectUpdate{},
		Settings:        map[string]any{"b": true, "a": []any{1, "two"}},
		AIConversations: []domain.AIConversation{{ID: "conversation-1", Context: map[string]any{"z": 1, "a": "<b>"}}},
	}
	return data
}

// The streamed bootstrap must be byte for byte what writeJSON sends.
func TestStreamedJSONMatchesWriteJSON(t *testing.T) {
	for name, value := range map[string]domain.Bootstrap{"populated": streamTestBootstrap(), "zero": {}} {
		t.Run(name, func(t *testing.T) {
			want := httptest.NewRecorder()
			writeJSON(want, http.StatusOK, value)
			got := httptest.NewRecorder()
			writeStreamedJSON(context.Background(), got, http.StatusOK, value)
			if !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) {
				t.Fatalf("streamed JSON differs from writeJSON\n got: %.600s\nwant: %.600s", got.Body.String(), want.Body.String())
			}
			if got.Code != want.Code || got.Header().Get("Content-Type") != want.Header().Get("Content-Type") {
				t.Fatalf("status/content type = %d %q, want %d %q", got.Code, got.Header().Get("Content-Type"), want.Code, want.Header().Get("Content-Type"))
			}
		})
	}
}

// A client that went away stops the encoding: the response is aborted, not
// completed as truncated JSON.
func TestStreamedJSONStopsForCancelledRequest(t *testing.T) {
	data := streamTestBootstrap()
	for len(data.Projects) < 1000 {
		data.Projects = append(data.Projects, data.Projects[0])
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", recovered)
		}
	}()
	writeStreamedJSON(ctx, httptest.NewRecorder(), http.StatusOK, data)
	t.Fatal("encoding a cancelled request completed")
}

// The gate admits at most its concurrency, answers waiters that outlast the
// wait with 503 + Retry-After, rejects past the queue limit at once, and lets
// a waiter whose client disconnects leave without being served.
func TestMetadataReadGateBoundsConcurrency(t *testing.T) {
	gate := newMetadataReadGate(2, 1, 150*time.Millisecond, time.Minute)
	release := make(chan struct{})
	entered := make(chan struct{}, 8)
	handler := gate.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	serve := func(ctx context.Context) chan *httptest.ResponseRecorder {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/issue-records/bootstrap", nil))
			done <- recorder
		}()
		return done
	}
	first, second := serve(context.Background()), serve(context.Background())
	<-entered
	<-entered
	// Both slots are taken: a third request waits, a fourth exceeds the queue.
	third := serve(context.Background())
	waitFor := func(condition func() bool) {
		deadline := time.Now().Add(2 * time.Second)
		for !condition() {
			if time.Now().After(deadline) {
				t.Fatal("condition not reached")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor(func() bool { return gate.queued.Load() == 1 })
	fourth := <-serve(context.Background())
	if fourth.Code != http.StatusServiceUnavailable || fourth.Header().Get("Retry-After") == "" {
		t.Fatalf("over the queue limit: %d Retry-After=%q", fourth.Code, fourth.Header().Get("Retry-After"))
	}
	timedOut := <-third
	if timedOut.Code != http.StatusServiceUnavailable || timedOut.Header().Get("Retry-After") == "" {
		t.Fatalf("after the queue wait: %d Retry-After=%q", timedOut.Code, timedOut.Header().Get("Retry-After"))
	}
	// A waiter whose client disconnects leaves the queue at once.
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := serve(ctx)
	waitFor(func() bool { return gate.queued.Load() == 1 })
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("a cancelled waiter stayed in the queue")
	}
	if gate.cancelled.Load() != 1 || len(entered) != 0 {
		t.Fatalf("cancelled=%d entered=%d", gate.cancelled.Load(), len(entered))
	}
	close(release)
	for _, done := range []chan *httptest.ResponseRecorder{first, second} {
		if response := <-done; response.Code != http.StatusOK {
			t.Fatalf("admitted request = %d", response.Code)
		}
	}
	if gate.queued.Load() != 0 || len(gate.slots) != 0 {
		t.Fatalf("gate not drained: queued=%d slots=%d", gate.queued.Load(), len(gate.slots))
	}
	if gate.served.Load() != 2 || gate.rejected.Load() != 2 {
		t.Fatalf("served=%d rejected=%d", gate.served.Load(), gate.rejected.Load())
	}
}

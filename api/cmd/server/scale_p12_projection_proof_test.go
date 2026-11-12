package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP12QueuedProofSurvivesBodyWritesAndRejectsGrantChanges(t *testing.T) {
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		principal := f.principals[3]
		access, err := repo.ResolveEventAccess(t.Context(), f.data.Workspace.URLKey, principal.user.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		event := domain.RealtimeEvent{ID: "p12-proof", Type: "issue.updated", AggregateID: f.secret.ID, Payload: json.RawMessage(`{}`)}
		source, err := f.service.BuildEventSource(t.Context(), f.data.Workspace.URLKey, event)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := ProjectEvent(source, access)
		if err != nil || !frame.Visible() {
			t.Fatalf("frame %v", err)
		}
		defer frame.Close()
		authRequest[domain.Issue](t, f.admin, http.MethodPatch, f.host.URL+"/api/issue-records/"+f.secret.ID, map[string]string{"title": "Body-only edit"}, f.data.Workspace.URLKey, http.StatusOK)
		if err := frame.Validate(t.Context()); err != nil {
			t.Fatalf("body write revoked an unchanged permission proof: %v", err)
		}
		old, err := frame.Event()
		if err != nil || strings.Contains(string(old.Payload), "Body-only edit") {
			t.Fatal("immutable source changed in place")
		}
		authRequest[[]domain.IssuePermission](t, f.admin, http.MethodPut, f.host.URL+"/api/issues/"+f.secret.ID+"/permissions", map[string]any{"permissions": []any{}}, f.data.Workspace.URLKey, http.StatusOK)
		if err := frame.Validate(t.Context()); err == nil {
			t.Fatal("queued explicit grant survived revocation")
		}
		writer := httptest.NewRecorder()
		if frame.WriteSSE(writer, "") || writer.Body.Len() != 0 {
			t.Fatal("revoked prevalidated frame reached the wire")
		}
	}
}

func TestScaleP12APIDocumentProjectionMatchesLegacy(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		for _, team := range []string{f.data.Teams[0].ID, f.private} {
			document := authRequest[domain.Document](t, f.admin, http.MethodPost, f.host.URL+"/api/documents", map[string]any{"title": "API-created document", "content": "Body", "teamIds": []string{team}}, f.data.Workspace.URLKey, http.StatusCreated)
			event := domain.RealtimeEvent{ID: "p12-api-" + document.ID, Type: "document.created", AggregateID: document.ID, Payload: json.RawMessage(`{}`)}
			for _, principal := range f.principals {
				t.Run(principal.name+"/"+team, func(t *testing.T) {
					scaleP12EqualEvent(t, f.service, principal.request(f.data.Workspace.URLKey), event)
				})
			}
		}
	}
}

func TestScaleP12JournalFramesKeepTransactionsAtomicAndQueuesBounded(t *testing.T) {
	transaction := emptySyncTransaction("transaction", "9007199254740993")
	for range 50 {
		transaction.Upserts = append(transaction.Upserts, syncV2Upsert{Key: syncV2Key{Kind: "issues", ID: "issue"}, Version: "1", Data: json.RawMessage(`{"id":"issue","title":"` + strings.Repeat("x", 1000) + `"}`), Projection: "summary", DetailStale: true})
	}
	frame, err := syncTransactionFrame(transaction, "epoch")
	if err != nil {
		t.Fatal(err)
	}
	if frame.Size()+len(frame.id)+128 > syncV2FrameBytes {
		t.Fatal("stream transaction exceeded frame budget")
	}
	event, err := frame.Event()
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Transaction syncV2Transaction `json:"transaction"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Transaction.Upserts) != 0 || len(payload.Transaction.Invalidations) != 1 {
		t.Fatal("large stream transaction was split")
	}
	w := httptest.NewRecorder()
	if !frame.WriteSSE(w, "other-replica") || !strings.HasPrefix(w.Body.String(), "id: 9007199254740993\n") {
		t.Fatal("journal cursor was tied to one replica")
	}
	frame.noCursor = true
	w = httptest.NewRecorder()
	if !frame.WriteSSE(w, "") || strings.HasPrefix(w.Body.String(), "id:") {
		t.Fatal("business side channel advanced a transaction cursor")
	}
	hub := newRealtimeHub()
	subscriber, stop := hub.subscribeTransport("workspace", "", false)
	defer stop()
	subscriber.setSyncV2QueueLimit()
	for i := range 16 {
		event := domain.RealtimeEvent{ID: string(rune('a' + i)), Type: "issue.updated", Payload: json.RawMessage(`{"content":"` + strings.Repeat("x", 4096) + `"}`)}
		if !subscriber.push(event) {
			break
		}
	}
	subscriber.mu.Lock()
	retained := subscriber.retained
	subscriber.mu.Unlock()
	if retained > 32<<10 {
		t.Fatalf("negotiated backlog=%d", retained)
	}
	stop()
	if hub.budget.used.Load() != 0 {
		t.Fatal("queue lease survived unsubscribe")
	}
}

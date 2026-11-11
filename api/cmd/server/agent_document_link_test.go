package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A document the Agent page creates must open from the reply's link, and be created once: the reply's writes reach
// the asking tab (realtime), document events reach everyone who may open the document, a quiet reply stream is kept
// alive, and a write call that reaches Flow twice runs once.

// newAgentChatTestServer is an auth-disabled server whose chat model is provider (Chat Completions).
func newAgentChatTestServer(t *testing.T, provider http.HandlerFunc) (http.Handler, *store.SQLiteStore) {
	t.Helper()
	host := httptest.NewServer(provider)
	t.Cleanup(host.Close)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-chat-completions", BaseURL: host.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}, agentClient: host.Client()}
	handler := newHandler(s)
	t.Cleanup(func() { s.stopDeliveryScheduler(context.Background()) })
	return handler, repository
}

// chatChunk writes one Chat Completions stream chunk.
func chatChunk(w io.Writer, delta map[string]any, finish string) {
	choice := map[string]any{"index": 0, "delta": delta}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	raw, _ := json.Marshal(map[string]any{"choices": []any{choice}})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
}

func documentsTitled(t *testing.T, repository *store.SQLiteStore, title string) []domain.Document {
	t.Helper()
	data, ok := repository.WorkspaceMetadata("")
	if !ok {
		t.Fatal("workspace metadata missing")
	}
	var found []domain.Document
	for _, document := range data.Documents {
		if document.Title == title {
			found = append(found, document)
		}
	}
	return found
}

// A provider stream that delivers the same save_document call twice (here under a second index, as gateways that
// repeat a call do) used to run it twice: two identical documents behind one tool row.
func TestAgentChatRunsARepeatedWriteCallOnce(t *testing.T) {
	arguments, _ := json.Marshal(map[string]any{"title": "Release checklist", "content": "- one\n- two"})
	handler, repository := newAgentChatTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		switch {
		case strings.Contains(string(body), `"role":"tool"`):
			chatChunk(w, map[string]any{"content": "Created [Release checklist](/test-workspace/document/x)."}, "stop")
		case strings.Contains(string(body), `"tools"`):
			for _, index := range []int{0, 1} {
				chatChunk(w, map[string]any{"tool_calls": []any{map[string]any{"index": index, "id": "call_doc_1", "type": "function", "function": map[string]any{"name": "save_document", "arguments": string(arguments)}}}}, "")
			}
			chatChunk(w, map[string]any{}, "tool_calls")
		default:
			chatChunk(w, map[string]any{"content": "Release checklist"}, "stop")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	_, reply := runAgentChangeChat(t, handler, "Create a document called Release checklist")
	if documents := documentsTitled(t, repository, "Release checklist"); len(documents) != 1 {
		t.Fatalf("documents = %d, want 1", len(documents))
	}
	rows := 0
	for _, part := range reply.Parts {
		if part.ToolCall != nil && part.ToolCall.Name == "save_document" {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("save_document rows = %d, want 1: %#v", rows, reply.Parts)
	}
}

// A write call that reaches Flow again (a retry, a replay of the stored call, the same call arriving while the first
// still runs) answers with the first run's document instead of creating another; a new call still creates.
func TestAgentWriteCallReplayReturnsTheSameDocument(t *testing.T) {
	f := newAgentAttributionFixture(t)
	session := domain.AgentSession{ID: "agent_session_replay", UserID: f.data.Viewer.ID}
	request := func() *http.Request {
		ctx := context.WithValue(context.Background(), authUserContextKey{}, f.data.Viewer)
		ctx = store.ContextWithActor(ctx, f.data.Viewer)
		return httptest.NewRequest("POST", "/api/agent?workspace="+f.data.Workspace.URLKey, nil).WithContext(ctx)
	}
	arguments := json.RawMessage(`{"title":"Replay checklist","content":"Body"}`)
	run := func(id string) (string, *domain.AgentChange) {
		t.Helper()
		call := domain.AgentToolCall{ID: id, Name: "save_document", Arguments: arguments}
		result, err := f.s.executeAgentToolCallOnce(request(), f.data, session, &call)
		if err != nil {
			t.Fatal(err)
		}
		var receipt struct{ ID, SlugID string }
		if err := json.Unmarshal(result, &receipt); err != nil || receipt.ID == "" {
			t.Fatalf("receipt %s: %v", result, err)
		}
		return receipt.ID, call.Change
	}
	count := func() int { return len(documentsTitled(t, f.s.store, "Replay checklist")) }

	first, change := run("call_1")
	if again, againChange := run("call_1"); again != first || againChange == nil || againChange.EntityID != first {
		t.Fatalf("retried call answered %s (%+v), want %s", again, againChange, first)
	}
	if count() != 1 {
		t.Fatalf("documents after a retry = %d, want 1", count())
	}

	// The same call stored in the conversation (another server, after a restart) answers from there.
	stored := domain.AgentToolCall{ID: "call_2", Name: "save_document", Arguments: arguments, Status: "completed", Change: change, Result: json.RawMessage(`{"id":"` + first + `","slugId":"replay"}`)}
	session.Messages = []domain.AgentMessage{{ID: "reply", Role: "assistant", Parts: []domain.AgentMessagePart{{ID: "reply_tool_0", Type: "toolCall", Status: "completed", ToolCall: &stored}}}}
	if replayed, _ := run("call_2"); replayed != first || count() != 1 {
		t.Fatalf("stored call answered %s with %d documents, want %s and 1", replayed, count(), first)
	}

	// Two copies of one call at once: one writes, the other waits for its result.
	var wait sync.WaitGroup
	ids := make([]string, 2)
	for index := range ids {
		wait.Add(1)
		go func() {
			defer wait.Done()
			ids[index], _ = run("call_3")
		}()
	}
	wait.Wait()
	if ids[0] != ids[1] || ids[0] == first || count() != 2 {
		t.Fatalf("concurrent copies answered %v with %d documents, want one new document", ids, count())
	}

	// The same id with other arguments is another call (providers that number ids per response reuse them).
	call := domain.AgentToolCall{ID: "call_1", Name: "save_document", Arguments: json.RawMessage(`{"title":"Replay checklist","content":"Other body"}`)}
	if _, err := f.s.executeAgentToolCallOnce(request(), f.data, session, &call); err != nil || count() != 3 {
		t.Fatalf("a new call with a reused id: %v, documents = %d, want 3", err, count())
	}
}

// The agent's writes publish their realtime events without the asking tab's client id: that tab applied nothing
// itself, and dropped them as its own echo, so the document the reply created was missing there.
func TestAgentToolWritesReachTheAskingTab(t *testing.T) {
	f := newAgentAttributionFixture(t)
	var mu sync.Mutex
	clients := map[string]string{}
	f.s.store.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		mu.Lock()
		defer mu.Unlock()
		clients[event.Type] = event.ClientID
	})
	ctx := context.WithValue(context.Background(), authUserContextKey{}, f.data.Viewer)
	ctx = store.ContextWithActor(ctx, f.data.Viewer)
	ctx = store.ContextWithRealtimeClient(ctx, "tab-1")
	request := httptest.NewRequest("POST", "/api/agent/sessions/stream?workspace="+f.data.Workspace.URLKey, nil).WithContext(ctx)
	if _, err := f.s.executeAgentTool(request, f.data, domain.AgentToolCall{Name: "save_document", Arguments: json.RawMessage(`{"title":"Tab doc"}`)}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	client, published := clients["document.created"]
	if !published || client != "" {
		t.Fatalf("document.created published=%v with client %q, want no client id (events: %v)", published, client, clients)
	}
}

// The realtime metadata holds no documents, so document events were dropped for everyone. They now follow the
// document's own access: a member gets the events of documents they may open, not of private ones, and a deleted
// document only as the bare signal.
func TestPagedRealtimeDocumentEventsFollowDocumentAccess(t *testing.T) {
	repo := pulseTestRepo(t)
	now := time.Now().UTC()
	admin := domain.User{ID: "usr_admin", Name: "Test admin"}
	err := repo.MutateWorkspace(t.Context(), "test-workspace", "test.documents", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents,
			domain.Document{ID: "doc_shared", SlugID: "shared-0123456789ab", Title: "Shared", Creator: admin, CreatedAt: now, UpdatedAt: now, Permissions: []domain.DocumentPermission{{ID: "p1", DocumentID: "doc_shared", SubjectType: "user", SubjectID: admin.ID, Role: "owner"}, {ID: "p2", DocumentID: "doc_shared", SubjectType: "workspace", Role: "editor"}}},
			domain.Document{ID: "doc_private", SlugID: "private-0123456789ab", Title: "Private", Creator: admin, CreatedAt: now, UpdatedAt: now, Permissions: []domain.DocumentPermission{{ID: "p3", DocumentID: "doc_private", SubjectType: "user", SubjectID: admin.ID, Role: "owner"}}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateMemberRole(t.Context(), repo.Bootstrap().Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	s := &server{store: repo}
	viewer := func(user domain.User) *http.Request {
		return httptest.NewRequest("GET", "/api/realtime/events?workspace=test-workspace", nil).WithContext(context.WithValue(context.Background(), authUserContextKey{}, user))
	}
	member := domain.User{ID: "usr_member", Name: "Test member"}
	payload := json.RawMessage(`{"entity":{"title":"secret body"}}`)
	for _, check := range []struct {
		user     domain.User
		document string
		visible  bool
		bare     bool
	}{
		{admin, "doc_private", true, false},
		{member, "doc_shared", true, false},
		{member, "doc_private", false, false},
		{member, "doc_gone", true, true},
	} {
		event, visible, err := s.pagedRealtimeEvent(viewer(check.user), domain.RealtimeEvent{ID: "event_1", Seq: 7, Type: "document.created", AggregateID: check.document, Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		if visible != check.visible || (len(event.Payload) == 0) != check.bare || event.Seq != 7 {
			t.Fatalf("%s / %s: visible=%v payload=%s seq=%d, want visible=%v bare=%v", check.user.ID, check.document, visible, event.Payload, event.Seq, check.visible, check.bare)
		}
	}
}

// A reply stream that is quiet (the model reasons for a while before its first call) gets comment lines, so a
// reverse proxy's idle timeout does not close it while the reply goes on.
func TestAgentStreamSendsKeepAlivesWhileQuiet(t *testing.T) {
	previous := agentStreamKeepAlive
	agentStreamKeepAlive = 20 * time.Millisecond
	t.Cleanup(func() { agentStreamKeepAlive = previous })
	handler, _ := newAgentChatTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		time.Sleep(200 * time.Millisecond)
		chatChunk(w, map[string]any{"content": "Done."}, "stop")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	host := httptest.NewServer(handler)
	defer host.Close()
	response, err := host.Client().Post(host.URL+"/api/agent/sessions/stream", "application/json", strings.NewReader(`{"message":"Hello","location":"page"}`))
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(stream), ": keep-alive\n\n") || !strings.Contains(string(stream), "session.completed") {
		t.Fatalf("stream without keep-alives or completion:\n%s", stream)
	}
}

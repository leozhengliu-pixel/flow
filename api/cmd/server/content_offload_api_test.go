package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Offloaded bodies (older document versions, long agent replies and tool
// results) are not loaded with the workspace; the routes that show or use
// them load them on demand.
func TestOffloadedBodiesLoadWhereTheyAreServed(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	viewer := repository.Bootstrap().Viewer
	now := time.Now().UTC().Truncate(time.Second)
	started := now.Add(-time.Hour)
	old := strings.Repeat("The first plan. ", 200)
	document := domain.Document{ID: "document_offload", SlugID: "plan-offload", Title: "Plan", Content: "Now", ContentData: map[string]any{"type": "doc"}, Creator: viewer, TeamIDs: []string{}, ProjectIDs: []string{}, SubscriberIDs: []string{viewer.ID}, CreatedAt: now, UpdatedAt: now, Version: 1, Revisions: []domain.DocumentRevision{
		{ID: "revision_new", DocumentID: "document_offload", Title: "Plan", Content: "Now", ContentData: map[string]any{"type": "doc"}, Author: viewer, CreatedAt: now, StartedAt: &started},
		{ID: "revision_old", DocumentID: "document_offload", Title: "Old plan", Content: old, ContentData: map[string]any{"type": "doc", "text": old}, Author: viewer, CreatedAt: now.Add(-2 * time.Hour), StartedAt: &started},
	}}
	reply := strings.Repeat("A long answer. ", 700)
	result, _ := json.Marshal(map[string]string{"body": strings.Repeat("issue text ", 1000)})
	session := domain.AgentSession{ID: "agent_session_offload", SlugID: "chat", UserID: viewer.ID, Title: "Chat", Location: "page", IssueIDs: []string{}, SkillIDs: []string{}, CreatedAt: now, UpdatedAt: now, Messages: []domain.AgentMessage{
		{ID: "message_user", Role: "user", Content: "Find it", CreatedAt: now},
		{ID: "message_reply", Role: "assistant", Content: reply, CreatedAt: now, Parts: []domain.AgentMessagePart{
			{ID: "part_tool", Type: "tool", ToolCall: &domain.AgentToolCall{ID: "call_1", Name: "search_issues", Arguments: json.RawMessage(`{"query":"it"}`), Result: result, Status: "completed"}},
			{ID: "part_text", Type: "text", Text: reply},
		}},
	}}
	if err := repository.MutateWorkspace(ctx, "test-workspace", "test.seed", "seed", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, document)
		data.AgentSessions = append(data.AgentSessions, session, domain.AgentSession{ID: "agent_session_small", SlugID: "small", UserID: viewer.ID, Title: "Small", Location: "page", IssueIDs: []string{}, SkillIDs: []string{}, CreatedAt: now, UpdatedAt: now, Messages: []domain.AgentMessage{{ID: "message_small", Role: "user", Content: "Hi", CreatedAt: now}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err = store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	if database, objects := repository.OffloadedContentReads(); database+objects != 0 {
		t.Fatalf("opening the workspace read %d bodies", database+objects)
	}

	// The workspace carries the old version without its body.
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, item := range bootstrap.Documents {
		if item.ID == document.ID && (item.Revisions[1].Content != "" || item.Revisions[1].Offloaded["body"].Key == "") {
			t.Fatalf("bootstrap version = %+v", item.Revisions[1])
		}
	}
	// History loads the bodies (as MCP's document history does through it).
	history := requestJSON[struct {
		Nodes []domain.DocumentRevision `json:"nodes"`
	}](t, handler, http.MethodGet, "/api/documents/"+document.ID+"/history", nil, http.StatusOK)
	if len(history.Nodes) != 2 || history.Nodes[1].Content != old || history.Nodes[1].Offloaded != nil {
		t.Fatalf("history versions = %+v", history.Nodes)
	}
	// The document API and MCP send versions whole, as before.
	got := requestJSON[domain.Document](t, handler, http.MethodGet, "/api/documents/"+document.ID, nil, http.StatusOK)
	if got.Revisions[1].Content != old || got.Revisions[1].Offloaded != nil {
		t.Fatalf("GET document version = %+v", got.Revisions[1].Offloaded)
	}
	listed := requestJSON[[]domain.Document](t, handler, http.MethodGet, "/api/documents", nil, http.StatusOK)
	for _, item := range listed {
		if item.ID == document.ID && item.Revisions[1].Content != old {
			t.Fatal("listed document lost its version body")
		}
	}
	tool, err := (&server{store: repository, authDisabled: true}).runFlowTool(ctx, mcpActor{WorkspaceKey: "test-workspace", User: viewer}, "get_document", map[string]any{"id": document.ID})
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(tool); !strings.Contains(string(raw), "The first plan. The first plan.") || strings.Contains(string(raw), "_offloaded") {
		t.Fatal("MCP get_document lost the version body")
	}
	// Restoring an offloaded version restores its content.
	restored := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/restore/revision_old", nil, http.StatusOK)
	if restored.Content != old || restored.Title != "Old plan" {
		t.Fatalf("restored = %q / %q", restored.Title, restored.Content[:min(40, len(restored.Content))])
	}

	// Lists send a partly offloaded conversation without messages; opening
	// it loads everything.
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	for _, item := range sessions {
		if item.ID == session.ID && len(item.Messages) != 0 {
			t.Fatalf("listed session carries %d messages", len(item.Messages))
		}
	}
	// Every response that leaves the messages out says so, so the app never
	// takes the empty list for the conversation; a whole one has no marker.
	assertAgentSummaryMarked(t, "GET /api/agent/sessions", sessions, session.ID)
	assertAgentSummaryMarked(t, "GET /api/bootstrap", requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).AgentSessions, session.ID)
	assertAgentSummaryMarked(t, "GET /api/issue-records/bootstrap", requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/issue-records/bootstrap", nil, http.StatusOK).AgentSessions, session.ID)
	opened := requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+session.ID, nil, http.StatusOK)
	if opened.MessagesOmitted {
		t.Fatal("the opened conversation is marked as a summary")
	}
	if raw, _ := json.Marshal(opened); strings.Contains(string(raw), "messagesOmitted") {
		t.Fatal("the opened conversation carries the summary marker")
	}
	wantMessages, _ := json.Marshal(session.Messages)
	gotMessages, _ := json.Marshal(opened.Messages)
	if string(gotMessages) != string(wantMessages) {
		t.Fatalf("opened conversation differs\n got %.200s\nwant %.200s", gotMessages, wantMessages)
	}
	// The model's history gets the full replies and tool results.
	data, _ := repository.WorkspaceMetadataFields("test-workspace", "agentSessions")
	var lazy domain.AgentSession
	for _, item := range data.AgentSessions {
		if item.ID == session.ID {
			lazy = item
		}
	}
	request, _ := http.NewRequest(http.MethodGet, "/", nil)
	replay := agentProviderHistory((&server{store: repository}).withAgentBodies(request.WithContext(ctx), lazy), "system", nil)
	encoded, _ := json.Marshal(replay)
	text := string(encoded)
	if !strings.Contains(text, "issue text issue text") || !strings.Contains(text, "A long answer. A long answer.") {
		t.Fatal("the model's history is missing offloaded replies or tool results")
	}
}

// assertAgentSummaryMarked checks that the offloaded conversation is sent as a
// marked summary and a conversation kept whole is sent with its messages and
// no marker.
func assertAgentSummaryMarked(t *testing.T, route string, sessions []domain.AgentSession, summaryID string) {
	t.Helper()
	found := 0
	for _, item := range sessions {
		switch item.ID {
		case summaryID:
			found++
			if !item.MessagesOmitted || len(item.Messages) != 0 {
				t.Fatalf("%s: summary marked=%v with %d messages", route, item.MessagesOmitted, len(item.Messages))
			}
		case "agent_session_small":
			found++
			if item.MessagesOmitted || len(item.Messages) != 1 {
				t.Fatalf("%s: whole conversation marked=%v with %d messages", route, item.MessagesOmitted, len(item.Messages))
			}
		}
	}
	if found != 2 {
		t.Fatalf("%s: listed %d of the 2 conversations", route, found)
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scriptedAgentCall struct {
	name string
	args map[string]any
}

// scriptedAgentProvider answers each chat turn with the next scripted tool call, then with "Done.".
func scriptedAgentProvider(t *testing.T, script func() []scriptedAgentCall) *httptest.Server {
	t.Helper()
	var calls atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		index := int(calls.Add(1)) - 1
		steps := script()
		if index < len(steps) {
			args, _ := json.Marshal(steps[index].args)
			item, _ := json.Marshal(map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": fmt.Sprintf("item_%d", index), "call_id": fmt.Sprintf("call_%d", index), "type": "function_call", "name": steps[index].name, "arguments": string(args)}})
			done, _ := json.Marshal(map[string]any{"type": "response.function_call_arguments.done", "item_id": fmt.Sprintf("item_%d", index), "arguments": string(args)})
			_, _ = fmt.Fprintf(w, "event: response.output_item.added\ndata: %s\n\nevent: response.function_call_arguments.done\ndata: %s\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", item, done)
			return
		}
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Done.\"}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
}

func newAgentChangeTestServer(t *testing.T, script func() []scriptedAgentCall) (http.Handler, *store.SQLiteStore) {
	t.Helper()
	provider := scriptedAgentProvider(t, script)
	t.Cleanup(provider.Close)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}, agentClient: provider.Client()}
	handler := newHandler(s)
	t.Cleanup(func() { s.stopDeliveryScheduler(context.Background()) })
	return handler, repository
}

// runAgentChangeChat sends one chat message through the streaming endpoint and returns the saved reply.
func runAgentChangeChat(t *testing.T, handler http.Handler, message string) (domain.AgentSession, domain.AgentMessage) {
	t.Helper()
	host := httptest.NewServer(handler)
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"message": message, "location": "page"})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, host.URL+"/api/agent/sessions/stream", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(stream), "session.completed") {
		t.Fatalf("chat did not complete: %s", stream)
	}
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	if len(sessions) == 0 {
		t.Fatal("no chat saved")
	}
	session := requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+sessions[0].ID, nil, http.StatusOK)
	reply := session.Messages[len(session.Messages)-1]
	if reply.Role != "assistant" {
		t.Fatalf("last message is not a reply: %#v", reply)
	}
	return session, reply
}

func agentChangeParts(message domain.AgentMessage) []domain.AgentMessagePart {
	var parts []domain.AgentMessagePart
	for _, part := range message.Parts {
		if part.ToolCall != nil && part.ToolCall.Change != nil {
			parts = append(parts, part)
		}
	}
	return parts
}

func findAgentChangePart(t *testing.T, message domain.AgentMessage, action, entity string) domain.AgentMessagePart {
	t.Helper()
	for _, part := range agentChangeParts(message) {
		if part.ToolCall.Change.Action == action && part.ToolCall.Change.EntityType == entity {
			return part
		}
	}
	t.Fatalf("no %s %s change in %#v", action, entity, message.Parts)
	return domain.AgentMessagePart{}
}

func agentChangeProject(t *testing.T, repository *store.SQLiteStore, name string) (domain.Project, bool) {
	t.Helper()
	data, ok := repository.WorkspaceMetadata("")
	if !ok {
		t.Fatal("workspace metadata missing")
	}
	index := slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.Name == name })
	if index < 0 {
		return domain.Project{}, false
	}
	return data.Projects[index], true
}

func TestAgentChangeSetRecordsWritesAndUndoesThem(t *testing.T) {
	var teamKey string
	handler, repository := newAgentChangeTestServer(t, func() []scriptedAgentCall {
		return []scriptedAgentCall{
			{"save_project", map[string]any{"name": "QA fix – Agent project", "setTeams": []string{teamKey}, "description": "Original plan"}},
			{"save_project", map[string]any{"id": "QA fix – Agent project", "description": "Agent rewrite with risks", "summary": "Agent summary"}},
			{"save_issue", map[string]any{"team": teamKey, "title": "QA fix – Agent issue"}},
			{"list_teams", map[string]any{}},
		}
	})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	teamKey = bootstrap.Teams[0].Key
	session, reply := runAgentChangeChat(t, handler, "Create a project, rewrite it and file an issue")

	// Reads leave no change; each write records one.
	if got := len(agentChangeParts(reply)); got != 3 {
		t.Fatalf("expected 3 recorded changes, got %d: %#v", got, reply.Parts)
	}
	created := findAgentChangePart(t, reply, "created", "project")
	if change := created.ToolCall.Change; change.Label != "QA fix – Agent project" || change.EntityID == "" || !change.Undoable {
		t.Fatalf("created project change = %#v", change)
	}
	updated := findAgentChangePart(t, reply, "updated", "project")
	change := updated.ToolCall.Change
	if !change.Undoable || !slices.Equal(change.Fields, []string{"description", "summary"}) {
		t.Fatalf("updated project change = %#v", change)
	}
	var before, after map[string]any
	_ = json.Unmarshal(change.Before, &before)
	_ = json.Unmarshal(change.After, &after)
	if before["description"] != "Original plan" || after["description"] != "Agent rewrite with risks" || after["summary"] != "Agent summary" {
		t.Fatalf("snapshots before=%v after=%v", before, after)
	}
	issuePart := findAgentChangePart(t, reply, "created", "issue")
	if label := issuePart.ToolCall.Change.Label; !strings.HasPrefix(label, teamKey+"-") {
		t.Fatalf("created issue label = %q", label)
	}
	if link := issuePart.ToolCall.Change.URL; !strings.Contains(link, "/issue/"+issuePart.ToolCall.Change.Label) || strings.Contains(link, "://") {
		t.Fatalf("created issue path = %q", link)
	}
	if link := created.ToolCall.Change.URL; !strings.Contains(link, "/project/") {
		t.Fatalf("created project path = %q", link)
	}

	base := "/api/agent/sessions/" + session.ID + "/messages/" + reply.ID
	// Undo the description edit only.
	undone := requestJSON[domain.AgentSession](t, handler, http.MethodPost, base+"/undo", map[string]any{"partIds": []string{updated.ID}}, http.StatusOK)
	project, _ := agentChangeProject(t, repository, "QA fix – Agent project")
	if project.Description != "Original plan" || project.Summary != "" {
		t.Fatalf("undo did not restore the project: %q / %q", project.Description, project.Summary)
	}
	if state := findAgentChangePart(t, undone.Messages[len(undone.Messages)-1], "updated", "project").ToolCall.Change.State; state != "undone" {
		t.Fatalf("change state after undo = %q", state)
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, base+"/undo", map[string]any{"partIds": []string{updated.ID}}, http.StatusConflict)

	// Restore re-applies it.
	requestJSON[domain.AgentSession](t, handler, http.MethodPost, base+"/restore", map[string]any{"partIds": []string{updated.ID}}, http.StatusOK)
	project, _ = agentChangeProject(t, repository, "QA fix – Agent project")
	if project.Description != "Agent rewrite with risks" || project.Summary != "Agent summary" {
		t.Fatalf("restore did not re-apply: %q / %q", project.Description, project.Summary)
	}

	// Someone edits the description afterwards: Undo leaves it and reverts only the untouched summary.
	requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+project.ID, map[string]any{"description": "Human edit"}, http.StatusOK)
	partial := requestJSON[domain.AgentSession](t, handler, http.MethodPost, base+"/undo", map[string]any{"partIds": []string{updated.ID}}, http.StatusOK)
	project, _ = agentChangeProject(t, repository, "QA fix – Agent project")
	if project.Description != "Human edit" || project.Summary != "" {
		t.Fatalf("partial undo touched the human edit: %q / %q", project.Description, project.Summary)
	}
	if note := findAgentChangePart(t, partial.Messages[len(partial.Messages)-1], "updated", "project").ToolCall.Change.Note; !strings.Contains(note, "description") {
		t.Fatalf("partial undo note = %q", note)
	}
	// Restoring now conflicts on the description and still writes the summary back.
	requestJSON[domain.AgentSession](t, handler, http.MethodPost, base+"/restore", map[string]any{"partIds": []string{updated.ID}}, http.StatusOK)
	project, _ = agentChangeProject(t, repository, "QA fix – Agent project")
	if project.Description != "Human edit" || project.Summary != "Agent summary" {
		t.Fatalf("restore after conflict: %q / %q", project.Description, project.Summary)
	}
	// Every changed field edited since: nothing is written and the request is refused.
	requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+project.ID, map[string]any{"summary": "Human summary"}, http.StatusOK)
	refused := requestJSON[map[string]any](t, handler, http.MethodPost, base+"/undo", map[string]any{"partIds": []string{updated.ID}}, http.StatusConflict)
	if !strings.Contains(fmt.Sprint(refused), "changed after the agent") {
		t.Fatalf("conflict message = %v", refused)
	}
	project, _ = agentChangeProject(t, repository, "QA fix – Agent project")
	if project.Description != "Human edit" || project.Summary != "Human summary" {
		t.Fatalf("refused undo wrote anyway: %q / %q", project.Description, project.Summary)
	}

	// Undoing the whole reply removes what it created (to the trash), newest first; Restore brings it back.
	requestJSON[domain.AgentSession](t, handler, http.MethodPost, base+"/undo", map[string]any{"partIds": []string{created.ID, issuePart.ID}}, http.StatusOK)
	if _, ok := agentChangeProject(t, repository, "QA fix – Agent project"); ok {
		t.Fatal("undo did not delete the created project")
	}
	trash := repository.TrashSnapshot("")
	if !slices.ContainsFunc(trash, func(entry domain.TrashEntry) bool { return entry.ResourceID == issuePart.ToolCall.Change.EntityID }) {
		t.Fatal("undo did not move the created issue to the trash")
	}
	requestJSON[domain.AgentSession](t, handler, http.MethodPost, base+"/restore", map[string]any{"partIds": []string{created.ID, issuePart.ID}}, http.StatusOK)
	if _, ok := agentChangeProject(t, repository, "QA fix – Agent project"); !ok {
		t.Fatal("restore did not bring the project back")
	}
	data, _ := repository.WorkspaceMetadata("")
	if issue, err := repository.IssueRecord(t.Context(), data.Workspace.URLKey, issuePart.ToolCall.Change.EntityID); err != nil || issue.Title != "QA fix – Agent issue" {
		t.Fatalf("restore did not bring the issue back: %v %#v", err, issue.Title)
	}
}

func TestAgentChangeSetDocumentContentAndViewCreate(t *testing.T) {
	handler, repository := newAgentChangeTestServer(t, func() []scriptedAgentCall {
		return []scriptedAgentCall{
			{"save_document", map[string]any{"title": "QA fix – Agent doc", "content": "First draft"}},
			{"save_document", map[string]any{"id": "QA fix – Agent doc", "content": "First draft\n\nAdded risks section"}},
			{"save_view", map[string]any{"name": "QA fix – Agent view"}},
		}
	})
	session, reply := runAgentChangeChat(t, handler, "Write a doc and a view")
	document := findAgentChangePart(t, reply, "updated", "document")
	if change := document.ToolCall.Change; !change.Undoable || !slices.Equal(change.Fields, []string{"content"}) {
		t.Fatalf("document change = %#v", change)
	}
	view := findAgentChangePart(t, reply, "created", "view")
	base := "/api/agent/sessions/" + session.ID + "/messages/" + reply.ID
	requestJSON[domain.AgentSession](t, handler, http.MethodPost, base+"/undo", map[string]any{"partIds": []string{document.ID, view.ID}}, http.StatusOK)
	data, _ := repository.WorkspaceMetadata("")
	docIndex := slices.IndexFunc(data.Documents, func(item domain.Document) bool { return item.ID == document.ToolCall.Change.EntityID })
	if docIndex < 0 || strings.TrimSpace(data.Documents[docIndex].Content) != "First draft" {
		t.Fatalf("document content not reverted: %#v", data.Documents)
	}
	if slices.ContainsFunc(data.SavedViews, func(item domain.SavedView) bool { return item.Name == "QA fix – Agent view" }) {
		t.Fatal("created view not removed")
	}
	// A view has no trash: Restore creates it again from the agent's arguments and records the new id.
	restored := requestJSON[domain.AgentSession](t, handler, http.MethodPost, base+"/restore", map[string]any{"partIds": []string{view.ID}}, http.StatusOK)
	data, _ = repository.WorkspaceMetadata("")
	index := slices.IndexFunc(data.SavedViews, func(item domain.SavedView) bool { return item.Name == "QA fix – Agent view" })
	if index < 0 {
		t.Fatal("view not re-created")
	}
	if id := findAgentChangePart(t, restored.Messages[len(restored.Messages)-1], "created", "view").ToolCall.Change.EntityID; id != data.SavedViews[index].ID {
		t.Fatalf("restored view id %q, want %q", id, data.SavedViews[index].ID)
	}
}

func TestAgentChangeSetOnlyTheChatOwnerCanUndo(t *testing.T) {
	handler, repository := newAgentChangeTestServer(t, func() []scriptedAgentCall {
		return []scriptedAgentCall{{"save_document", map[string]any{"title": "QA fix – Owner doc", "content": "Body"}}}
	})
	session, reply := runAgentChangeChat(t, handler, "Write a doc")
	// Hand the chat to another member: the viewer can no longer see it, so they cannot undo its writes.
	err := repository.MutateWorkspace(t.Context(), "", "agent.session_moved", session.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.AgentSessions {
			if data.AgentSessions[index].ID == session.ID {
				data.AgentSessions[index].UserID = "someone_else"
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/messages/"+reply.ID+"/undo", map[string]any{}, http.StatusNotFound)
	data, _ := repository.WorkspaceMetadata("")
	if !slices.ContainsFunc(data.Documents, func(item domain.Document) bool { return item.Title == "QA fix – Owner doc" }) {
		t.Fatal("a refused undo deleted the document")
	}
}

func TestAgentChangeTargetIsDataDriven(t *testing.T) {
	cases := []struct {
		tool, entity, action string
		args                 map[string]any
		ok                   bool
	}{
		{"save_issue", "issue", "created", map[string]any{}, true},
		{"mcp__flow.save_issue", "issue", "updated", map[string]any{"id": "DEV-1"}, true},
		{"save_status_update", "initiative_update", "created", map[string]any{"type": "initiative"}, true},
		{"save_agent_skill", "skill", "created", map[string]any{}, true},
		{"delete_issue", "issue", "deleted", map[string]any{"id": "DEV-1"}, true},
		{"triage_issue", "issue", "updated", map[string]any{"id": "DEV-1"}, true},
		{"save_comment", "", "", map[string]any{}, false},
		{"list_issues", "", "", map[string]any{}, false},
	}
	for _, item := range cases {
		entity, action, ok := agentChangeTarget(item.tool, item.args)
		if ok != item.ok || entity != item.entity || action != item.action {
			t.Errorf("%s → %q %q %v, want %q %q %v", item.tool, entity, action, ok, item.entity, item.action, item.ok)
		}
	}
	if !agentValuesEqual("a  b\n", "a b") || agentValuesEqual("a", "b") || !agentValuesEqual(nil, "") || !agentValuesEqual([]any{"x"}, []any{"x"}) {
		t.Fatal("agentValuesEqual")
	}
}

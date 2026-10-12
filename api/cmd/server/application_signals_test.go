package main

import (
	"encoding/json"
	"fmt"
	"testing"

	"flow/api/internal/domain"
)

func TestApplicationCommentConversationSignalsAndContinuation(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	app := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Comment agent", "scopes": []string{"read", "app:mentionable"}, "supportsClientCredentials": true}, data.Workspace.URLKey, 201)
	token := applicationFormToken(t, host, app, "read app:mentionable", 200)
	issue := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issues", map[string]any{"title": "Conversation", "teamId": data.Teams[0].ID}, data.Workspace.URLKey, 201)
	comment := authRequest[domain.Comment](t, admin, "POST", host.URL+"/api/issues/"+issue.ID+"/comments", map[string]any{"body": "Ask for a language", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": token.Subject}}}}}, data.Workspace.URLKey, 201)
	tasks, err := s.store.ListAgentTasks(t.Context(), data.Workspace.URLKey, issue.ID, token.Subject)
	if err != nil || len(tasks) != 1 || tasks[0].CommentID != comment.ID {
		t.Fatalf("source comment was not attached: %+v %v", tasks, err)
	}
	task := tasks[0]
	path := "/api/agent-tasks/" + task.ID + "/activities"
	emit := func(body map[string]any, status int) {
		t.Helper()
		body["expectedVersion"] = task.Version
		encoded, _ := json.Marshal(body)
		raw := applicationBearerRequest(t, host, token.Token, "POST", path, string(encoded), status)
		if status == 200 {
			if err := json.Unmarshal(raw, &task); err != nil {
				t.Fatal(err)
			}
		}
	}
	emit(map[string]any{"type": "elicitation", "body": "Language?", "signal": "select", "signalMetadata": map[string]any{"options": []any{map[string]string{"value": "Chinese"}, map[string]string{"value": "English"}}}}, 200)
	if task.Status != "awaitingInput" {
		t.Fatal("clarification did not wait for a human")
	}
	emit(map[string]any{"type": "prompt", "body": "Chinese"}, 403)
	task = authRequest[domain.AgentTask](t, admin, "POST", host.URL+path, map[string]any{"type": "prompt", "body": "Chinese", "expectedVersion": task.Version}, data.Workspace.URLKey, 200)
	emit(map[string]any{"content": map[string]string{"type": "thought", "body": "Preparing answer"}}, 200)
	emit(map[string]any{"type": "output", "body": "First part"}, 200)
	emit(map[string]any{"type": "response", "body": "- One\n- Two\n- Three"}, 200)
	if task.Status != "complete" {
		t.Fatal("response did not complete")
	}
	responseID := fmt.Sprintf("%020d", task.Version)
	task = authRequest[domain.AgentTask](t, admin, "POST", host.URL+path, map[string]any{"expectedVersion": task.Version, "type": "feedback", "activityId": responseID, "body": "👍", "enabled": true}, data.Workspace.URLKey, 200)
	if task.Status != "complete" {
		t.Fatal("reaction restarted a completed task")
	}
	emit(map[string]any{"type": "feedback", "activityId": responseID, "body": "👍", "enabled": true}, 403)
	authRequest[map[string]any](t, admin, "POST", host.URL+path, map[string]any{"expectedVersion": task.Version, "type": "feedback", "activityId": "another-task-activity", "body": "👍", "enabled": true}, data.Workspace.URLKey, 400)
	feedback, err := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, responseID)
	if err != nil || len(feedback) != 1 || feedback[0].Type != "feedback" || feedback[0].Metadata["activityId"] != responseID || feedback[0].ActorID != data.Viewer.ID {
		t.Fatal("feedback was not persisted with its target and actor", err)
	}
	task = authRequest[domain.AgentTask](t, admin, "POST", host.URL+path, map[string]any{"type": "prompt", "body": "Follow up", "expectedVersion": task.Version}, data.Workspace.URLKey, 200)
	if task.Status != "pending" {
		t.Fatal("completed conversation cannot continue")
	}
	emit(map[string]any{"type": "thought", "body": "Continuing"}, 200)
	old := task.Version
	task = authRequest[domain.AgentTask](t, admin, "POST", host.URL+path, map[string]any{"type": "prompt", "signal": "stop", "expectedVersion": task.Version}, data.Workspace.URLKey, 200)
	if task.Status != "canceled" || task.PendingTool != nil {
		t.Fatal("stop did not cancel")
	}
	stale, _ := json.Marshal(map[string]any{"type": "response", "body": "Stale answer", "expectedVersion": old})
	applicationBearerRequest(t, host, token.Token, "POST", path, string(stale), 409)
}

func TestApplicationSignalsRejectInvalidOptionsAndAuth(t *testing.T) {
	for _, test := range []struct {
		name, kind, signal string
		metadata           map[string]any
		app                bool
	}{
		{"duplicate", "elicitation", "select", map[string]any{"options": []any{map[string]any{"value": "a"}, map[string]any{"value": "a"}}}, true},
		{"empty", "elicitation", "select", map[string]any{"options": []any{}}, true},
		{"unsafe auth", "elicitation", "auth", map[string]any{"url": "javascript:alert(1)"}, true},
		{"human selection", "prompt", "select", map[string]any{"options": []any{map[string]any{"value": "a"}}}, false},
		{"application stop", "prompt", "stop", nil, true},
		{"orphan metadata", "response", "", map[string]any{"url": "https://example.test"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if validateApplicationSignal(test.kind, test.signal, test.metadata, test.app) == nil {
				t.Fatal("invalid signal accepted")
			}
		})
	}
	if err := validateApplicationSignal("elicitation", "auth", map[string]any{"url": "https://example.test/connect", "userId": "member", "providerName": "Example"}, true); err != nil {
		t.Fatal(err)
	}
}

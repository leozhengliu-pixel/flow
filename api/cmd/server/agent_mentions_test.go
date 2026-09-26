package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAgentMentionsReachThePromptAndSession(t *testing.T) {
	var mu sync.Mutex
	var lastPrompt string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		raw, _ := json.Marshal(payload["messages"])
		mu.Lock()
		lastPrompt = string(raw)
		mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "ok"}}}})
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, BaseURL: provider.URL, Model: "flow-test"}, agentClient: provider.Client()})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Projects) == 0 {
		t.Skip("fixture has no projects")
	}
	project := bootstrap.Projects[0]

	session := requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions", map[string]any{"message": "Hello", "location": "toolbar"}, http.StatusCreated)
	viewer := bootstrap.Viewer
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/messages", map[string]any{
		"message":    "How is @" + project.Name + " going for @" + viewer.DisplayName + "?",
		"projectIds": []string{project.ID},
		"userIds":    []string{viewer.ID},
		"mentions":   []map[string]string{{"type": "project", "id": project.ID, "label": project.Name}, {"type": "user", "id": viewer.ID, "label": viewer.DisplayName}},
	}, http.StatusOK)
	if len(session.UserIDs) != 1 {
		t.Fatalf("mentioned person not recorded: %#v", session.UserIDs)
	}
	var sent domain.AgentMessage
	for _, message := range session.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "going for") {
			sent = message
		}
	}
	if len(sent.Mentions) != 2 || sent.Mentions[1].Type != "user" {
		t.Fatalf("mentions not stored on the message: %#v", sent.Mentions)
	}
	if len(session.ProjectIDs) != 1 || session.ProjectIDs[0] != project.ID {
		t.Fatalf("mentioned project not recorded: %#v", session.ProjectIDs)
	}
	mu.Lock()
	prompt := lastPrompt
	mu.Unlock()
	if !strings.Contains(prompt, "Resources the user mentioned") || !strings.Contains(prompt, project.Name) || !strings.Contains(prompt, "Person "+viewer.DisplayName) {
		t.Fatalf("mentioned project missing from the prompt: %s", prompt)
	}

	requestJSON[any](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/messages", map[string]any{"message": "And this?", "projectIds": []string{"project_missing"}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/messages", map[string]any{"message": "Bad", "mentions": []map[string]string{{"type": "team", "id": "x", "label": "x"}}}, http.StatusBadRequest)
}

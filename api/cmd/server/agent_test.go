package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAgentChatUsesSelectedIssueContext(t *testing.T) {
	var providerMessages []agentChatMessage
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatalf("unexpected provider request: %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Model    string             `json:"model"`
			Messages []agentChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		providerMessages = payload.Messages
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "The selected issue is in progress."}}}})
	}))
	defer provider.Close()

	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, BaseURL: provider.URL, APIKey: "test-secret", Model: "flow-test"}, agentClient: provider.Client()})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Issues) == 0 {
		t.Fatal("seed must include an issue")
	}
	issue := bootstrap.Issues[0]
	reply := requestJSON[map[string]string](t, handler, http.MethodPost, "/api/agent/chat", map[string]any{"message": "Summarize this", "issueIds": []string{issue.ID}}, http.StatusOK)
	if reply["message"] != "The selected issue is in progress." || reply["model"] != "flow-test" {
		t.Fatalf("unexpected reply: %#v", reply)
	}
	if len(providerMessages) != 2 || !strings.Contains(providerMessages[0].Content, issue.Identifier) || !strings.Contains(providerMessages[0].Content, issue.Title) || providerMessages[1].Content != "Summarize this" {
		t.Fatalf("selected issue context missing: %#v", providerMessages)
	}
}

func TestAgentChatRejectsUnconfiguredProvider(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	requestJSON[any](t, handler, http.MethodPost, "/api/agent/chat", map[string]any{"message": "Hello", "issueIds": []string{"issue"}}, http.StatusServiceUnavailable)
}

func TestAgentSessionAndSkillLifecycle(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "Agent reply"}}}})
	}))
	defer provider.Close()
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, BaseURL: provider.URL, Model: "flow-test"}, agentClient: provider.Client()})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	skill := requestJSON[domain.PersonalAgentSkill](t, handler, http.MethodPost, "/api/agent/skills", map[string]string{"name": "Release notes", "instructions": "Summarize shipped work."}, http.StatusCreated)
	if skill.Name != "Release notes" || skill.UserID != bootstrap.Viewer.ID {
		t.Fatalf("skill not persisted: %#v", skill)
	}
	session := requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions", map[string]any{"message": "Summarize this", "issueIds": []string{bootstrap.Issues[0].ID}, "skillIds": []string{skill.ID}, "location": "page"}, http.StatusCreated)
	if session.SlugID == "" || len(session.Messages) != 2 || session.Messages[1].Content != "Agent reply" || len(session.SkillIDs) != 1 {
		t.Fatalf("session not completed: %#v", session)
	}
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/messages", map[string]string{"message": "Continue"}, http.StatusOK)
	if len(session.Messages) != 4 {
		t.Fatalf("follow-up did not persist: %#v", session.Messages)
	}
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPatch, "/api/agent/sessions/"+session.ID, map[string]any{"favorite": true, "location": "toolbar"}, http.StatusOK)
	if !session.Favorite || session.Location != "toolbar" {
		t.Fatalf("session update failed: %#v", session)
	}
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	if len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Fatalf("session history failed: %#v", sessions)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/agent/sessions/"+session.ID, nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodDelete, "/api/agent/skills/"+skill.ID, nil, http.StatusNoContent)
	if remaining := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK); len(remaining) != 0 {
		t.Fatalf("session delete failed: %#v", remaining)
	}
}

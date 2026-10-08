package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAgentSessionReadState(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "Agent reply"}}}})
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, BaseURL: provider.URL, Model: "flow-test"}, agentClient: provider.Client()})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	workspace := bootstrap.Workspace.URLKey

	session := requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions", map[string]any{"message": "Summarize this", "location": "page"}, http.StatusCreated)
	if session.LastReadAt == nil {
		t.Fatalf("a new chat starts read by its author: %#v", session)
	}

	// A reply that lands after the owner last looked makes the chat unread.
	later := session.LastReadAt.Add(time.Minute)
	setUpdated := func(id string, at time.Time) {
		t.Helper()
		if err := repository.MutateWorkspace(context.Background(), workspace, "agent.message_completed", id, nil, func(data *domain.Bootstrap) error {
			for index := range data.AgentSessions {
				if data.AgentSessions[index].ID == id {
					data.AgentSessions[index].UpdatedAt = at
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	setUpdated(session.ID, later)
	session = requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+session.ID, nil, http.StatusOK)
	if agentSessionRead(session) {
		t.Fatalf("a later reply must make the chat unread: lastReadAt %v updatedAt %v", session.LastReadAt, session.UpdatedAt)
	}

	// Starring it does not count as reading it.
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPatch, "/api/agent/sessions/"+session.ID, map[string]any{"favorite": true}, http.StatusOK)
	if agentSessionRead(session) {
		t.Fatalf("an owner edit must keep an unread chat unread: %#v", session)
	}

	updatedAt := session.UpdatedAt
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPost, "/api/agent/sessions/"+session.ID+"/read", nil, http.StatusOK)
	if !agentSessionRead(session) || !session.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("marking read must cover the latest change without reordering history: %#v", session)
	}

	// The owner's own edits keep a read chat read.
	session = requestJSON[domain.AgentSession](t, handler, http.MethodPatch, "/api/agent/sessions/"+session.ID, map[string]any{"favorite": false}, http.StatusOK)
	if !agentSessionRead(session) {
		t.Fatalf("an owner edit must not make a read chat unread: %#v", session)
	}
	listed := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	if len(listed) != 1 || listed[0].LastReadAt == nil || !agentSessionRead(listed[0]) {
		t.Fatalf("read state must persist: %#v", listed)
	}

	// Another person's chat can't be marked read (or even found).
	foreignID := "agent_session_foreign"
	foreignUpdated := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if err := repository.MutateWorkspace(context.Background(), workspace, "agent.session_created", foreignID, nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = append(data.AgentSessions, domain.AgentSession{ID: foreignID, SlugID: "foreign", UserID: "someone-else", Title: "Private", Location: "page", IssueIDs: []string{}, SkillIDs: []string{}, Messages: []domain.AgentMessage{}, CreatedAt: foreignUpdated, UpdatedAt: foreignUpdated})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/agent/sessions/"+foreignID+"/read", nil, http.StatusNotFound)
	metadata, ok := repository.WorkspaceMetadata(workspace)
	if !ok {
		t.Fatal("workspace metadata missing")
	}
	for _, item := range metadata.AgentSessions {
		if item.ID == foreignID && item.LastReadAt != nil {
			t.Fatalf("another user's chat must stay untouched: %#v", item)
		}
	}
}

func TestAgentSessionsWithoutReadStampCountAsRead(t *testing.T) {
	at := time.Now().UTC()
	if !agentSessionRead(domain.AgentSession{UpdatedAt: at}) {
		t.Fatal("chats from before read tracking must not all light up as unread")
	}
	earlier := at.Add(-time.Second)
	if agentSessionRead(domain.AgentSession{UpdatedAt: at, LastReadAt: &earlier}) {
		t.Fatal("a change after the read stamp is unread")
	}
}

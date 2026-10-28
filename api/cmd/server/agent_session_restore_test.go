package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestAgentSessionSoftDeleteRestoreAndPurge(t *testing.T) {
	srv, handler := newLoopTestServer(t, &fakeLoopProvider{})
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	workspace := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Workspace.URLKey
	attachments := []map[string]any{{"name": "notes.md", "contentType": "text/markdown", "size": 30, "content": "# Notes\nKeep me."}}
	stream := streamAgent(t, srv, host, "/api/agent/sessions/stream", map[string]any{"message": "Hello", "location": "page", "attachments": attachments}, nil)
	match := regexp.MustCompile(`"id":"(agent_session_[0-9a-z_]+)"`).FindStringSubmatch(stream)
	if match == nil {
		t.Fatalf("no session in stream: %s", stream)
	}
	id := match[1]
	path := "/api/agent/sessions/" + id
	session := requestJSON[domain.AgentSession](t, handler, http.MethodGet, path, nil, http.StatusOK)
	file := session.Messages[0].Attachments[0].URL
	onDisk := func() bool {
		_, err := os.Stat(filepath.Join(srv.uploadPath, strings.TrimPrefix(file, "/uploads/")))
		return err == nil
	}
	if !onDisk() {
		t.Fatal("attachment not stored")
	}

	requestJSON[any](t, handler, http.MethodDelete, path, nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodGet, path, nil, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodPatch, path, map[string]any{"title": "x"}, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodDelete, path, nil, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodPost, path+"/messages", map[string]any{"message": "again"}, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodPost, path+"/messages/stream", map[string]any{"message": "again"}, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodPost, path+"/read", nil, http.StatusNotFound)
	if list := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK); len(list) != 0 {
		t.Fatalf("deleted chat in list: %#v", list)
	}
	if boot := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK); len(boot.AgentSessions) != 0 {
		t.Fatalf("deleted chat in bootstrap: %#v", boot.AgentSessions)
	}
	if !onDisk() {
		t.Fatal("soft delete removed the upload")
	}

	restored := requestJSON[domain.AgentSession](t, handler, http.MethodPost, path+"/restore", nil, http.StatusOK)
	if restored.ID != id || restored.DeletedAt != nil || len(restored.Messages) != len(session.Messages) {
		t.Fatalf("restore = %#v", restored)
	}
	requestJSON[domain.AgentSession](t, handler, http.MethodGet, path, nil, http.StatusOK)
	if !onDisk() {
		t.Fatal("restored chat lost its file")
	}
	// Restoring a chat that is not deleted is a 404 too.
	requestJSON[any](t, handler, http.MethodPost, path+"/restore", nil, http.StatusNotFound)

	// Another user's chat is not restorable.
	requestJSON[any](t, handler, http.MethodDelete, path, nil, http.StatusNoContent)
	setOwner := func(owner string) {
		t.Helper()
		if err := srv.store.MutateWorkspace(context.Background(), workspace, "agent.session_updated", id, nil, func(data *domain.Bootstrap) error {
			data.AgentSessions[0].UserID = owner
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	owner := session.UserID
	setOwner("someone-else")
	requestJSON[any](t, handler, http.MethodPost, path+"/restore", nil, http.StatusNotFound)
	setOwner(owner)

	// Inside the window nothing is purged; past it the chat and its upload go.
	if n, err := srv.purgeDeletedAgentSessions(context.Background(), workspace, time.Now().Add(-time.Hour)); err != nil || n != 0 {
		t.Fatalf("early purge = %d, %v", n, err)
	}
	if n, err := srv.purgeDeletedAgentSessions(context.Background(), workspace, time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	requestJSON[any](t, handler, http.MethodPost, path+"/restore", nil, http.StatusNotFound)
	if onDisk() {
		t.Fatal("purged chat's upload still exists")
	}
}

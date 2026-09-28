package main

import (
	"encoding/json"
	"flow/api/internal/domain"
	"flow/api/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestCreateProjectRecordsCreator(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()

	admin := authClient(t)
	session := authRequest[domain.AuthSession](t, admin, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	created := authRequest[domain.Project](t, admin, http.MethodPost, server.URL+"/api/projects", map[string]any{"name": "Creator project", "teamIds": []string{bootstrap.Teams[0].ID}}, "test-workspace", http.StatusCreated)
	if created.CreatorID == "" || created.CreatorID != session.User.ID || created.Creator == nil || created.Creator.ID != session.User.ID {
		t.Fatalf("project creator = %q %#v, want %q", created.CreatorID, created.Creator, session.User.ID)
	}
	reloaded := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	found := false
	for _, project := range reloaded.Projects {
		if project.ID == created.ID {
			found = true
			if project.CreatorID != session.User.ID || project.Creator == nil {
				t.Fatalf("persisted project creator = %q %#v", project.CreatorID, project.Creator)
			}
		}
	}
	if !found {
		t.Fatalf("created project %q missing from bootstrap", created.ID)
	}
}

func TestProjectHistoryRecordsPropertyChanges(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	created := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "History project", "teamIds": []string{"team_test"}}, http.StatusCreated)
	if created.CreatorID == "" || created.Creator == nil {
		t.Fatalf("auth-disabled project creator missing: %#v", created)
	}
	requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{"priority": 2, "targetDate": "2026-12-01"}, http.StatusOK)
	// A no-op update must not add a history entry.
	requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{"priority": 2}, http.StatusOK)

	page := requestJSON[struct {
		Nodes []domain.AuditLogEntry `json:"nodes"`
	}](t, handler, http.MethodGet, "/api/projects/"+created.ID+"/history", nil, http.StatusOK)
	var updates []domain.AuditLogEntry
	for _, item := range page.Nodes {
		if item.Action == "updated" {
			updates = append(updates, item)
		}
	}
	if len(updates) != 1 {
		t.Fatalf("history updates = %#v", page.Nodes)
	}
	raw, _ := json.Marshal(updates[0].Metadata["changes"])
	var changes []projectPropertyChange
	if err := json.Unmarshal(raw, &changes); err != nil {
		t.Fatal(err)
	}
	want := []projectPropertyChange{{Field: "priority", From: "", To: "High"}, {Field: "targetDate", From: "", To: "2026-12-01"}}
	if len(changes) != len(want) || changes[0] != want[0] || changes[1] != want[1] {
		t.Fatalf("history changes = %#v, want %#v", changes, want)
	}
}

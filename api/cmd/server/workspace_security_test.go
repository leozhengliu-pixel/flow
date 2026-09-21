package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestWorkspaceSoftDeleteAndCancel(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "soft-delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	key := "test-workspace"

	scheduled := requestJSON[domain.Workspace](t, handler, http.MethodDelete, "/api/workspaces/"+key, nil, http.StatusOK)
	if scheduled.DeletionRequestedAt == nil {
		t.Fatal("expected deletionRequestedAt after schedule")
	}
	data, ok := repository.BootstrapFor(key)
	if !ok || data.Workspace.DeletionRequestedAt == nil {
		t.Fatal("workspace should remain after soft-delete")
	}

	canceled := requestJSON[domain.Workspace](t, handler, http.MethodPost, "/api/workspaces/"+key+"/cancel-deletion", nil, http.StatusOK)
	if canceled.DeletionRequestedAt != nil {
		t.Fatal("expected deletionRequestedAt cleared")
	}
}

func TestWorkspaceAccessStatusBranches(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "access-status.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer host.Close()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)

	missing := authRequest[map[string]any](t, client, http.MethodGet, host.URL+"/api/workspaces/does-not-exist/access-status", nil, "", http.StatusOK)
	if missing["reason"] != "not_found" {
		t.Fatalf("expected not_found, got %#v", missing["reason"])
	}

	okStatus := authRequest[map[string]any](t, client, http.MethodGet, host.URL+"/api/workspaces/test-workspace/access-status", nil, "", http.StatusOK)
	if okStatus["reason"] != "ok" || okStatus["hasMembership"] != true {
		t.Fatalf("expected ok membership, got %#v", okStatus)
	}
}

func TestCodingAgentCommitSigningPreference(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "commit-signing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	saved := requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{
		"codingAgentSettings": map[string]any{"commitSigningEnabled": true},
	}, http.StatusOK)
	if !saved.CodingAgentSettings.CommitSigningEnabled {
		t.Fatal("expected commitSigningEnabled")
	}
}

func TestAllowedAuthServicesPreference(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "allowed-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	saved := requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{
		"allowedAuthServices": []string{"saml", "google"},
	}, http.StatusOK)
	if !saved.GoogleAuthEnabled || saved.EmailAuthEnabled {
		t.Fatalf("expected google-only flags from allowedAuthServices, got google=%v email=%v", saved.GoogleAuthEnabled, saved.EmailAuthEnabled)
	}
}

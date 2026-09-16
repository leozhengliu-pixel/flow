package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAsksFeatureTogglePermissionsAndPersistence(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	dbPath := filepath.Join(t.TempDir(), "asks.db")
	repo, err := store.OpenSQLiteTestFixture(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	workspace := data.Workspace.URLKey
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	member, memberUser := verifiedAuthClient(t, host.URL, "Asks member", "asks-member@example.test")
	invite, err := repo.Invite(t.Context(), data.Workspace.ID, data.Viewer.ID, memberUser.Email, "member", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AcceptInvitation(t.Context(), invite.Token, memberUser.ID); err != nil {
		t.Fatal(err)
	}
	preferences := host.URL + "/api/workspace/preferences"
	patch := func(asks bool) map[string]any { return map[string]any{"featureFlags": map[string]bool{"asks": asks}} }
	authRequest[any](t, member, "PATCH", preferences, patch(false), workspace, http.StatusForbidden)
	for _, customers := range []bool{true, false} {
		authRequest[domain.WorkspaceSettings](t, admin, "PATCH", preferences, map[string]any{"featureFlags": map[string]bool{"customer-requests": customers}}, workspace, http.StatusOK)
		for _, asks := range []bool{false, true} {
			saved := authRequest[domain.WorkspaceSettings](t, admin, "PATCH", preferences, patch(asks), workspace, http.StatusOK)
			if saved.FeatureFlags["asks"] != asks || saved.FeatureFlags["customer-requests"] != customers {
				t.Fatalf("independent flags changed: %+v", saved.FeatureFlags)
			}
			read := authRequest[domain.Bootstrap](t, member, "GET", host.URL+"/api/issue-records/bootstrap", nil, workspace, http.StatusOK)
			if read.WorkspaceSettings.FeatureFlags["asks"] != asks || read.WorkspaceSettings.FeatureFlags["customer-requests"] != customers {
				t.Fatal("member bootstrap lost saved flags")
			}
			status := http.StatusForbidden
			if asks {
				status = http.StatusCreated
			}
			authRequest[any](t, member, "POST", host.URL+"/api/asks", map[string]string{"title": "Asks toggle verification"}, workspace, status)
		}
	}
	if err := repo.UpdateMemberRole(t.Context(), data.Workspace.ID, memberUser.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	authRequest[domain.WorkspaceSettings](t, member, "PATCH", preferences, patch(false), workspace, http.StatusOK)
	reopened, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, ok := reopened.WorkspaceSettingsMetadata(workspace)
	if !ok || restored.WorkspaceSettings.FeatureFlags["asks"] || restored.WorkspaceSettings.FeatureFlags["customer-requests"] {
		t.Fatal("disabled flags did not survive loading a new store")
	}
}

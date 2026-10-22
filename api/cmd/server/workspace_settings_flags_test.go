package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// PUT /api/workspace/settings stores the free-form settings blob; it never fed
// WorkspaceSettings.FeatureFlags, so a flag sent there was silently accepted and
// ignored. Feature flags are saved with PATCH /api/workspace/preferences (what the
// web settings use), so PUT refuses them with a pointer to that route.
func TestWorkspaceSettingsPutRefusesFeatureFlagsAndPreferencesPersistThem(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer host.Close()
	const workspace = "test-workspace"
	owner := authClient(t)
	authRequest[domain.AuthSession](t, owner, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)

	authRequest[map[string]any](t, owner, http.MethodPut, host.URL+"/api/workspace/settings", map[string]any{"featureFlags": map[string]bool{"coding-sessions": true}}, workspace, http.StatusBadRequest)
	data := authRequest[domain.Bootstrap](t, owner, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	if data.WorkspaceSettings.FeatureFlags["coding-sessions"] {
		t.Fatal("the refused PUT still changed the flag")
	}

	authRequest[domain.WorkspaceSettings](t, owner, http.MethodPatch, host.URL+"/api/workspace/preferences", map[string]any{"featureFlags": map[string]bool{"coding-sessions": true}}, workspace, http.StatusOK)
	data = authRequest[domain.Bootstrap](t, owner, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	if !data.WorkspaceSettings.FeatureFlags["coding-sessions"] {
		t.Fatal("PATCH /api/workspace/preferences did not persist the coding-sessions flag")
	}
	// A PUT without flags still works and leaves the flag alone.
	authRequest[map[string]any](t, owner, http.MethodPut, host.URL+"/api/workspace/settings", map[string]any{"values": map[string]any{"firstDay": "Sunday"}}, workspace, http.StatusOK)
	data = authRequest[domain.Bootstrap](t, owner, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	if !data.WorkspaceSettings.FeatureFlags["coding-sessions"] {
		t.Fatal("a settings PUT cleared the coding-sessions flag")
	}
}

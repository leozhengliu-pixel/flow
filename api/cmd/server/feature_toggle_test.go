package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestCustomerFeatureCanBeDisabledAndReenabled(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "features.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer host.Close()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	for _, enabled := range []bool{false, true} {
		settings := authRequest[domain.WorkspaceSettings](t, client, "PATCH", host.URL+"/api/workspace/preferences", map[string]any{"featureFlags": map[string]bool{"customer-requests": enabled}}, "test-workspace", http.StatusOK)
		if settings.FeatureFlags["customer-requests"] != enabled || !settings.FeatureFlags["initiatives"] {
			t.Fatal("feature patch was lost or changed unrelated flags")
		}
		stored := authRequest[domain.WorkspaceSettings](t, client, "GET", host.URL+"/api/workspace/preferences", nil, "test-workspace", http.StatusOK)
		if stored.FeatureFlags["customer-requests"] != enabled {
			t.Fatal("saved feature state was not returned")
		}
		status := http.StatusForbidden
		if enabled {
			status = http.StatusCreated
		}
		authRequest[any](t, client, "POST", host.URL+"/api/customers", map[string]string{"name": "Feature toggle test customer"}, "test-workspace", status)
	}
}

func TestPulseToggleNeverLoadsIssueBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulse.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(service))
	defer host.Close()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE workspace_memberships SET role='member' WHERE user_id='usr_member'`); err != nil {
		t.Fatal(err)
	}
	member := authClient(t)
	authRequest[domain.AuthSession](t, member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", 200)
	authRequest[any](t, member, "PATCH", host.URL+"/api/workspace/preferences", map[string]any{"featureFlags": map[string]bool{"pulse": false}}, "", 403)
	for _, statement := range []string{`UPDATE issue_records SET data=?`, `UPDATE workspace_content_records SET data=? WHERE kind IN ('comment','activity')`} {
		if _, err := db.Exec(statement, []byte("invalid-unrelated-json")); err != nil {
			t.Fatal(err)
		}
	}
	for _, enabled := range []bool{false, true} {
		value := authRequest[domain.WorkspaceSettings](t, client, "PATCH", host.URL+"/api/workspace/preferences", map[string]any{"featureFlags": map[string]bool{"pulse": enabled}}, "test-workspace", 200)
		if value.FeatureFlags["pulse"] != enabled {
			t.Fatal("pulse value was not saved")
		}
		value = authRequest[domain.WorkspaceSettings](t, client, "GET", host.URL+"/api/workspace/preferences", nil, "test-workspace", 200)
		if value.FeatureFlags["pulse"] != enabled {
			t.Fatal("pulse value was not persisted")
		}
	}
	r := httptest.NewRequest("GET", "/api/realtime/events?workspace=test-workspace", nil)
	r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, domain.User{ID: "usr_admin"}))
	event, visible, err := service.pagedRealtimeEvent(r, domain.RealtimeEvent{Type: "workspace_preferences.updated"})
	if err != nil || !visible || string(event.Payload) != `{"settingsChanged":true}` {
		t.Fatalf("settings SSE should be a lightweight invalidation: %s %v", event.Payload, err)
	}
}

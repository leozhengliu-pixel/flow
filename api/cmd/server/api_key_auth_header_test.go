package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A bearer token is an explicit credential choice. An insufficient token must
// not fall through to an ambient browser cookie and gain broader access.
func TestAuthorizationHeaderDoesNotFallBackToCookie(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "api-key-header.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	key := authRequest[struct {
		Secret string `json:"secret"`
	}](t, admin, http.MethodPost, server.URL+"/api/api-keys", map[string]any{"name": "Read-only", "scopes": []string{"read"}, "teamIds": []string{bootstrap.Teams[0].ID}}, "test-workspace", http.StatusCreated)
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/issues", bytes.NewBufferString(`{"title":"must be denied","teamId":"`+bootstrap.Teams[0].ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key.Secret)
	req.Header.Set("X-Workspace-Key", "test-workspace")
	req.Header.Set("Content-Type", "application/json")
	// Reuse the authenticated browser cookie while presenting the restricted
	// bearer token. The bearer scope must win and reject this request.
	response, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("restricted bearer with cookie returned %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	accountRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/account/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	accountRequest.Header.Set("Authorization", "Bearer "+key.Secret)
	accountRequest.Header.Set("X-Workspace-Key", "test-workspace")
	accountResponse, err := admin.Do(accountRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer accountResponse.Body.Close()
	if accountResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("bearer account security request returned %d, want %d", accountResponse.StatusCode, http.StatusForbidden)
	}
}

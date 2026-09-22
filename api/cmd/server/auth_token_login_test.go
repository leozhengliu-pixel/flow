package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAuthTokenLoginAndForceReauth(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "token-login.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer host.Close()

	magic := authRequest[map[string]any](t, authClient(t), http.MethodPost, host.URL+"/api/auth/magic-link", map[string]string{"email": "admin@example.test"}, "", http.StatusOK)
	token, _ := magic["loginToken"].(string)
	if token == "" {
		t.Fatal("expected loginToken in development mode")
	}
	fresh := authClient(t)
	session := authRequest[domain.AuthSession](t, fresh, http.MethodPost, host.URL+"/api/auth/token-login", map[string]any{
		"email": "admin@example.test", "authToken": token, "service": "email",
	}, "", http.StatusOK)
	if session.User.Email != "admin@example.test" {
		t.Fatalf("unexpected session user %#v", session.User)
	}
	authRequest[domain.AuthSession](t, fresh, http.MethodGet, host.URL+"/api/auth/session", nil, "", http.StatusOK)

	magic = authRequest[map[string]any](t, authClient(t), http.MethodPost, host.URL+"/api/auth/magic-link", map[string]string{"email": "admin@example.test"}, "", http.StatusOK)
	token, _ = magic["loginToken"].(string)
	if token == "" {
		t.Fatal("expected second loginToken")
	}
	reauth := authClient(t)
	authRequest[domain.AuthSession](t, reauth, http.MethodPost, host.URL+"/api/auth/token-login", map[string]any{
		"email": "admin@example.test", "authToken": token, "forceReauth": true,
	}, "", http.StatusOK)
	authRequest[any](t, fresh, http.MethodGet, host.URL+"/api/auth/session", nil, "", http.StatusUnauthorized)
}

func TestAuthTokenLoginRejectsInvalid(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "token-login-invalid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer host.Close()
	authRequest[any](t, authClient(t), http.MethodPost, host.URL+"/api/auth/token-login", map[string]any{
		"email": "admin@example.test", "authToken": "not-a-token",
	}, "", http.StatusUnauthorized)
}

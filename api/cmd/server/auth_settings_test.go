package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAuthenticationPolicyEnforcesCurrentSessionAndMFA(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer api.Close()
	client := authClient(t)
	session := authRequest[domain.AuthSession](t, client, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	parsed, _ := url.Parse(api.URL)
	token := ""
	for _, cookie := range client.Jar.Cookies(parsed) {
		if cookie.Name == sessionCookieName {
			token = cookie.Value
		}
	}
	if token == "" {
		t.Fatal("missing test session")
	}
	authRequest[domain.WorkspaceSettings](t, client, http.MethodPatch, api.URL+"/api/workspace/preferences", map[string]any{"emailAuthEnabled": false, "disableAdminBypass": true}, "test-workspace", http.StatusOK)
	authRequest[map[string]string](t, client, http.MethodGet, api.URL+"/api/bootstrap", nil, "test-workspace", http.StatusForbidden)
	if err := repository.SetSessionAuthentication(t.Context(), token, "google", "https://accounts.google.com", false); err != nil {
		t.Fatal(err)
	}
	authRequest[domain.Bootstrap](t, client, http.MethodGet, api.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	authRequest[domain.WorkspaceSettings](t, client, http.MethodPatch, api.URL+"/api/workspace/preferences", map[string]any{"requireTwoFactor": true}, "test-workspace", http.StatusOK)
	blocked := authRequest[map[string]string](t, client, http.MethodGet, api.URL+"/api/bootstrap", nil, "test-workspace", http.StatusForbidden)
	if blocked["code"] != "mfa_required" {
		t.Fatalf("wrong policy response: %#v", blocked)
	}
	authRequest[map[string]string](t, client, http.MethodPost, api.URL+"/api/account/mfa/start", nil, "test-workspace", http.StatusConflict)
	if err := repository.SetSessionAuthentication(t.Context(), token, "google", "https://accounts.google.com", true); err != nil {
		t.Fatal(err)
	}
	authRequest[domain.Bootstrap](t, client, http.MethodGet, api.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	other := authClient(t)
	authRequest[domain.AuthSession](t, other, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": session.User.Email, "password": "test-password"}, "", http.StatusOK)
	authRequest[map[string]string](t, other, http.MethodGet, api.URL+"/api/bootstrap", nil, "test-workspace", http.StatusForbidden)
}

func TestEnforcedIdentityProviderPolicy(t *testing.T) {
	data := domain.Bootstrap{WorkspaceSettings: domain.WorkspaceSettings{EmailAuthEnabled: true, GoogleAuthEnabled: true, DisableAdminBypass: true}, IdentityProviders: []domain.IdentityProvider{{ID: "enterprise-a", Issuer: "https://sso.example.test", Enabled: true, Enforced: true}}}
	if allowedAuthenticationMethod(data, "member", store.SessionAuthentication{Provider: "password"}) {
		t.Fatal("password bypassed enforced SSO")
	}
	if allowedAuthenticationMethod(data, "admin", store.SessionAuthentication{Provider: "google"}) {
		t.Fatal("admin bypassed enforced SSO while bypass disabled")
	}
	if !allowedAuthenticationMethod(data, "member", store.SessionAuthentication{Provider: "enterprise:enterprise-a", Issuer: "https://sso.example.test"}) {
		t.Fatal("required provider rejected")
	}
	if allowedAuthenticationMethod(data, "member", store.SessionAuthentication{Provider: "oidc", Issuer: "https://unrelated.example.test"}) {
		t.Fatal("unrelated issuer accepted")
	}
	if verifiedMFAClaim(map[string]any{"amr": []any{"pwd"}}) || !verifiedMFAClaim(map[string]any{"amr": []any{"pwd", "mfa"}}) {
		t.Fatal("incorrect amr handling")
	}
}

func TestLegacySessionCannotMasqueradeAsAllowedPasswordLogin(t *testing.T) {
	data := domain.Bootstrap{WorkspaceSettings: domain.WorkspaceSettings{EmailAuthEnabled: true, GoogleAuthEnabled: false, DisableAdminBypass: true}}
	if allowedAuthenticationMethod(data, "member", store.SessionAuthentication{Provider: "legacy"}) {
		t.Fatal("unknown legacy login bypassed authentication restriction")
	}
	if !allowedAuthenticationMethod(data, "member", store.SessionAuthentication{Provider: "password"}) {
		t.Fatal("verified password method rejected")
	}
}

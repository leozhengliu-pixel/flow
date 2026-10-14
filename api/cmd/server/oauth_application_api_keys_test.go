package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// credentialKeyRequest sends a request authenticated only by an API key (no cookies)
// and returns the status, the Cache-Control header and the decoded body.
func credentialKeyRequest(t *testing.T, host *httptest.Server, token, workspace, method, path string, body any) (int, string, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	request, _ := http.NewRequest(method, host.URL+path, reader)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Workspace-Key", workspace)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	result := map[string]any{}
	_ = json.Unmarshal(raw, &result)
	return response.StatusCode, response.Header.Get("Cache-Control"), result
}

func createAPIKey(t *testing.T, host *httptest.Server, client *http.Client, workspace string, input map[string]any) string {
	t.Helper()
	return authRequest[struct {
		Secret string `json:"secret"`
	}](t, client, http.MethodPost, host.URL+"/api/api-keys", input, workspace, http.StatusCreated).Secret
}

// TestOAuthApplicationCredentialsWithAPIKeys: admins can manage OAuth
// application credentials with an admin API key; member, read-only,
// team-restricted keys are refused; every API-key use is audited without the
// secret; signed-in admins keep using the routes.
func TestOAuthApplicationCredentialsWithAPIKeys(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	if err := s.store.UpdateMemberRole(t.Context(), data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	webhookSecret := "flow_wh_" + strings.Repeat("Qa7_", 8)
	manifest := map[string]any{"schemaVersion": "1.0.0", "developer": map[string]any{"name": "Acme"}, "oauth": map[string]any{"client_name": "Keyed app", "redirect_uris": []string{oauthTestRedirect}, "scopes": []string{"read", "write"}}, "webhook": map[string]any{"url": "https://hooks.example.com/flow", "resourceTypes": []string{"Issue"}, "secret": webhookSecret}}
	created := authRequest[map[string]any](t, admin, "POST", host.URL+"/api/oauth-applications/manifest", manifest, workspace, 201)
	id, clientSecret := created["id"].(string), created["clientSecret"].(string)
	if id == "" || clientSecret == "" {
		t.Fatalf("manifest application = %v", created)
	}

	adminKey := createAPIKey(t, host, admin, workspace, map[string]any{"name": "Admin automation", "scopes": []string{"admin"}})
	readKey := createAPIKey(t, host, admin, workspace, map[string]any{"name": "Reader", "scopes": []string{"read"}})
	teamKey := createAPIKey(t, host, admin, workspace, map[string]any{"name": "Team admin", "scopes": []string{"admin"}, "teamRestriction": "selected", "teamIds": []string{data.Teams[0].ID}})
	member := authClient(t)
	authRequest[map[string]any](t, member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", 200)
	memberKey := createAPIKey(t, host, member, workspace, map[string]any{"name": "Member full access"})

	credentialRoutes := []struct{ method, path string }{
		{"GET", "/api/oauth-applications/" + id + "/client-secret"},
		{"POST", "/api/oauth-applications/" + id + "/rotate-secret"},
		{"GET", "/api/oauth-applications/" + id + "/developer-token"},
		{"POST", "/api/oauth-applications/" + id + "/developer-token"},
		{"GET", "/api/oauth-applications/" + id + "/webhook/secret"},
		{"POST", "/api/oauth-applications/" + id + "/webhook/rotate-secret"},
	}
	for _, key := range []struct{ name, token string }{{"read-only", readKey}, {"team-restricted", teamKey}, {"member", memberKey}} {
		for _, route := range credentialRoutes {
			if status, _, body := credentialKeyRequest(t, host, key.token, workspace, route.method, route.path, map[string]any{}); status != http.StatusForbidden {
				t.Errorf("%s key %s %s = %d %v, want 403", key.name, route.method, route.path, status, body)
			}
		}
	}

	// The admin key manages the application and its credentials.
	if status, _, _ := credentialKeyRequest(t, host, adminKey, workspace, "GET", "/api/oauth-applications/"+id, nil); status != 200 {
		t.Fatalf("get application with admin key = %d", status)
	}
	status, cache, body := credentialKeyRequest(t, host, adminKey, workspace, "GET", "/api/oauth-applications/"+id+"/client-secret", nil)
	if status != 200 || cache != "no-store" || body["clientSecret"] != clientSecret {
		t.Fatalf("client secret with admin key = %d %q", status, cache)
	}
	status, cache, body = credentialKeyRequest(t, host, adminKey, workspace, "POST", "/api/oauth-applications/"+id+"/rotate-secret", nil)
	rotated, _ := body["clientSecret"].(string)
	if status != 200 || cache != "no-store" || rotated == "" || rotated == clientSecret {
		t.Fatalf("rotate with admin key = %d %q", status, cache)
	}
	status, cache, body = credentialKeyRequest(t, host, adminKey, workspace, "POST", "/api/oauth-applications/"+id+"/developer-token", map[string]any{"actor": "app", "scopes": []string{"read"}})
	developerToken, _ := body["access_token"].(string)
	if status != 200 || cache != "no-store" || developerToken == "" || body["actor"] != "app" {
		t.Fatalf("developer token with admin key = %d %q %v", status, cache, body["actor"])
	}
	if status, _, body = credentialKeyRequest(t, host, adminKey, workspace, "GET", "/api/oauth-applications/"+id+"/developer-token", nil); status != 200 || body["existing"] != true || body["access_token"] != developerToken {
		t.Fatalf("existing developer token with admin key = %d", status)
	}
	if status, cache, body = credentialKeyRequest(t, host, adminKey, workspace, "GET", "/api/oauth-applications/"+id+"/webhook/secret", nil); status != 200 || cache != "no-store" || body["secret"] != webhookSecret {
		t.Fatalf("webhook secret with admin key = %d %q", status, cache)
	}
	status, cache, body = credentialKeyRequest(t, host, adminKey, workspace, "POST", "/api/oauth-applications/"+id+"/webhook/rotate-secret", nil)
	rotatedWebhook, _ := body["secret"].(string)
	if status != 200 || cache != "no-store" || rotatedWebhook == "" || rotatedWebhook == webhookSecret {
		t.Fatalf("webhook rotation with admin key = %d %q", status, cache)
	}

	// Signed-in admins keep working, without API-key audit entries.
	if got := authRequest[map[string]string](t, admin, "GET", host.URL+"/api/oauth-applications/"+id+"/client-secret", nil, workspace, 200); got["clientSecret"] != rotated {
		t.Fatal("session read of the rotated client secret failed")
	}

	entries := authRequest[[]domain.AuditLogEntry](t, admin, "GET", host.URL+"/api/workspace/audit-log?resourceType=oauth_application", nil, workspace, 200)
	actions := []string{}
	for _, entry := range entries {
		if entry.ResourceID != id || entry.Metadata["authentication"] != "apiKey" {
			continue
		}
		actions = append(actions, entry.Action)
		if entry.Actor.ID != data.Viewer.ID || entry.Metadata["apiKeyId"] == "" || entry.Metadata["apiKeyName"] != "Admin automation" || entry.Metadata["clientId"] == "" {
			t.Errorf("audit entry %s = %+v", entry.Action, entry)
		}
	}
	slices.Sort(actions)
	want := []string{"client_secret_read", "client_secret_rotated", "developer_token_created", "developer_token_read", "webhook_secret_read", "webhook_secret_rotated"}
	if !slices.Equal(actions, want) {
		t.Fatalf("audited actions = %v, want %v", actions, want)
	}
	raw, _ := json.Marshal(entries)
	for _, secret := range []string{clientSecret, rotated, developerToken, webhookSecret, rotatedWebhook} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("the audit log contains a credential")
		}
	}
}

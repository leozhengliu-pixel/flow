package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// fakeGitLab is a minimal GitLab REST API: tokens map to scopes, and the
// self-rotation endpoint swaps the presented token for a new one.
type fakeGitLab struct {
	*httptest.Server
	mu      sync.Mutex
	tokens  map[string][]string
	expires map[string]string
	rotated []string
	seen    []string
}

func newFakeGitLab(t *testing.T) *fakeGitLab {
	t.Helper()
	fake := &fakeGitLab{
		tokens: map[string][]string{
			"glpat-fake-api":       {"api"},
			"glpat-fake-read":      {"read_api"},
			"glpat-fake-rotatable": {"read_api", "self_rotate"},
			"glpat-fake-noscope":   {"read_user"},
		},
		expires: map[string]string{},
	}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		token := r.Header.Get("PRIVATE-TOKEN")
		fake.seen = append(fake.seen, r.Method+" "+r.URL.Path)
		scopes, ok := fake.tokens[token]
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/personal_access_tokens/self":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "name": "Flow", "scopes": scopes, "active": true, "revoked": false, "expires_at": fake.expires[token]})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "flow-bot", "bot": false})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v4/personal_access_tokens/self/rotate":
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			next := "glpat-fake-rotated-" + string(rune('a'+len(fake.rotated)))
			delete(fake.tokens, token)
			fake.tokens[next] = scopes
			fake.rotated = append(fake.rotated, token)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 8 + len(fake.rotated), "name": "Flow", "scopes": scopes, "token": next, "expires_at": input["expires_at"]})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fake.Close)
	return fake
}

func gitlabTestServer(t *testing.T) (*server, http.Handler, *store.SQLiteStore) {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	return s, newHandler(s), repository
}

func enableConnectorSecrets(t *testing.T) {
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("g", 32))))
}

func gitlabRawRequest(handler http.Handler, method, path string, input any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(input)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestGitLabTokenConnectValidatesURLTokenAndScopes(t *testing.T) {
	_, handler, _ := gitlabTestServer(t)
	fake := newFakeGitLab(t)
	path := "/api/integrations/gitlab?workspace=test-workspace"

	if rec := gitlabRawRequest(handler, http.MethodPut, path, map[string]any{"config": map[string]string{"apiToken": "glpat-fake-api", "host": fake.URL}}); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("connect without encrypted storage: %d %s", rec.Code, rec.Body.String())
	}
	enableConnectorSecrets(t)
	if rec := gitlabRawRequest(handler, http.MethodPut, path, map[string]any{"config": map[string]string{"apiToken": "  "}}); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Please enter a valid access token.") {
		t.Fatalf("empty token: %d %s", rec.Code, rec.Body.String())
	}
	for _, bad := range []string{"http://gitlab.example.com", "gitlab.example.com", "https://user:pass@gitlab.example.com", "https://gitlab.example.com/?a=b"} {
		if rec := gitlabRawRequest(handler, http.MethodPut, path, map[string]any{"config": map[string]string{"apiToken": "glpat-fake-api", "host": bad}}); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Please enter a valid HTTPS URL.") {
			t.Fatalf("invalid URL %q accepted: %d %s", bad, rec.Code, rec.Body.String())
		}
	}
	rec := gitlabRawRequest(handler, http.MethodPut, path, map[string]any{"config": map[string]string{"apiToken": "glpat-fake-unknown", "host": fake.URL}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "invalid, expired or revoked") || !strings.Contains(rec.Body.String(), "errorRequest") {
		t.Fatalf("rejected token: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "glpat-fake-unknown") {
		t.Fatal("error response echoed the access token")
	}
	rec = gitlabRawRequest(handler, http.MethodPut, path, map[string]any{"config": map[string]string{"apiToken": "glpat-fake-noscope", "host": fake.URL}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "requires the api or read_api scope") {
		t.Fatalf("token without api scope: %d %s", rec.Code, rec.Body.String())
	}
}

func TestGitLabTokenLifecycleStoresEncryptedTokenRotatesAndDisconnects(t *testing.T) {
	enableConnectorSecrets(t)
	s, handler, repository := gitlabTestServer(t)
	fake := newFakeGitLab(t)
	fake.expires["glpat-fake-rotatable"] = time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02")

	connected := requestJSON[gitlabConnectResponse](t, handler, http.MethodPut, "/api/integrations/gitlab?workspace=test-workspace", map[string]any{"name": "GitLab", "config": map[string]string{"apiToken": "glpat-fake-api", "host": fake.URL + "/"}}, http.StatusOK)
	if connected.Status != "connected" || connected.Config["host"] != fake.URL || connected.Config["readonly"] != "false" || connected.Config["canSelfRotate"] != "true" || connected.Config["username"] != "flow-bot" || connected.Config["tokenHint"] != "-api" {
		t.Fatalf("connection facts not recorded: %#v", connected.IntegrationConnection)
	}
	if connected.WebhookSecret == "" || connected.WebhookPath != "/api/integrations/gitlab/webhook?workspace=test-workspace" || connected.Config["webhookSecret"] != "" {
		t.Fatalf("webhook setup not returned once: secret=%q path=%q config=%#v", connected.WebhookSecret, connected.WebhookPath, connected.Config)
	}
	workspace := repository.Bootstrap().Workspace.URLKey
	credential, err := s.gitlabCredential(t.Context(), workspace, connected.ID)
	if err != nil || credential.Token != "glpat-fake-api" || credential.URL != fake.URL {
		t.Fatalf("stored credential: %#v %v", credential, err)
	}
	state, _ := json.Marshal(repository.Bootstrap())
	if bytes.Contains(state, []byte("glpat-fake-api")) {
		t.Fatal("access token leaked into workspace state")
	}
	bootstrap := gitlabRawRequest(handler, http.MethodGet, "/api/bootstrap?workspace=test-workspace", nil)
	if strings.Contains(bootstrap.Body.String(), "glpat-fake-api") || strings.Contains(bootstrap.Body.String(), connected.WebhookSecret) {
		t.Fatal("bootstrap exposes GitLab credentials")
	}

	// The stored credential powers the connection test and webhook secret.
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/integrations/gitlab/"+connected.ID+"/test?workspace=test-workspace", map[string]any{}, http.StatusOK)
	webhook := httptest.NewRequest(http.MethodPost, connected.WebhookPath, strings.NewReader(`{"object_kind":"push"}`))
	webhook.Header.Set("X-Gitlab-Token", connected.WebhookSecret)
	webhookRec := httptest.NewRecorder()
	handler.ServeHTTP(webhookRec, webhook)
	if webhookRec.Code != http.StatusAccepted {
		t.Fatalf("webhook with issued secret: %d %s", webhookRec.Code, webhookRec.Body.String())
	}

	// Replacing the token with a read-only one disables linkbacks and rotation.
	readonly := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPost, "/api/integrations/gitlab/"+connected.ID+"/token?workspace=test-workspace", map[string]string{"token": "glpat-fake-read"}, http.StatusOK)
	if readonly.Config["readonly"] != "true" || readonly.Config["canSelfRotate"] != "false" || readonly.LinkbackEnabled {
		t.Fatalf("read-only token facts: %#v", readonly)
	}
	requestJSON[any](t, handler, http.MethodPut, "/api/integrations/gitlab/"+connected.ID+"/token/rotation?workspace=test-workspace", map[string]bool{"enabled": true}, http.StatusUnprocessableEntity)
	requestJSON[any](t, handler, http.MethodPost, "/api/integrations/gitlab/"+connected.ID+"/token/rotate?workspace=test-workspace", nil, http.StatusUnprocessableEntity)

	// read_api + self_rotate can renew itself.
	rotatable := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPost, "/api/integrations/gitlab/"+connected.ID+"/token?workspace=test-workspace", map[string]string{"token": "glpat-fake-rotatable"}, http.StatusOK)
	if rotatable.Config["canSelfRotate"] != "true" || rotatable.Config["expiresAt"] == "" {
		t.Fatalf("rotatable token facts: %#v", rotatable.Config)
	}
	enabled := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/gitlab/"+connected.ID+"/token/rotation?workspace=test-workspace", map[string]bool{"enabled": true}, http.StatusOK)
	if enabled.Config["rotationEnabled"] != "true" || enabled.Config["nextRotationAt"] == "" {
		t.Fatalf("rotation schedule: %#v", enabled.Config)
	}
	rotated := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPost, "/api/integrations/gitlab/"+connected.ID+"/token/rotate?workspace=test-workspace", nil, http.StatusOK)
	if rotated.Config["lastRotatedAt"] == "" || rotated.Config["tokenHint"] != "ed-a" {
		t.Fatalf("manual rotation: %#v", rotated.Config)
	}
	if credential, _ = s.gitlabCredential(t.Context(), workspace, connected.ID); credential.Token != "glpat-fake-rotated-a" {
		t.Fatalf("rotated token not stored: %q", credential.Token)
	}

	// The minute sweep renews tokens whose rotation time has passed.
	if err := repository.MutateWorkspace(t.Context(), "test-workspace", "test.rotation_due", connected.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.IntegrationConnections {
			if data.IntegrationConnections[index].ID == connected.ID {
				data.IntegrationConnections[index].Config["nextRotationAt"] = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.rotateDueGitLabTokens(t.Context(), "test-workspace", time.Now().UTC())
	if credential, _ = s.gitlabCredential(t.Context(), workspace, connected.ID); credential.Token != "glpat-fake-rotated-b" {
		t.Fatalf("scheduled rotation did not renew the token: %q", credential.Token)
	}

	// A failed rotation is recorded and not retried automatically.
	fake.mu.Lock()
	delete(fake.tokens, "glpat-fake-rotated-b")
	fake.mu.Unlock()
	failed := gitlabRawRequest(handler, http.MethodPost, "/api/integrations/gitlab/"+connected.ID+"/token/rotate?workspace=test-workspace", nil)
	if failed.Code != http.StatusUnprocessableEntity || !strings.Contains(failed.Body.String(), "invalid, expired or revoked") {
		t.Fatalf("failed rotation: %d %s", failed.Code, failed.Body.String())
	}
	for _, connection := range repository.Bootstrap().IntegrationConnections {
		if connection.ID == connected.ID && !strings.HasPrefix(connection.Config["rotationFailureReason"], "Last rotation failed") {
			t.Fatalf("rotation failure not recorded: %#v", connection.Config)
		}
	}
	test := gitlabRawRequest(handler, http.MethodPost, "/api/integrations/gitlab/"+connected.ID+"/test?workspace=test-workspace", map[string]any{})
	if test.Code != http.StatusBadGateway || !strings.Contains(test.Body.String(), "errorRequest") {
		t.Fatalf("connectivity test with revoked token: %d %s", test.Code, test.Body.String())
	}

	requestJSON[any](t, handler, http.MethodDelete, "/api/integrations/gitlab/"+connected.ID+"?workspace=test-workspace", nil, http.StatusNoContent)
	if _, err := s.gitlabCredential(t.Context(), workspace, connected.ID); err == nil {
		t.Fatal("disconnect left the encrypted token behind")
	}
}

func TestNormalizeGitLabURL(t *testing.T) {
	cases := map[string]string{
		"":                                     gitlabDefaultURL,
		"https://GitLab.Example.com/":          "https://gitlab.example.com",
		"https://example.com/gitlab/api/v4/":   "https://example.com/gitlab",
		"https://gitlab.your-company.com:8443": "https://gitlab.your-company.com:8443",
	}
	for input, want := range cases {
		if got, err := normalizeGitLabURL(input, false); err != nil || got != want {
			t.Fatalf("normalizeGitLabURL(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := normalizeGitLabURL("http://127.0.0.1:9000", false); err == nil {
		t.Fatal("plain HTTP accepted outside local development")
	}
	if got, err := normalizeGitLabURL("http://127.0.0.1:9000", true); err != nil || got != "http://127.0.0.1:9000" {
		t.Fatalf("local development URL rejected: %q %v", got, err)
	}
	if err := (&server{}).gitlabRequest(t.Context(), "https://10.0.0.1", "glpat-fake", http.MethodGet, "/user", nil, nil); err == nil || !strings.Contains(err.Error(), "public HTTPS") {
		t.Fatalf("private GitLab address was not blocked: %v", err)
	}
}

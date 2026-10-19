package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/codehost/gitlab"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
)

func gitlabCodingConfig(enabled bool) appconfig.CodingConfig {
	return appconfig.CodingConfig{Enabled: enabled, GCInterval: time.Minute, GitLab: appconfig.CodingGitLabConfig{CredentialMode: gitlab.ModeProjectToken, TokenScopes: []string{"read_repository", "write_repository", "read_api"}}}
}

func TestGitLabCodingHostUsesExistingTokenConnection(t *testing.T) {
	enableConnectorSecrets(t)
	s, handler, _ := gitlabTestServer(t)
	s.initCoding(gitlabCodingConfig(false))
	api := newFakeGitLab(t)
	connected := requestJSON[gitlabConnectResponse](t, handler, http.MethodPut, "/api/integrations/gitlab?workspace=test-workspace", map[string]any{
		"name": "self-hosted", "config": map[string]string{"apiToken": "glpat-fake-api", "host": api.URL},
	}, http.StatusOK)

	host := s.gitlabCodingHost()
	if host == nil {
		t.Fatal("GitLab code host is not registered")
	}
	ctx := context.Background()
	if ok, err := host.Connected(ctx, "test-workspace"); !ok || err != nil {
		t.Fatalf("workspace with a GitLab connection must be connected: %t %v", ok, err)
	}
	source := gitlabCodingSource{s: s}
	connections, err := source.GitLabConnections(ctx, "test-workspace")
	if err != nil || len(connections) != 1 {
		t.Fatalf("connections: %v %#v", err, connections)
	}
	connection := connections[0]
	if connection.ID != connected.ID || connection.BaseURL != api.URL || connection.Kind != gitlab.ConnectionToken || connection.Username != "flow-bot" || connection.Readonly || connection.HostName == "" {
		t.Fatalf("connection: %#v", connection)
	}
	token, err := source.GitLabAPIToken(ctx, "test-workspace", connection.ID)
	if err != nil || token != "glpat-fake-api" {
		t.Fatalf("API token from encrypted connector storage: %q %v", token, err)
	}
	// The integration's own client reaches the (loopback) fake in development mode.
	if client, err := source.GitLabHTTPClient(ctx, api.URL); err != nil || client == nil {
		t.Fatalf("http client: %v", err)
	}
	hosts, err := host.EgressHosts(ctx, "test-workspace")
	if err != nil || len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("egress hosts: %v %v", hosts, err)
	}
	if ok, _ := host.Connected(ctx, "other-workspace"); ok {
		t.Fatal("a workspace without GitLab must not be connected")
	}
}

func TestGitLabCodingWebhookParsingKeepsReviewSync(t *testing.T) {
	enableConnectorSecrets(t)
	s, handler, repository := gitlabTestServer(t)
	s.initCoding(gitlabCodingConfig(true))
	api := newFakeGitLab(t)
	connected := requestJSON[gitlabConnectResponse](t, handler, http.MethodPut, "/api/integrations/gitlab?workspace=test-workspace", map[string]any{
		"config": map[string]string{"apiToken": "glpat-fake-api", "host": api.URL},
	}, http.StatusOK)
	note := []byte(`{"object_kind":"note","user":{"username":"alice"},"project":{"id":7,"path_with_namespace":"acme/api","web_url":"https://gitlab.example.com/acme/api"},"object_attributes":{"id":9001,"note":"@flow add tests","noteable_type":"MergeRequest"},"merge_request":{"id":44001,"iid":27,"title":"Fix","url":"https://gitlab.example.com/acme/api/-/merge_requests/27","state":"opened","source_branch":"flow/fix","target_branch":"main","last_commit":{"id":"abc"}}}`)
	request := func(event, token string, body []byte) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/gitlab/webhook?workspace=test-workspace", bytes.NewReader(body))
		req.Header.Set("X-Gitlab-Event", event)
		req.Header.Set("X-Gitlab-Token", token)
		req.Header.Set("X-Gitlab-Event-UUID", "note-delivery-1")
		return req
	}

	data, _ := repository.WorkspaceMetadata("test-workspace")
	connection := &data.IntegrationConnections[0]
	event, ok, err := s.parseGitLabCodingWebhook(request(gitlab.HookNote, connected.WebhookSecret, note), connection, note)
	if err != nil || !ok {
		t.Fatalf("parse: %v ok=%t", err, ok)
	}
	if event.Type != codehost.EventComment || event.InstallationID != connected.ID || event.DeliveryID != "note-delivery-1" || event.ChangeRequest == nil || event.ChangeRequest.Number != 27 || event.CommentBody != "@flow add tests" {
		t.Fatalf("normalized event: %#v", event)
	}
	// Flow's own notes (the connection user) are marked as bot.
	own := bytes.Replace(note, []byte(`"username":"alice"`), []byte(`"username":"flow-bot"`), 1)
	if event, _, _ := s.parseGitLabCodingWebhook(request(gitlab.HookNote, connected.WebhookSecret, own), connection, own); !event.AuthorIsBot {
		t.Fatal("connection user's note must be AuthorIsBot")
	}
	// Hooks that cannot affect sessions are skipped before parsing.
	if _, ok, err := s.parseGitLabCodingWebhook(request("Push Hook", connected.WebhookSecret, note), connection, note); ok || err != nil {
		t.Fatalf("push hook: ok=%t err=%v", ok, err)
	}

	// The HTTP path is unchanged: a note is ignored by the review sync, a
	// merge request still creates a review, and a bad token is still 401.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request(gitlab.HookNote, connected.WebhookSecret, note))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "ignored") {
		t.Fatalf("note webhook: %d %s", rec.Code, rec.Body.String())
	}
	mr := []byte(`{"object_kind":"merge_request","user":{"username":"alice"},"project":{"id":7,"path_with_namespace":"acme/api","web_url":"https://gitlab.example.com/acme/api"},"object_attributes":{"id":44001,"iid":27,"title":"Draft: Fix","url":"https://gitlab.example.com/acme/api/-/merge_requests/27","state":"opened","action":"open","source_branch":"flow/fix","target_branch":"main","last_commit":{"id":"abc"}}}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(gitlab.HookMergeRequest, connected.WebhookSecret, mr))
	if rec.Code != http.StatusAccepted || len(repository.Bootstrap().Reviews) == 0 {
		t.Fatalf("merge request webhook: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(gitlab.HookMergeRequest, "wrong", mr))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", rec.Code)
	}
}

func TestGitLabCodingOAuthTokenRefresh(t *testing.T) {
	s, _, repository := gitlabTestServer(t)
	s.initCoding(gitlabCodingConfig(false))
	var refreshes atomic.Int32
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-1" || r.Form.Get("client_id") != "client" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		refreshes.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-2","refresh_token":"refresh-2","expires_in":7200}`))
	}))
	t.Cleanup(tokenServer.Close)
	t.Setenv("FLOW_INTEGRATION_GITLAB_TOKEN_URL", tokenServer.URL+"/oauth/token")
	t.Setenv("FLOW_INTEGRATION_GITLAB_CLIENT_ID", "client")
	t.Setenv("FLOW_INTEGRATION_GITLAB_HOST", "https://gitlab.example.com")
	expired := time.Now().UTC().Add(time.Minute)
	err := repository.MutateWorkspace(context.Background(), "test-workspace", "integration.connected", "gl", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: "integration_gitlab_oauth", Provider: "gitlab", Status: "connected", Config: map[string]string{"mode": "oauth"}, OAuthAccessToken: "access-1", OAuthRefreshToken: "refresh-1", OAuthExpiresAt: &expired, Scopes: []string{"api"}, Channels: []string{}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	source := gitlabCodingSource{s: s}
	ctx := context.Background()
	connections, _ := source.GitLabConnections(ctx, "test-workspace")
	if len(connections) != 1 || connections[0].Kind != gitlab.ConnectionOAuth || connections[0].BaseURL != "https://gitlab.example.com" || connections[0].HostName != "gitlab.example.com" {
		t.Fatalf("oauth connection: %#v", connections)
	}
	token, expiresAt, err := source.GitLabOAuthToken(ctx, "test-workspace", "integration_gitlab_oauth")
	if err != nil || token != "access-2" || time.Until(expiresAt) < time.Hour {
		t.Fatalf("refreshed token: %q %s %v", token, expiresAt, err)
	}
	// The refreshed token is stored and reused; no second refresh.
	token, _, err = source.GitLabOAuthToken(ctx, "test-workspace", "integration_gitlab_oauth")
	if err != nil || token != "access-2" || refreshes.Load() != 1 {
		t.Fatalf("second call: %q %v refreshes=%d", token, err, refreshes.Load())
	}
	if api, err := source.GitLabAPIToken(ctx, "test-workspace", "integration_gitlab_oauth"); err != nil || api != "access-2" {
		t.Fatalf("API token of an OAuth connection: %q %v", api, err)
	}
	stored := repository.Bootstrap().IntegrationConnections
	for _, connection := range stored {
		if connection.ID == "integration_gitlab_oauth" && (connection.OAuthRefreshToken != "refresh-2" || connection.OAuthAccessToken != "access-2") {
			t.Fatalf("refresh not persisted: %#v", connection)
		}
	}
}

func TestGitLabSessionTokenKeepRules(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	idle := 15 * time.Minute
	cases := []struct {
		name    string
		session domain.CodingSession
		want    bool
	}{
		{"running", domain.CodingSession{Phase: domain.CodingPhaseRunning, UpdatedAt: now.Add(-3 * time.Hour)}, true},
		{"publishing", domain.CodingSession{Phase: domain.CodingPhasePublishing}, true},
		{"fresh idle", domain.CodingSession{Phase: domain.CodingPhaseIdle, UpdatedAt: now.Add(-10 * time.Minute)}, true},
		{"idle past TTL", domain.CodingSession{Phase: domain.CodingPhaseIdle, UpdatedAt: now.Add(-time.Hour)}, false},
		{"done", domain.CodingSession{Phase: domain.CodingPhaseDone, UpdatedAt: now}, false},
	}
	for _, c := range cases {
		if got := gitlabSessionHoldsToken(c.session, now, idle); got != c.want {
			t.Errorf("%s: got %t want %t", c.name, got, c.want)
		}
	}
	s, _, _ := gitlabTestServer(t)
	s.initCoding(gitlabCodingConfig(false))
	keep := s.gitlabTokenKeep(context.Background(), "test-workspace", now)
	if !keep(gitlab.SandboxTokenPrefix+"snap", now.Add(-time.Hour)) || keep(gitlab.SandboxTokenPrefix+"snap", now.Add(-3*time.Hour)) {
		t.Fatal("sandbox token age rule")
	}
	if _, haveSessions := any(s.codingStore()).(codingSessionReader); !haveSessions {
		// Before track B's store lands, session tokens fall back to their age.
		if !keep(gitlab.SessionTokenPrefix+"s1", now.Add(-time.Hour)) || keep(gitlab.SessionTokenPrefix+"s1", now.Add(-30*time.Hour)) {
			t.Fatal("session token age fallback")
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/codehost/gitlab"
	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// GitLab as a coding-session code host (docs/coding-sessions-design.md §14).
// Owner: track J. This file wires the codehost/gitlab package to the
// existing GitLab integration (token connections in gitlab_integration.go,
// OAuth connections in settings_features.go), hands verified GitLab webhooks
// to the coding follow-up dispatcher, and sweeps leftover project tokens.

// gitlabCodingHost returns the GitLab code host, or nil.
func (s *server) gitlabCodingHost() *gitlab.Host {
	if s.coding == nil {
		return nil
	}
	host, _ := s.coding.hosts[domain.CodeHostGitLab].(*gitlab.Host)
	return host
}

// wireGitLabCodingHost connects the host to the workspace GitLab connections.
func (s *server) wireGitLabCodingHost() {
	host := s.gitlabCodingHost()
	if host == nil {
		return
	}
	host.SetConnectionSource(gitlabCodingSource{s: s})
	if s.coding.config.Enabled && host.CredentialMode() == gitlab.ModeConnection {
		log.Printf("WARNING: coding sessions: FLOW_CODING_GITLAB_CREDENTIAL_MODE=connection hands the long-lived GitLab connection token to sandboxes; prefer project-token")
	}
}

// gitlabCodingSource implements gitlab.ConnectionSource on the existing
// GitLab integration data. Tokens are read on demand and never cached here.
type gitlabCodingSource struct{ s *server }

var _ gitlab.ConnectionSource = gitlabCodingSource{}

func (src gitlabCodingSource) workspaceConnections(workspaceKey string) (string, []domain.IntegrationConnection) {
	data, ok := src.s.store.WorkspaceMetadataFields(workspaceKey, "integrationConnections")
	if !ok {
		return workspaceKey, nil
	}
	workspace := data.Workspace.URLKey
	if workspace == "" {
		workspace = workspaceKey
	}
	connections := []domain.IntegrationConnection{}
	for _, connection := range data.IntegrationConnections {
		if connection.Provider == "gitlab" && slices.Contains([]string{"configured", "connected"}, connection.Status) {
			connections = append(connections, connection)
		}
	}
	return workspace, connections
}

func (src gitlabCodingSource) connection(workspaceKey, id string) (string, domain.IntegrationConnection, bool) {
	workspace, connections := src.workspaceConnections(workspaceKey)
	for _, connection := range connections {
		if connection.ID == id {
			return workspace, connection, true
		}
	}
	return workspace, domain.IntegrationConnection{}, false
}

func gitlabConnectionIsOAuth(connection domain.IntegrationConnection) bool {
	return connection.OAuthAccessToken != "" || connection.Config["mode"] == "oauth"
}

// gitlabConnectionBaseURL is the connection's GitLab URL: the validated host
// of a token connection; for OAuth the configured host, the deployment host,
// or the token endpoint's origin.
func (src gitlabCodingSource) gitlabConnectionBaseURL(connection domain.IntegrationConnection) string {
	candidates := []string{connection.Config["host"], connection.Config["gitlabUrl"]}
	if gitlabConnectionIsOAuth(connection) {
		candidates = append(candidates, os.Getenv("FLOW_INTEGRATION_GITLAB_HOST"))
		if tokenURL, err := url.Parse(integrationOAuthConfigFor("gitlab", connection.Config).TokenURL); err == nil && tokenURL.Host != "" {
			candidates = append(candidates, tokenURL.Scheme+"://"+tokenURL.Host)
		}
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if base, err := normalizeGitLabURL(candidate, true); err == nil {
			return base
		}
	}
	return gitlabDefaultURL
}

func (src gitlabCodingSource) GitLabConnections(_ context.Context, workspaceKey string) ([]gitlab.Connection, error) {
	_, connections := src.workspaceConnections(workspaceKey)
	result := make([]gitlab.Connection, 0, len(connections))
	for _, connection := range connections {
		kind := gitlab.ConnectionToken
		if gitlabConnectionIsOAuth(connection) {
			if connection.OAuthAccessToken == "" {
				continue // OAuth not completed yet
			}
			kind = gitlab.ConnectionOAuth
		}
		base := src.gitlabConnectionBaseURL(connection)
		parsed, _ := url.Parse(base)
		hostName := ""
		if parsed != nil {
			hostName = strings.ToLower(parsed.Host)
		}
		result = append(result, gitlab.Connection{
			ID:           connection.ID,
			WorkspaceKey: workspaceKey,
			BaseURL:      base,
			HostName:     hostName,
			Kind:         kind,
			Username:     connection.Config["username"],
			Scopes:       slices.Clone(connection.Scopes),
			Readonly:     connection.Config["readonly"] == "true",
		})
	}
	return result, nil
}

func (src gitlabCodingSource) GitLabAPIToken(ctx context.Context, workspaceKey, connectionID string) (string, error) {
	workspace, connection, ok := src.connection(workspaceKey, connectionID)
	if !ok {
		return "", errNotFound
	}
	if gitlabConnectionIsOAuth(connection) {
		token, _, err := src.GitLabOAuthToken(ctx, workspaceKey, connectionID)
		return token, err
	}
	credential, err := src.s.gitlabCredential(ctx, workspace, connection.ID)
	if err != nil {
		return "", err
	}
	return credential.Token, nil
}

// gitlabOAuthRefreshMu serializes OAuth refreshes: GitLab rotates the refresh
// token on use, so two concurrent refreshes would invalidate each other.
var gitlabOAuthRefreshMu sync.Mutex

const gitlabOAuthRefreshMargin = 5 * time.Minute

func (src gitlabCodingSource) GitLabOAuthToken(ctx context.Context, workspaceKey, connectionID string) (string, time.Time, error) {
	_, connection, ok := src.connection(workspaceKey, connectionID)
	if !ok || connection.OAuthAccessToken == "" {
		return "", time.Time{}, errNotFound
	}
	expiry := func(connection domain.IntegrationConnection) time.Time {
		if connection.OAuthExpiresAt != nil {
			return connection.OAuthExpiresAt.UTC()
		}
		return time.Time{}
	}
	fresh := func(connection domain.IntegrationConnection) bool {
		return connection.OAuthExpiresAt == nil || time.Until(*connection.OAuthExpiresAt) > gitlabOAuthRefreshMargin
	}
	if fresh(connection) {
		return connection.OAuthAccessToken, expiry(connection), nil
	}
	gitlabOAuthRefreshMu.Lock()
	defer gitlabOAuthRefreshMu.Unlock()
	// Another caller may have refreshed while we waited.
	if _, current, ok := src.connection(workspaceKey, connectionID); ok && fresh(current) {
		return current.OAuthAccessToken, expiry(current), nil
	}
	refreshed, err := src.s.refreshGitLabOAuthConnection(ctx, workspaceKey, connection)
	if err != nil {
		return "", time.Time{}, err
	}
	return refreshed.OAuthAccessToken, expiry(refreshed), nil
}

func (src gitlabCodingSource) GitLabHTTPClient(ctx context.Context, baseURL string) (*http.Client, error) {
	client, err := src.s.gitlabClient(ctx, baseURL)
	if err != nil {
		return nil, err
	}
	// The integration client has a 10s timeout; project listings and token
	// calls on large self-managed instances need more headroom.
	client.Timeout = 30 * time.Second
	return client, nil
}

func (src gitlabCodingSource) GitLabDeploymentHosts() []string {
	if host := strings.TrimSpace(os.Getenv("FLOW_INTEGRATION_GITLAB_HOST")); host != "" {
		return []string{host}
	}
	return nil
}

// refreshGitLabOAuthConnection exchanges the connection's refresh token (the
// same exchange as POST /api/integrations/gitlab/{id}/oauth/refresh) and
// stores the new tokens with a compare-and-swap on the old refresh token.
func (s *server) refreshGitLabOAuthConnection(ctx context.Context, workspaceKey string, snapshot domain.IntegrationConnection) (domain.IntegrationConnection, error) {
	if snapshot.OAuthRefreshToken == "" {
		return domain.IntegrationConnection{}, errors.New("GitLab OAuth refresh token is not available")
	}
	oauthConfig := integrationOAuthConfigFor("gitlab", snapshot.Config)
	if oauthConfig.TokenURL == "" || !integrationEndpointSafe(ctx, oauthConfig.TokenURL, s.authDisabled) {
		return domain.IntegrationConnection{}, errors.New("GitLab OAuth token endpoint is not configured")
	}
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {snapshot.OAuthRefreshToken}, "client_id": {oauthConfig.ClientID}}
	if oauthConfig.ClientSecret != "" {
		values.Set("client_secret", oauthConfig.ClientSecret)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthConfig.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return domain.IntegrationConnection{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := secureOutboundClient(15 * time.Second)
	if s.authDisabled && safeLocalDevelopmentURL(oauthConfig.TokenURL) {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return domain.IntegrationConnection{}, fmt.Errorf("GitLab OAuth refresh failed: %w", err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return domain.IntegrationConnection{}, fmt.Errorf("GitLab OAuth refresh returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.AccessToken == "" {
		return domain.IntegrationConnection{}, errors.New("GitLab OAuth refresh response did not include access_token")
	}
	var result domain.IntegrationConnection
	err = s.store.MutateWorkspace(ctx, workspaceKey, "integration.oauth_refreshed", snapshot.ID, map[string]string{"provider": "gitlab", "source": "coding"}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool {
			return item.ID == snapshot.ID && item.Provider == "gitlab" && item.OAuthRefreshToken == snapshot.OAuthRefreshToken
		})
		if index < 0 {
			return errNotFound
		}
		connection := &data.IntegrationConnections[index]
		connection.OAuthAccessToken = payload.AccessToken
		if payload.RefreshToken != "" {
			connection.OAuthRefreshToken = payload.RefreshToken
		}
		if payload.ExpiresIn > 0 {
			expiry := time.Now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second)
			connection.OAuthExpiresAt = &expiry
		}
		connection.Status, connection.LastError, connection.UpdatedAt = "connected", "", time.Now().UTC()
		result = *connection
		return nil
	})
	return result, err
}

// gitlabCodingHooks are the GitLab hooks that can become session follow-ups.
var gitlabCodingHooks = []string{gitlab.HookMergeRequest, gitlab.HookNote, gitlab.HookPipeline}

// parseGitLabCodingWebhook normalizes a delivery code_webhooks.go already
// verified against the connection's X-Gitlab-Token. ok is false for hooks
// that cannot affect a coding session.
func (s *server) parseGitLabCodingWebhook(r *http.Request, connection *domain.IntegrationConnection, body []byte) (codehost.WebhookEvent, bool, error) {
	host := s.gitlabCodingHost()
	if host == nil || connection == nil || !slices.Contains(gitlabCodingHooks, strings.TrimSpace(r.Header.Get("X-Gitlab-Event"))) {
		return codehost.WebhookEvent{}, false, nil
	}
	secret := connection.Config["webhookSecret"]
	if envName := strings.TrimSpace(connection.Config["webhookSecretEnv"]); strings.HasPrefix(envName, "FLOW_INTEGRATION_") {
		secret = os.Getenv(envName)
	}
	ctx := gitlab.WithWebhookVerification(r.Context(), gitlab.WebhookVerification{
		Secrets:       []string{secret},
		ConnectionID:  connection.ID,
		SelfUsernames: []string{connection.Config["username"]},
	})
	event, err := host.ParseWebhook(ctx, r.Header, body)
	if err != nil {
		return codehost.WebhookEvent{}, false, err
	}
	return event, event.Type != codehost.EventIgnored, nil
}

// dispatchGitLabCodingWebhook is the single call code_webhooks.go makes for
// GitLab after verification. It never fails the webhook: the existing
// CodeReview sync must keep working whatever happens to coding sessions.
func (s *server) dispatchGitLabCodingWebhook(r *http.Request, provider string, connection *domain.IntegrationConnection, body []byte) {
	if provider != "gitlab" || !s.codingEnabled() {
		return
	}
	event, ok, err := s.parseGitLabCodingWebhook(r, connection, body)
	if err != nil {
		log.Printf("coding sessions: GitLab webhook workspace=%s: %v", workspaceKey(r), err)
		return
	}
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
	defer cancel()
	// Track L's dispatcher (coding_webhook_dispatch.go), called directly.
	if err := s.dispatchCodingWebhook(ctx, workspaceKey(r), event); err != nil {
		log.Printf("coding sessions: GitLab webhook dispatch workspace=%s delivery=%s: %v", workspaceKey(r), event.DeliveryID, err)
	}
}

// gitlabSandboxTokenMaxAge bounds flow-sb-* tokens (snapshot builds, debug
// terminals): far longer than their sandboxes may live.
const gitlabSandboxTokenMaxAge = 2 * time.Hour

// gitlabSessionTokenMaxAge bounds tokens whose session cannot be looked up:
// GitLab expires them after one or two days anyway.
const gitlabSessionTokenMaxAge = 26 * time.Hour

// runGitLabCoding sweeps leftover GitLab project access tokens every
// FLOW_CODING_GC_INTERVAL (§5 orphan GC, §22 "project token not revoked").
func (s *server) runGitLabCoding(ctx context.Context) {
	host := s.gitlabCodingHost()
	if host == nil || host.CredentialMode() != gitlab.ModeProjectToken {
		return
	}
	interval := s.coding.config.GCInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run := func() error { s.sweepGitLabProjectTokens(ctx, time.Now().UTC()); return nil }
			if s.coordinator != nil {
				_, _ = s.coordinator.WithLeaderLock(ctx, "coding-gitlab-token-sweep", run)
			} else {
				_ = run()
			}
		}
	}
}

type codingEnvironmentLister interface {
	ListCodingEnvironments(ctx context.Context, workspaceKey string, includeArchived bool) ([]domain.CodingEnvironment, error)
}

// sweepGitLabProjectTokens revokes flow-cs-* tokens of ended sessions and
// stale flow-sb-* tokens in every GitLab project an environment uses.
func (s *server) sweepGitLabProjectTokens(ctx context.Context, now time.Time) {
	host := s.gitlabCodingHost()
	if host == nil {
		return
	}
	for _, key := range s.store.WorkspaceKeys() {
		if ctx.Err() != nil {
			return
		}
		if connected, err := host.Connected(ctx, key); err != nil || !connected {
			continue
		}
		revoked, err := host.SweepProjectTokens(ctx, key, s.gitlabCodingProjects(ctx, key), s.gitlabTokenKeep(ctx, key, now))
		if err != nil {
			log.Printf("coding sessions: GitLab project token sweep workspace=%s revoked=%d: %v", key, revoked, err)
		}
	}
}

func (s *server) gitlabCodingProjects(ctx context.Context, workspaceKey string) []codehost.RepoRef {
	lister, ok := any(s.codingStore()).(codingEnvironmentLister)
	if !ok {
		return nil
	}
	environments, err := lister.ListCodingEnvironments(ctx, workspaceKey, true)
	if err != nil {
		return nil
	}
	repos := []codehost.RepoRef{}
	for _, environment := range environments {
		if environment.CodeHost == domain.CodeHostGitLab && environment.Repository != "" {
			repos = append(repos, codehost.RepoRef{CodeHost: domain.CodeHostGitLab, HostName: environment.HostName, FullName: environment.Repository, ID: environment.RepositoryID})
		}
	}
	return repos
}

// gitlabTokenKeep keeps a session token while its session is live: any
// phase but done, and idle only until the idle TTL reclaimed the sandbox.
func (s *server) gitlabTokenKeep(ctx context.Context, workspaceKey string, now time.Time) gitlab.TokenKeep {
	sessions, haveSessions := any(s.codingStore()).(codingSessionReader)
	idleTTL := s.coding.config.Timeouts.Idle
	if idleTTL <= 0 {
		idleTTL = 15 * time.Minute
	}
	return func(name string, createdAt time.Time) bool {
		age := now.Sub(createdAt)
		if strings.HasPrefix(name, gitlab.SandboxTokenPrefix) {
			return age < gitlabSandboxTokenMaxAge
		}
		sessionID := strings.TrimPrefix(name, gitlab.SessionTokenPrefix)
		if !haveSessions {
			return age < gitlabSessionTokenMaxAge
		}
		session, err := sessions.CodingSession(ctx, workspaceKey, sessionID)
		if errors.Is(err, coding.ErrNotFound) {
			return false
		}
		if err != nil {
			return true // unsure: keep, the next sweep decides
		}
		return gitlabSessionHoldsToken(session, now, idleTTL)
	}
}

func gitlabSessionHoldsToken(session domain.CodingSession, now time.Time, idleTTL time.Duration) bool {
	switch session.Phase {
	case domain.CodingPhaseDone:
		return false
	case domain.CodingPhaseIdle:
		return now.Sub(session.UpdatedAt) < idleTTL+5*time.Minute
	default:
		return true
	}
}

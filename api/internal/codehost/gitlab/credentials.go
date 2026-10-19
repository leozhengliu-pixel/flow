package gitlab

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"flow/api/internal/codehost"
)

// Project access token naming. Session tokens are named flow-cs-<sessionId>;
// tokens for session-less sandboxes (snapshot builds, debug terminals) are
// flow-sb-<sandboxId>. GC (SweepProjectTokens) relies on these prefixes.
const (
	SessionTokenPrefix = "flow-cs-"
	SandboxTokenPrefix = "flow-sb-"
	revokePrefix       = "gitlab-pat"
	// minimum remaining lifetime before a cached token is replaced.
	tokenRefreshMargin = 6 * time.Hour
	// connection-mode credentials are re-fetched hourly by the runner.
	connectionCredentialTTL = time.Hour
	// usernames git sends with the token (GitLab ignores them for tokens).
	tokenUsername = "flow-cs"
	oauthUsername = "oauth2"
)

// mintedToken is a project access token Flow created and still holds.
type mintedToken struct {
	workspaceKey string
	connectionID string
	project      string // :id path segment
	ownerID      string // session or sandbox id
	tokenID      int64
	credential   codehost.Credential
}

// tokenCache keeps minted project tokens in memory (never persisted) so the
// git credential helper, which asks on every fetch and push, reuses one
// token per session and project. After a restart the next mint rotates the
// existing token by name instead of creating a second one.
type tokenCache struct {
	mu     sync.Mutex
	tokens map[string]*mintedToken
	locks  map[string]*sync.Mutex
	// projects remembers every (workspace, connection, project) Flow minted
	// in, for the sweep.
	projects map[string]codehost.RepoRef
}

func newTokenCache() *tokenCache {
	return &tokenCache{tokens: map[string]*mintedToken{}, locks: map[string]*sync.Mutex{}, projects: map[string]codehost.RepoRef{}}
}

func (c *tokenCache) lock(key string) func() {
	c.mu.Lock()
	lock, ok := c.locks[key]
	if !ok {
		lock = &sync.Mutex{}
		c.locks[key] = lock
	}
	c.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (c *tokenCache) get(key string) *mintedToken {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokens[key]
}

func (c *tokenCache) put(key string, token *mintedToken, repo codehost.RepoRef) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens[key] = token
	c.projects[token.workspaceKey+"\x00"+token.connectionID+"\x00"+token.project] = repo
}

func (c *tokenCache) remove(match func(*mintedToken) bool) []*mintedToken {
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := []*mintedToken{}
	for key, token := range c.tokens {
		if match(token) {
			removed = append(removed, token)
			delete(c.tokens, key)
		}
	}
	return removed
}

func (c *tokenCache) knownProjects(workspaceKey string) []codehost.RepoRef {
	c.mu.Lock()
	defer c.mu.Unlock()
	repos := []codehost.RepoRef{}
	for key, repo := range c.projects {
		if strings.HasPrefix(key, workspaceKey+"\x00") {
			repos = append(repos, repo)
		}
	}
	return repos
}

// TokenName is the project access token name for a credential request.
func TokenName(request codehost.CredentialRequest) string {
	if id := strings.TrimSpace(request.SessionID); id != "" {
		return SessionTokenPrefix + id
	}
	return SandboxTokenPrefix + strings.TrimSpace(request.SandboxID)
}

// MintCredential returns a sandbox credential for one repository, according
// to FLOW_CODING_GITLAB_CREDENTIAL_MODE:
//
//   - project-token: a project access token (Developer, configured scopes,
//     expiring after at least one full day) created with the connection
//     token, reused for the session and revoked when the session ends;
//   - oauth: the workspace's OAuth access token (about 2h, refreshed by Flow);
//   - connection: the connection token itself (long-lived; discouraged).
func (h *Host) MintCredential(ctx context.Context, request codehost.CredentialRequest) (codehost.Credential, error) {
	if strings.TrimSpace(request.Repo.FullName) == "" && strings.TrimSpace(request.Repo.ID) == "" {
		return codehost.Credential{}, fmt.Errorf("%w: repository is required", codehost.ErrNoAccess)
	}
	connection, err := h.connection(ctx, request.WorkspaceKey, request.Repo.HostName)
	if err != nil {
		return codehost.Credential{}, err
	}
	switch h.CredentialMode() {
	case ModeOAuth:
		return h.mintOAuth(ctx, connection)
	case ModeConnection:
		return h.mintConnection(ctx, connection)
	default:
		return h.mintProjectToken(ctx, connection, request)
	}
}

func (h *Host) mintOAuth(ctx context.Context, connection Connection) (codehost.Credential, error) {
	if connection.Kind != ConnectionOAuth {
		return codehost.Credential{}, &Error{Kind: ErrOAuthRequired, Message: MessageOAuthRequired}
	}
	source := h.connectionSource()
	token, expiresAt, err := source.GitLabOAuthToken(ctx, connection.WorkspaceKey, connection.ID)
	if err != nil || strings.TrimSpace(token) == "" {
		return codehost.Credential{}, &Error{Kind: codehost.ErrNotConnected, Message: "The GitLab OAuth authorization could not be refreshed. Reconnect GitLab in Settings › Integrations › GitLab."}
	}
	if expiresAt.IsZero() {
		expiresAt = h.now().Add(time.Hour)
	}
	return codehost.Credential{Username: oauthUsername, Password: token, ExpiresAt: expiresAt, CLIEnv: CLIEnv(connection, token)}, nil
}

func (h *Host) mintConnection(ctx context.Context, connection Connection) (codehost.Credential, error) {
	token, err := h.connectionSource().GitLabAPIToken(ctx, connection.WorkspaceKey, connection.ID)
	if err != nil || strings.TrimSpace(token) == "" {
		return codehost.Credential{}, notConnected(MessageTokenRevoked)
	}
	username := tokenUsername
	if connection.Kind == ConnectionOAuth {
		username = oauthUsername
	}
	return codehost.Credential{Username: username, Password: token, ExpiresAt: h.now().Add(connectionCredentialTTL), CLIEnv: CLIEnv(connection, token)}, nil
}

type apiAccessToken struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Token     string    `json:"token"`
	Active    bool      `json:"active"`
	Revoked   bool      `json:"revoked"`
	ExpiresAt string    `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	Scopes    []string  `json:"scopes"`
}

// tokenExpiry picks the expires_at date: GitLab's granularity is one day
// (the token stops working at 00:00 UTC on that date), so Flow asks for the
// date after tomorrow when less than half a day of "tomorrow" would remain.
func tokenExpiry(now time.Time) (string, time.Time) {
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	if day.Sub(now) < 12*time.Hour {
		day = day.AddDate(0, 0, 1)
	}
	return day.Format("2006-01-02"), day
}

func parseExpiry(value string, fallback time.Time) time.Time {
	if parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value)); err == nil {
		return parsed.UTC()
	}
	return fallback
}

func (h *Host) mintProjectToken(ctx context.Context, connection Connection, request codehost.CredentialRequest) (codehost.Credential, error) {
	if connection.Readonly {
		return codehost.Credential{}, &Error{Kind: ErrReadOnlyConnection, Message: MessageReadOnly}
	}
	name := TokenName(request)
	if name == SandboxTokenPrefix {
		return codehost.Credential{}, fmt.Errorf("%w: session or sandbox id is required", codehost.ErrNoAccess)
	}
	project := projectPath(request.Repo)
	key := connection.WorkspaceKey + "\x00" + connection.ID + "\x00" + project + "\x00" + name
	unlock := h.tokens.lock(key)
	defer unlock()
	if cached := h.tokens.get(key); cached != nil && cached.credential.ExpiresAt.Sub(h.now()) > tokenRefreshMargin {
		return cached.credential, nil
	}
	client, err := h.client(ctx, connection)
	if err != nil {
		return codehost.Credential{}, err
	}
	existing, err := h.listProjectTokens(ctx, client, project, func(token apiAccessToken) bool { return token.Name == name })
	if err != nil {
		return codehost.Credential{}, projectTokenError(err)
	}
	expiresDate, expiresAt := tokenExpiry(h.now())
	var minted apiAccessToken
	if len(existing) > 0 {
		// Restart or expiry: rotate the token we already own (GitLab revokes
		// the old value) instead of piling up a second token.
		sort.Slice(existing, func(i, j int) bool { return existing[i].ID > existing[j].ID })
		_, err = client.do(ctx, http.MethodPost, fmt.Sprintf("/projects/%s/access_tokens/%d/rotate", project, existing[0].ID), map[string]any{"expires_at": expiresDate}, &minted)
		if err != nil && !errors.Is(err, codehost.ErrNotFound) {
			return codehost.Credential{}, projectTokenError(err)
		}
		if err != nil {
			// GitLab before 16.0 has no rotation endpoint: replace the token.
			minted = apiAccessToken{}
			_ = h.revokeProjectToken(ctx, client, project, existing[0].ID)
		}
		for _, extra := range existing[1:] {
			_ = h.revokeProjectToken(ctx, client, project, extra.ID)
		}
	}
	if minted.Token == "" {
		payload := map[string]any{"name": name, "scopes": h.tokenScopes(), "access_level": accessDeveloper, "expires_at": expiresDate}
		if _, err := client.do(ctx, http.MethodPost, "/projects/"+project+"/access_tokens", payload, &minted); err != nil {
			return codehost.Credential{}, projectTokenError(err)
		}
	}
	if minted.Token == "" || minted.ID == 0 {
		return codehost.Credential{}, &Error{Kind: codehost.ErrTransient, Message: "GitLab did not return the project access token."}
	}
	credential := codehost.Credential{
		Username:  tokenUsername,
		Password:  minted.Token,
		ExpiresAt: parseExpiry(minted.ExpiresAt, expiresAt),
		RevokeID:  encodeRevokeID(connection.ID, project, minted.ID),
		CLIEnv:    CLIEnv(connection, minted.Token),
	}
	owner := strings.TrimPrefix(strings.TrimPrefix(name, SessionTokenPrefix), SandboxTokenPrefix)
	h.tokens.put(key, &mintedToken{workspaceKey: connection.WorkspaceKey, connectionID: connection.ID, project: project, ownerID: owner, tokenID: minted.ID, credential: credential}, request.Repo)
	return credential, nil
}

// projectTokenError explains 403/404 from the access token API: the
// connection token lacks api scope or Maintainer, or gitlab.com needs Premium.
func projectTokenError(err error) error {
	if errors.Is(err, codehost.ErrNoAccess) || errors.Is(err, codehost.ErrNotFound) {
		return &Error{Kind: ErrProjectTokensUnavailable, Message: MessageProjectTokens, Status: statusOf(err)}
	}
	return err
}

func (h *Host) listProjectTokens(ctx context.Context, client *apiClient, project string, match func(apiAccessToken) bool) ([]apiAccessToken, error) {
	found := []apiAccessToken{}
	page := "1"
	for pages := 0; page != "" && pages < 20; pages++ {
		var tokens []apiAccessToken
		resp, err := client.do(ctx, http.MethodGet, "/projects/"+project+"/access_tokens?state=active&per_page=100&page="+page, nil, &tokens)
		if err != nil {
			return nil, err
		}
		for _, token := range tokens {
			if token.Revoked || !token.Active {
				continue
			}
			if match(token) {
				found = append(found, token)
			}
		}
		page = resp.NextPage
	}
	return found, nil
}

func (h *Host) revokeProjectToken(ctx context.Context, client *apiClient, project string, tokenID int64) error {
	_, err := client.do(ctx, http.MethodDelete, fmt.Sprintf("/projects/%s/access_tokens/%d", project, tokenID), nil, nil)
	if err == nil || errors.Is(err, codehost.ErrNotFound) {
		return nil
	}
	return err
}

func encodeRevokeID(connectionID, project string, tokenID int64) string {
	return strings.Join([]string{revokePrefix, connectionID, project, strconv.FormatInt(tokenID, 10)}, "|")
}

func decodeRevokeID(value string) (connectionID, project string, tokenID int64, ok bool) {
	parts := strings.Split(value, "|")
	if len(parts) != 4 || parts[0] != revokePrefix || parts[1] == "" || parts[2] == "" {
		return "", "", 0, false
	}
	id, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || id <= 0 {
		return "", "", 0, false
	}
	return parts[1], parts[2], id, true
}

// RevokeCredential revokes a project access token early. OAuth and
// connection credentials have no RevokeID and are left alone. Revoking an
// already revoked or expired token succeeds.
func (h *Host) RevokeCredential(ctx context.Context, workspaceKey string, credential codehost.Credential) error {
	connectionID, project, tokenID, ok := decodeRevokeID(credential.RevokeID)
	if !ok {
		return nil
	}
	h.tokens.remove(func(token *mintedToken) bool {
		return token.workspaceKey == workspaceKey && token.connectionID == connectionID && token.tokenID == tokenID
	})
	return h.revoke(ctx, workspaceKey, connectionID, project, tokenID)
}

func (h *Host) revoke(ctx context.Context, workspaceKey, connectionID, project string, tokenID int64) error {
	connections, err := h.connections(ctx, workspaceKey)
	if err != nil {
		return err
	}
	for _, connection := range connections {
		if connection.ID != connectionID {
			continue
		}
		client, err := h.client(ctx, connection)
		if err != nil {
			return err
		}
		return h.revokeProjectToken(ctx, client, project, tokenID)
	}
	// The connection is gone; the token dies with its expiry (at most ~2 days).
	return nil
}

// RevokeSessionCredentials revokes every project token Flow minted for a
// session (or a session-less sandbox id) and still remembers. The session
// layer calls it when a session ends; SweepProjectTokens catches the rest
// (tokens minted before a restart).
func (h *Host) RevokeSessionCredentials(ctx context.Context, workspaceKey, ownerID string) error {
	removed := h.tokens.remove(func(token *mintedToken) bool {
		return token.workspaceKey == workspaceKey && token.ownerID == ownerID
	})
	var errs []error
	for _, token := range removed {
		if err := h.revoke(ctx, workspaceKey, token.connectionID, token.project, token.tokenID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// TokenKeep decides whether a Flow-minted project token stays: name is the
// token name (flow-cs-<sessionId> or flow-sb-<sandboxId>).
type TokenKeep func(name string, createdAt time.Time) bool

// SweepProjectTokens revokes Flow's leftover project access tokens in the
// given projects (plus every project this process minted in) for which keep
// returns false. Non-Flow tokens are never touched. It is idempotent and
// safe to run on several replicas.
func (h *Host) SweepProjectTokens(ctx context.Context, workspaceKey string, projects []codehost.RepoRef, keep TokenKeep) (int, error) {
	connections, err := h.connections(ctx, workspaceKey)
	if err != nil || len(connections) == 0 {
		return 0, err
	}
	targets := map[string]codehost.RepoRef{}
	for _, repo := range append(projects, h.tokens.knownProjects(workspaceKey)...) {
		if repo.FullName == "" && repo.ID == "" {
			continue
		}
		targets[strings.ToLower(repo.HostName)+"\x00"+projectPath(repo)] = repo
	}
	revoked := 0
	var errs []error
	for _, repo := range targets {
		connection, err := h.connection(ctx, workspaceKey, repo.HostName)
		if err != nil || connection.Readonly {
			continue
		}
		client, err := h.client(ctx, connection)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		project := projectPath(repo)
		tokens, err := h.listProjectTokens(ctx, client, project, func(token apiAccessToken) bool {
			return strings.HasPrefix(token.Name, SessionTokenPrefix) || strings.HasPrefix(token.Name, SandboxTokenPrefix)
		})
		if err != nil {
			if !errors.Is(err, codehost.ErrNoAccess) && !errors.Is(err, codehost.ErrNotFound) {
				errs = append(errs, err)
			}
			continue
		}
		for _, token := range tokens {
			if keep != nil && keep(token.Name, token.CreatedAt) {
				continue
			}
			if err := h.revokeProjectToken(ctx, client, project, token.ID); err != nil {
				errs = append(errs, err)
				continue
			}
			h.tokens.remove(func(cached *mintedToken) bool {
				return cached.connectionID == connection.ID && cached.tokenID == token.ID
			})
			revoked++
			log.Printf("coding sessions: revoked GitLab project token %s (id %d) in %s", token.Name, token.ID, repo.FullName)
		}
	}
	return revoked, errors.Join(errs...)
}

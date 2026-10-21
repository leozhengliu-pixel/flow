// Package gitlab is the GitLab code host (track J), built on the existing
// GitLab token/OAuth connections. Sandbox credentials are per-session project
// access tokens minted through the API (FLOW_CODING_GITLAB_CREDENTIAL_MODE),
// MRs are opened by Flow as drafts, and Merge Request / Note / Pipeline hooks
// become session follow-ups. See docs/coding-sessions-design.md §14.
//
// The package never reads workspace state or connector secrets itself. The
// server wires a ConnectionSource (api/cmd/server/gitlab_coding.go) that
// exposes the workspace's existing GitLab connections: the encrypted token of
// a token connection, or the (refreshed) OAuth access token of an OAuth
// connection, plus the SSRF-hardened HTTP client the integration already uses.
package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

func init() { codehost.Register(domain.CodeHostGitLab, New) }

// Credential modes (FLOW_CODING_GITLAB_CREDENTIAL_MODE).
const (
	ModeProjectToken = "project-token"
	ModeOAuth        = "oauth"
	ModeConnection   = "connection"
)

// Connection kinds.
const (
	ConnectionToken = "token" // personal/group/project access token pasted by an admin
	ConnectionOAuth = "oauth" // the workspace's GitLab OAuth authorization
)

// Connection is one of the workspace's GitLab connections, without secrets.
type Connection struct {
	ID           string
	WorkspaceKey string
	// BaseURL is scheme://host[/relative-root] without /api/v4.
	BaseURL string
	// HostName is the host (and port) of BaseURL, e.g. gitlab.com.
	HostName string
	Kind     string // ConnectionToken | ConnectionOAuth
	// Username is the GitLab user behind the connection token; its notes are
	// marked AuthorIsBot in webhooks so Flow never reacts to itself.
	Username string
	Scopes   []string
	// Readonly is true for read_api tokens (no project tokens, no MR writes).
	Readonly bool
}

// ConnectionSource exposes the workspace's GitLab connections to the host.
type ConnectionSource interface {
	// GitLabConnections lists connected GitLab connections (status connected
	// or configured) of the workspace.
	GitLabConnections(ctx context.Context, workspaceKey string) ([]Connection, error)
	// GitLabAPIToken returns the token Flow itself uses for API calls: the
	// connection's access token, or a fresh OAuth access token.
	GitLabAPIToken(ctx context.Context, workspaceKey, connectionID string) (string, error)
	// GitLabOAuthToken returns the connection's OAuth access token, refreshed
	// when it expires within a few minutes, and its expiry.
	GitLabOAuthToken(ctx context.Context, workspaceKey, connectionID string) (string, time.Time, error)
	// GitLabHTTPClient returns a client allowed to reach baseURL.
	GitLabHTTPClient(ctx context.Context, baseURL string) (*http.Client, error)
	// GitLabDeploymentHosts are hosts configured for the whole deployment
	// (FLOW_INTEGRATION_GITLAB_HOST), used for the global egress allowlist.
	GitLabDeploymentHosts() []string
}

// Host implements codehost.Host for GitLab.
type Host struct {
	deps codehost.Dependencies

	mu     sync.RWMutex
	source ConnectionSource
	// shared/sharedLock: the project tokens every replica serves (SetSharedTokens).
	shared     SharedTokenStore
	sharedLock TokenLocker

	tokens *tokenCache
}

var _ codehost.Host = (*Host)(nil)

// New builds the GitLab host.
func New(deps codehost.Dependencies) (codehost.Host, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Host{deps: deps, tokens: newTokenCache()}, nil
}

// SetConnectionSource wires the workspace connections (done once at startup
// by the server). Without a source every workspace reads as not connected.
func (h *Host) SetConnectionSource(source ConnectionSource) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.source = source
}

func (h *Host) connectionSource() ConnectionSource {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.source
}

// Kind is gitlab.
func (h *Host) Kind() codehost.Kind { return domain.CodeHostGitLab }

// CredentialMode is the configured sandbox credential mode.
func (h *Host) CredentialMode() string {
	mode := strings.ToLower(strings.TrimSpace(h.deps.Config.GitLab.CredentialMode))
	if mode == "" {
		return ModeProjectToken
	}
	return mode
}

func (h *Host) tokenScopes() []string {
	scopes := []string{}
	for _, scope := range h.deps.Config.GitLab.TokenScopes {
		if scope = strings.TrimSpace(scope); scope != "" {
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 0 {
		return []string{"read_repository", "write_repository", "read_api"}
	}
	return scopes
}

func (h *Host) now() time.Time { return h.deps.Now().UTC() }

// Connected reports whether the workspace has a usable GitLab connection.
func (h *Host) Connected(ctx context.Context, workspaceKey string) (bool, error) {
	connections, err := h.connections(ctx, workspaceKey)
	if err != nil {
		return false, err
	}
	return len(connections) > 0, nil
}

func (h *Host) connections(ctx context.Context, workspaceKey string) ([]Connection, error) {
	source := h.connectionSource()
	if source == nil {
		return nil, nil
	}
	connections, err := source.GitLabConnections(ctx, workspaceKey)
	if err != nil {
		return nil, fmt.Errorf("%w: gitlab connections: %v", codehost.ErrTransient, err)
	}
	for index := range connections {
		connections[index].BaseURL = strings.TrimRight(connections[index].BaseURL, "/")
		if connections[index].HostName == "" {
			connections[index].HostName = hostOf(connections[index].BaseURL)
		}
	}
	return connections, nil
}

// connection picks the connection serving hostName ("" = the first one).
func (h *Host) connection(ctx context.Context, workspaceKey, hostName string) (Connection, error) {
	connections, err := h.connections(ctx, workspaceKey)
	if err != nil {
		return Connection{}, err
	}
	if len(connections) == 0 {
		return Connection{}, notConnected("This workspace has no GitLab connection. Connect GitLab in Settings › Integrations › GitLab.")
	}
	hostName = strings.ToLower(strings.TrimSpace(hostName))
	if hostName == "" {
		return connections[0], nil
	}
	for _, connection := range connections {
		if strings.EqualFold(connection.HostName, hostName) {
			return connection, nil
		}
	}
	return Connection{}, notConnected(fmt.Sprintf("This workspace has no GitLab connection for %s. Connect it in Settings › Integrations › GitLab.", hostName))
}

// client builds an API client acting with the connection's own token.
func (h *Host) client(ctx context.Context, connection Connection) (*apiClient, error) {
	source := h.connectionSource()
	if source == nil {
		return nil, notConnected("GitLab is not wired into coding sessions.")
	}
	token, err := source.GitLabAPIToken(ctx, connection.WorkspaceKey, connection.ID)
	if err != nil || strings.TrimSpace(token) == "" {
		return nil, notConnected("The GitLab access token is not available. Update the token in Settings › Integrations › GitLab.")
	}
	return h.clientWithToken(ctx, connection, token)
}

func (h *Host) clientWithToken(ctx context.Context, connection Connection, token string) (*apiClient, error) {
	source := h.connectionSource()
	httpClient := h.deps.HTTPClient
	if source != nil {
		client, err := source.GitLabHTTPClient(ctx, connection.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", codehost.ErrNoAccess, err)
		}
		if client != nil {
			httpClient = client
		}
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &apiClient{http: httpClient, base: connection.BaseURL, token: token, bearer: connection.Kind == ConnectionOAuth}, nil
}

// repoClient resolves the connection for a repository and its API client.
func (h *Host) repoClient(ctx context.Context, workspaceKey string, repo codehost.RepoRef) (Connection, *apiClient, error) {
	connection, err := h.connection(ctx, workspaceKey, repo.HostName)
	if err != nil {
		return Connection{}, nil, err
	}
	client, err := h.client(ctx, connection)
	return connection, client, err
}

// projectPath is the :id path segment for a repository: the numeric id when
// known, otherwise the URL-encoded full path (group%2Fsub%2Fproject).
func projectPath(repo codehost.RepoRef) string {
	if id := strings.TrimSpace(repo.ID); id != "" && isDigits(id) {
		return id
	}
	return url.PathEscape(strings.Trim(strings.TrimSpace(repo.FullName), "/"))
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hostOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Host)
}

// EgressHosts are the GitLab hosts sandboxes must reach. With an empty
// workspace key it returns the deployment-wide hosts (and gitlab.com).
func (h *Host) EgressHosts(ctx context.Context, workspaceKey string) ([]string, error) {
	seen := map[string]bool{}
	hosts := []string{}
	add := func(host string) {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || seen[host] {
			return
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	if source := h.connectionSource(); source != nil {
		for _, host := range source.GitLabDeploymentHosts() {
			if strings.Contains(host, "://") {
				host = hostOf(host)
			}
			add(host)
		}
	}
	if strings.TrimSpace(workspaceKey) == "" {
		add("gitlab.com")
		return hosts, nil
	}
	connections, err := h.connections(ctx, workspaceKey)
	if err != nil {
		return nil, err
	}
	for _, connection := range connections {
		add(stripPort(connection.HostName))
	}
	return hosts, nil
}

func stripPort(host string) string {
	if index := strings.LastIndex(host, ":"); index > 0 && !strings.Contains(host[index:], "]") {
		return host[:index]
	}
	return host
}

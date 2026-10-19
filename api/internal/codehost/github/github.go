// Package github is the GitHub code host through the GitHub App (track D):
// App JWT (RS256, 9-minute expiry), installation tokens scoped to one
// repository with contents/pull_requests write and checks/actions read,
// draft PRs, PR comment/review webhooks. Classic OAuth/PAT GitHub
// connections keep serving PR sync unchanged.
//
// See docs/coding-sessions-design.md §13. Everything here talks to the
// GitHub REST API (github.com or GitHub Enterprise Server through
// FLOW_GITHUB_APP_HOST / FLOW_GITHUB_APP_API_URL). Installation tokens are
// cached in memory only and never persisted.
package github

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/config"
	"flow/api/internal/domain"
)

func init() { codehost.Register(domain.CodeHostGitHub, New) }

// Host implements codehost.Host for GitHub through the GitHub App.
type Host struct {
	cfg    config.GitHubAppConfig
	deps   codehost.Dependencies
	client *http.Client
	now    func() time.Time
	// apiURL is the REST base (https://api.github.com or https://<ghe>/api/v3);
	// webURL is the web base used for OAuth and install links.
	apiURL string
	webURL string

	key    *rsa.PrivateKey
	keyErr error

	mu          sync.Mutex
	jwt         string
	jwtExpires  time.Time
	tokens      map[string]cachedToken
	inflight    map[string]*inflightToken
	repoInstall map[string]repoInstallation
}

type cachedToken struct {
	token          string
	expiresAt      time.Time
	installationID string
}

type inflightToken struct {
	done  chan struct{}
	token cachedToken
	err   error
}

type repoInstallation struct {
	installationID string
	expiresAt      time.Time
}

// repoInstallTTL bounds how long a repository → installation lookup is
// reused; installation webhooks invalidate it earlier.
const repoInstallTTL = 5 * time.Minute

// tokenRefreshMargin: cached installation tokens are reused until this long
// before they expire (GitHub issues them for one hour).
const tokenRefreshMargin = 5 * time.Minute

// New builds the GitHub host from FLOW_GITHUB_APP_*. A missing App leaves a
// host that reports not connected; an unreadable private key is logged and
// surfaces as ErrNotConnected on use, so it never stops other code hosts.
func New(deps codehost.Dependencies) (codehost.Host, error) {
	cfg := deps.Config.GitHubApp
	host := strings.ToLower(strings.TrimSpace(cfg.Host))
	if host == "" {
		host = "github.com"
	}
	cfg.Host = host
	apiURL := strings.TrimRight(strings.TrimSpace(cfg.APIURL), "/")
	if apiURL == "" {
		apiURL = "https://api.github.com"
		if host != "github.com" {
			apiURL = "https://" + host + "/api/v3"
		}
	}
	client := deps.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	h := &Host{cfg: cfg, deps: deps, client: client, now: now, apiURL: apiURL, webURL: "https://" + host,
		tokens: map[string]cachedToken{}, inflight: map[string]*inflightToken{}, repoInstall: map[string]repoInstallation{}}
	if cfg.AppID != "" && cfg.PrivateKey != "" {
		h.key, h.keyErr = parsePrivateKey(cfg.PrivateKey)
		if h.keyErr != nil {
			log.Printf("WARNING: GitHub App: FLOW_GITHUB_APP_PRIVATE_KEY: %v", h.keyErr)
		}
	}
	return h, nil
}

// Kind is github.
func (h *Host) Kind() codehost.Kind { return domain.CodeHostGitHub }

// Configured reports whether the App credentials are present and usable.
func (h *Host) Configured() bool { return h.cfg.Configured() && h.key != nil }

// HostName is the GitHub host the App lives on (github.com or a GHE host).
func (h *Host) HostName() string { return h.cfg.Host }

// Slug is the App slug used in install links and the bot identity.
func (h *Host) Slug() string { return h.cfg.Slug }

// CanAuthorizeUsers reports whether user-to-server OAuth (account linking
// for personal access checks) is configured.
func (h *Host) CanAuthorizeUsers() bool {
	return h.Configured() && h.cfg.ClientID != "" && h.cfg.ClientSecret != ""
}

func (h *Host) ready() error {
	if !h.cfg.Configured() {
		return fmt.Errorf("%w: GitHub App is not configured", codehost.ErrNotConnected)
	}
	if h.key == nil {
		return fmt.Errorf("%w: GitHub App private key is unusable", codehost.ErrNotConnected)
	}
	return nil
}

// Connected reports whether the workspace has an active installation.
func (h *Host) Connected(ctx context.Context, workspaceKey string) (bool, error) {
	if h.ready() != nil {
		return false, nil
	}
	installations, err := h.workspaceInstallations(ctx, workspaceKey)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(installations, func(item domain.CodeHostInstallation) bool { return item.SuspendedAt == nil }), nil
}

// EgressHosts are the hosts sandboxes need for git, gh and release assets.
func (h *Host) EgressHosts(context.Context, string) ([]string, error) {
	if !h.cfg.Configured() {
		return nil, nil
	}
	if h.cfg.Host == "github.com" {
		return []string{"github.com", "api.github.com", "codeload.github.com", "objects.githubusercontent.com", "raw.githubusercontent.com"}, nil
	}
	hosts := []string{h.cfg.Host}
	if parsed, err := url.Parse(h.apiURL); err == nil && parsed.Hostname() != "" && !strings.EqualFold(parsed.Hostname(), h.cfg.Host) {
		hosts = append(hosts, strings.ToLower(parsed.Hostname()))
	}
	return hosts, nil
}

// workspaceInstallations lists the workspace's installations on this host.
func (h *Host) workspaceInstallations(ctx context.Context, workspaceKey string) ([]domain.CodeHostInstallation, error) {
	if h.deps.Installations == nil {
		return nil, nil
	}
	all, err := h.deps.Installations.CodeHostInstallations(ctx, workspaceKey, domain.CodeHostGitHub)
	if err != nil {
		return nil, err
	}
	result := all[:0:0]
	for _, item := range all {
		if item.HostName == "" || strings.EqualFold(item.HostName, h.cfg.Host) {
			result = append(result, item)
		}
	}
	slices.SortFunc(result, func(a, b domain.CodeHostInstallation) int { return strings.Compare(a.InstallationID, b.InstallationID) })
	return result, nil
}

// checkRepo normalizes a repository reference for this host.
func (h *Host) checkRepo(repo codehost.RepoRef) (owner, name string, err error) {
	if err := h.ready(); err != nil {
		return "", "", err
	}
	if repo.CodeHost != "" && repo.CodeHost != domain.CodeHostGitHub {
		return "", "", fmt.Errorf("github: repository %q is on %s", repo.FullName, repo.CodeHost)
	}
	if repo.HostName != "" && !strings.EqualFold(repo.HostName, h.cfg.Host) {
		return "", "", fmt.Errorf("%w: the GitHub App is installed on %s, not %s", codehost.ErrNotConnected, h.cfg.Host, repo.HostName)
	}
	owner, name, ok := strings.Cut(strings.Trim(strings.TrimSpace(repo.FullName), "/"), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("github: invalid repository %q", repo.FullName)
	}
	return owner, strings.TrimSuffix(name, ".git"), nil
}

// installationFor resolves the workspace installation that covers repo.
// No installation at all is ErrNotConnected; an installation that does not
// include the repository (or is suspended, or belongs to another
// workspace) is ErrNoAccess.
func (h *Host) installationFor(ctx context.Context, workspaceKey string, repo codehost.RepoRef) (string, error) {
	owner, name, err := h.checkRepo(repo)
	if err != nil {
		return "", err
	}
	installations, err := h.workspaceInstallations(ctx, workspaceKey)
	if err != nil {
		return "", err
	}
	if len(installations) == 0 {
		return "", fmt.Errorf("%w: no GitHub App installation in this workspace", codehost.ErrNotConnected)
	}
	installationID, err := h.repoInstallationID(ctx, owner, name)
	if err != nil {
		return "", err
	}
	index := slices.IndexFunc(installations, func(item domain.CodeHostInstallation) bool { return item.InstallationID == installationID })
	if index < 0 {
		return "", fmt.Errorf("%w: the GitHub App installation for %s/%s is not linked to this workspace", codehost.ErrNoAccess, owner, name)
	}
	if installations[index].SuspendedAt != nil {
		return "", fmt.Errorf("%w: the GitHub App installation for %s is suspended", codehost.ErrNoAccess, installations[index].AccountLogin)
	}
	return installationID, nil
}

func (h *Host) repoInstallationID(ctx context.Context, owner, name string) (string, error) {
	key := strings.ToLower(owner + "/" + name)
	now := h.now()
	h.mu.Lock()
	cached, ok := h.repoInstall[key]
	h.mu.Unlock()
	if ok && now.Before(cached.expiresAt) {
		return cached.installationID, nil
	}
	var out struct {
		ID int64 `json:"id"`
	}
	err := h.appRequest(ctx, "repo_installation", http.MethodGet, "/repos/"+pathEscape(owner)+"/"+pathEscape(name)+"/installation", nil, &out)
	if errors.Is(err, codehost.ErrNotFound) {
		return "", fmt.Errorf("%w: the GitHub App is not installed on %s/%s", codehost.ErrNoAccess, owner, name)
	}
	if err != nil {
		return "", err
	}
	id := fmt.Sprint(out.ID)
	h.mu.Lock()
	h.repoInstall[key] = repoInstallation{installationID: id, expiresAt: now.Add(repoInstallTTL)}
	h.mu.Unlock()
	return id, nil
}

// InvalidateInstallation drops cached repository lookups and tokens of an
// installation (installation and installation_repositories webhooks).
func (h *Host) InvalidateInstallation(installationID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, item := range h.repoInstall {
		if item.installationID == installationID {
			delete(h.repoInstall, key)
		}
	}
	for key, item := range h.tokens {
		if item.installationID == installationID {
			delete(h.tokens, key)
		}
	}
}

func pathEscape(segment string) string { return url.PathEscape(segment) }

// refPath escapes a branch or ref for a path, keeping its slashes.
func refPath(ref string) string {
	parts := strings.Split(ref, "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

var _ codehost.Host = (*Host)(nil)

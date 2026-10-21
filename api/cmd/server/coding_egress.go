package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// Egress ACL check for the Squid external ACL helper (docs/coding-sessions-design.md §19).
//
// The egress proxy (deploy/egress-proxy) runs `flow-api egress-acl auth|check`
// as Squid helpers. They forward each sandbox's proxy credentials
// (`http://<sandboxId>:<token>@proxy:3128`) and the requested destination
// here. Flow resolves the sandbox's environment and answers by its network
// mode. The route is authenticated with FLOW_CODING_EGRESS_ACL_SECRET, not a
// sandbox token: only the proxy may call it.
//
// Owner: track H (deploy, egress proxy, docs). Keep the route list in sync
// with publicapi.InternalRoutes.
func (s *server) registerCodingEgressRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/coding-runtime/egress-check", s.codingEgressCheck)
}

// EgressCheckRequest is what the Squid helper sends. An empty Host asks only
// whether the credentials are valid (Squid's basic auth helper).
type EgressCheckRequest struct {
	SandboxID    string `json:"sandboxId"`
	SandboxToken string `json:"sandboxToken"`
	Host         string `json:"host,omitempty"`
	Port         int    `json:"port,omitempty"`
	Method       string `json:"method,omitempty"`
}

// EgressCheckResponse is the decision. Squid caches it for TTLSeconds.
type EgressCheckResponse struct {
	Allow      bool   `json:"allow"`
	Reason     string `json:"reason,omitempty"`
	Mode       string `json:"mode,omitempty"`
	TTLSeconds int    `json:"ttlSeconds"`
}

// Egress decision reasons (stable strings; they show in Squid's access log).
const (
	egressReasonAllowed         = "allowed"
	egressReasonAuthenticated   = "authenticated"
	egressReasonInvalidToken    = "invalid_token"
	egressReasonSandboxMismatch = "sandbox_mismatch"
	egressReasonWarmUnbound     = "warm_unbound"
	egressReasonPort            = "port_not_allowed"
	egressReasonPrivate         = "private_destination"
	egressReasonNotAllowlisted  = "not_allowlisted"
	egressReasonInvalidHost     = "invalid_host"
)

const egressDecisionTTL = 60 * time.Second

// defaultEgressAllowlist is the built-in allowlist (§19). The same list is in
// deploy/egress-proxy/allowlist.default.txt and the Helm chart
// (files/allowlist-default.txt); TestDefaultEgressAllowlistMatchesDeployFiles
// keeps them in sync. A leading dot matches the domain and its subdomains.
func defaultEgressAllowlist() []string {
	return []string{
		// Code hosts.
		"github.com", "api.github.com", "codeload.github.com", "objects.githubusercontent.com",
		"raw.githubusercontent.com", "ghcr.io", "pkg-containers.githubusercontent.com",
		// Package registries.
		"registry.npmjs.org", "registry.yarnpkg.com", "pypi.org", "files.pythonhosted.org",
		"proxy.golang.org", "sum.golang.org", "crates.io", "static.crates.io", "index.crates.io",
		"rubygems.org", "repo.maven.apache.org", "repo1.maven.org", "plugins.gradle.org",
		"services.gradle.org", "deb.debian.org",
		// Toolchains.
		"mise.jdx.dev", "mise-versions.jdx.dev", "nodejs.org", "go.dev", "dl.google.com",
		"static.rust-lang.org",
	}
}

// codingEgressStore is what the decision needs from the store. It is resolved
// from s.store at request time, so the endpoint works as soon as the session
// (track B) and environment (track K) stores land.
type codingEgressStore interface {
	AuthenticateSandboxToken(ctx context.Context, token string) (coding.SandboxGrant, error)
	CodingSession(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error)
	CodingEnvironment(ctx context.Context, workspaceKey, id string) (domain.CodingEnvironment, error)
	CodingSnapshot(ctx context.Context, workspaceKey, id string) (domain.CodingEnvironmentSnapshot, error)
}

func (s *server) codingEgressCheck(w http.ResponseWriter, r *http.Request) {
	if s.coding == nil || !s.coding.config.Enabled || s.coding.config.Egress.ACLSecret == "" {
		writeError(w, http.StatusNotFound, "egress checks are not enabled")
		return
	}
	if !egressSecretMatches(r, s.coding.config.Egress.ACLSecret) {
		writeError(w, http.StatusUnauthorized, "invalid egress ACL secret")
		return
	}
	var request EgressCheckRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	// The codingstore adapter, statically typed: a missing store method is a
	// compile error, not a runtime 503.
	var egressStore codingEgressStore = s.codingStore()
	decider := egressDecider{config: s.coding.config, store: egressStore, hosts: s.coding.hosts}
	response, err := decider.Decide(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "egress check failed")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// egressSecretMatches compares `Authorization: Bearer <secret>` in constant time.
func egressSecretMatches(r *http.Request, secret string) bool {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(header[len(prefix):])), []byte(secret)) == 1
}

// egressDecider turns a request into a decision. It has no state of its own,
// so it is safe to build per request.
type egressDecider struct {
	config appconfig.CodingConfig
	store  codingEgressStore
	hosts  map[codehost.Kind]codehost.Host
}

// Decide authenticates the sandbox and applies its environment's network
// mode. An error means the decision could not be made (store or code host
// failure); the helper then answers BH and Squid denies.
func (d egressDecider) Decide(ctx context.Context, request EgressCheckRequest) (EgressCheckResponse, error) {
	deny := func(reason, mode string) EgressCheckResponse {
		return EgressCheckResponse{Allow: false, Reason: reason, Mode: mode, TTLSeconds: int(egressDecisionTTL.Seconds())}
	}
	token := strings.TrimSpace(request.SandboxToken)
	if token == "" {
		return deny(egressReasonInvalidToken, ""), nil
	}
	grant, err := d.store.AuthenticateSandboxToken(ctx, token)
	if errors.Is(err, coding.ErrTokenInvalid) || errors.Is(err, coding.ErrNotFound) {
		return deny(egressReasonInvalidToken, ""), nil
	}
	if err != nil {
		return EgressCheckResponse{}, err
	}
	// The proxy user name must be the token's sandbox, so a leaked token
	// cannot be replayed under another sandbox's identity (or none) in the
	// logs. The runner always sends both (sandbox-runner/src/proxy-env.ts).
	if id := strings.TrimSpace(request.SandboxID); id != grant.SandboxID {
		return deny(egressReasonSandboxMismatch, ""), nil
	}
	if grant.Kind == sandbox.KindWarm && grant.SessionID == "" {
		// Warm sandboxes hold no capabilities until they are bound (§11).
		return deny(egressReasonWarmUnbound, ""), nil
	}
	environment, err := d.environment(ctx, grant)
	if err != nil {
		return EgressCheckResponse{}, err
	}
	mode := normalizeEgressMode(environment.Network)
	host := strings.TrimSpace(request.Host)
	if host == "" {
		return EgressCheckResponse{Allow: true, Reason: egressReasonAuthenticated, Mode: mode, TTLSeconds: int(egressDecisionTTL.Seconds())}, nil
	}
	host, ok := normalizeEgressHost(host)
	if !ok {
		return deny(egressReasonInvalidHost, mode), nil
	}
	if request.Port != 0 && request.Port != 80 && request.Port != 443 {
		return deny(egressReasonPort, mode), nil
	}
	if egressPrivateDestination(host) {
		return deny(egressReasonPrivate, mode), nil
	}
	allowlist, err := d.allowlist(ctx, grant.WorkspaceKey, environment, mode)
	if err != nil {
		return EgressCheckResponse{}, err
	}
	if egressHostAllowed(host, allowlist) {
		return EgressCheckResponse{Allow: true, Reason: egressReasonAllowed, Mode: mode, TTLSeconds: int(egressDecisionTTL.Seconds())}, nil
	}
	if mode == domain.CodingNetworkOpen && net.ParseIP(host) == nil {
		// Open mode reaches any public host name; Squid still blocks private
		// and link-local addresses after DNS resolution.
		return EgressCheckResponse{Allow: true, Reason: egressReasonAllowed, Mode: mode, TTLSeconds: int(egressDecisionTTL.Seconds())}, nil
	}
	return deny(egressReasonNotAllowlisted, mode), nil
}

// environment finds the sandbox's environment. A sandbox without one (or
// whose environment was deleted) gets the default mode.
func (d egressDecider) environment(ctx context.Context, grant coding.SandboxGrant) (domain.CodingEnvironment, error) {
	environmentID := grant.EnvironmentID
	if environmentID == "" && grant.SessionID != "" {
		session, err := d.store.CodingSession(ctx, grant.WorkspaceKey, grant.SessionID)
		if err != nil && !errors.Is(err, coding.ErrNotFound) {
			return domain.CodingEnvironment{}, err
		}
		environmentID = session.EnvironmentID
	}
	if environmentID == "" && grant.SnapshotID != "" {
		snapshot, err := d.store.CodingSnapshot(ctx, grant.WorkspaceKey, grant.SnapshotID)
		if err != nil && !errors.Is(err, coding.ErrNotFound) {
			return domain.CodingEnvironment{}, err
		}
		environmentID = snapshot.EnvironmentID
	}
	if environmentID == "" {
		return domain.CodingEnvironment{}, nil
	}
	environment, err := d.store.CodingEnvironment(ctx, grant.WorkspaceKey, environmentID)
	if errors.Is(err, coding.ErrNotFound) {
		return domain.CodingEnvironment{}, nil
	}
	return environment, err
}

// allowlist builds the domains a sandbox may reach in mode.
//
//   - none: Flow and the environment's code host only.
//   - default: the built-in list (unless switched off), FLOW_CODING_EGRESS_ALLOWLIST,
//     every configured code host (GitHub App host, GitLab hosts) and Flow.
//   - extended: default plus the environment's AllowedDomains.
//   - open: the same list; other public host names are allowed by Decide.
func (d egressDecider) allowlist(ctx context.Context, workspaceKey string, environment domain.CodingEnvironment, mode string) ([]string, error) {
	set := map[string]bool{}
	add := func(entries ...string) {
		for _, entry := range entries {
			if normalized := normalizeEgressEntry(entry); normalized != "" {
				set[normalized] = true
			}
		}
	}
	add(egressURLHost(d.config.RuntimeURL)) // the runtime API and LLM proxy
	add(environmentCodeHostDomains(environment)...)
	if environment.CodeHost != "" {
		if host := d.hosts[environment.CodeHost]; host != nil {
			hosts, err := host.EgressHosts(ctx, workspaceKey)
			if err != nil {
				return nil, err
			}
			add(hosts...)
		}
	}
	if mode != domain.CodingNetworkNone {
		if d.config.Egress.DefaultAllowlist {
			add(defaultEgressAllowlist()...)
		}
		add(d.config.Egress.Allowlist...)
		if d.config.GitHubApp.Configured() {
			add(d.config.GitHubApp.Host, egressURLHost(d.config.GitHubApp.APIURL))
		}
		kinds := make([]string, 0, len(d.hosts))
		for kind := range d.hosts {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			if kind == environment.CodeHost || d.hosts[kind] == nil {
				continue
			}
			hosts, err := d.hosts[kind].EgressHosts(ctx, workspaceKey)
			if err != nil {
				return nil, err
			}
			add(hosts...)
		}
	}
	if mode == domain.CodingNetworkExtended {
		add(environment.AllowedDomains...)
	}
	result := make([]string, 0, len(set))
	for entry := range set {
		result = append(result, entry)
	}
	sort.Strings(result)
	return result, nil
}

// environmentCodeHostDomains is the environment's code host even when the
// code host implementation does not report egress hosts (yet).
func environmentCodeHostDomains(environment domain.CodingEnvironment) []string {
	host := strings.ToLower(strings.TrimSpace(environment.HostName))
	switch {
	case host == "" && environment.CodeHost == domain.CodeHostGitHub, host == "github.com":
		return []string{"github.com", "api.github.com", "codeload.github.com", "objects.githubusercontent.com", "raw.githubusercontent.com"}
	case host == "" && environment.CodeHost == domain.CodeHostGitLab, host == "gitlab.com":
		return []string{"gitlab.com", "registry.gitlab.com"}
	case host != "":
		return []string{host}
	}
	return nil
}

func normalizeEgressMode(mode string) string {
	switch mode {
	case domain.CodingNetworkExtended, domain.CodingNetworkOpen, domain.CodingNetworkNone:
		return mode
	default:
		return domain.CodingNetworkDefault
	}
}

// normalizeEgressHost lower-cases a destination and strips brackets and the
// trailing dot. It rejects anything that is not a plausible host name or IP.
func normalizeEgressHost(host string) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 {
		return "", false
	}
	if net.ParseIP(host) != nil {
		return host, true
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return "", false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return "", false
			}
		}
	}
	return host, true
}

// normalizeEgressEntry normalizes an allowlist entry: "*.example.com" and
// ".example.com" match subdomains, "example.com" matches only itself.
func normalizeEgressEntry(entry string) string {
	entry = strings.ToLower(strings.TrimSpace(entry))
	if entry == "" {
		return ""
	}
	if strings.Contains(entry, "://") {
		entry = egressURLHost(entry)
	}
	wildcard := strings.HasPrefix(entry, "*.") || strings.HasPrefix(entry, ".")
	entry = strings.TrimLeft(entry, "*.")
	if host, _, err := net.SplitHostPort(entry); err == nil {
		entry = host
	}
	host, ok := normalizeEgressHost(entry)
	if !ok {
		return ""
	}
	if wildcard {
		return "." + host
	}
	return host
}

func egressURLHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// egressHostAllowed applies Squid dstdomain semantics.
func egressHostAllowed(host string, allowlist []string) bool {
	for _, entry := range allowlist {
		if strings.HasPrefix(entry, ".") {
			if host == entry[1:] || strings.HasSuffix(host, entry) {
				return true
			}
		} else if host == entry {
			return true
		}
	}
	return false
}

// egressPrivateDestination rejects IP literals in private, loopback,
// link-local (cloud metadata) and other non-public ranges, and names that
// only resolve inside a cluster or host. Squid repeats the address check
// after DNS resolution.
func egressPrivateDestination(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return !egressPublicIP(ip)
	}
	if !strings.Contains(host, ".") || host == "localhost" {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".svc", ".cluster.local", ".home.arpa", ".in-addr.arpa", ".ip6.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return host == "metadata.google.internal"
}

var egressBlockedNetworks = func() []*net.IPNet {
	var networks []*net.IPNet
	for _, cidr := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "::/128", "::1/128", "64:ff9b::/96",
		"100::/64", "fc00::/7", "fe80::/10", "ff00::/8", "fd00:ec2::254/128",
	} {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil {
			networks = append(networks, network)
		}
	}
	return networks
}()

func egressPublicIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, network := range egressBlockedNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

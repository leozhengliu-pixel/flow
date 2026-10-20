package coding

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// Git relay: pushing as the member without the member's token in the
// sandbox (docs/coding-sessions-security.md, "Member credentials").
//
// A session that pushes as its creator gets origin's push URL pointed at
// Flow's runtime API:
//
//	<runtime>/internal/coding-runtime/git/<host>/<owner>/<repo>.git
//
// Fetches keep going straight to the host with the workspace's
// repository-scoped credential. For the relay URL the sandbox's git
// credential helper gets only an opaque placeholder ("fgr_…", derived from
// the sandbox token and stored next to it, so it expires and is revoked with
// the sandbox). Flow's relay accepts git smart-HTTP requests for exactly the
// session's repository (info/refs, git-upload-pack, git-receive-pack),
// swaps the placeholder for the member's token, and forwards them to
// https://<host>/<owner>/<repo>.git/…. The member's token never leaves
// Flow: a placeholder sent anywhere else (another repository, the host's
// API, any other host) carries no credential, and the relay refuses every
// other path.
//
// Why Flow and not the egress proxy: sandboxes reach code hosts through
// CONNECT tunnels, and the proxy cannot add a header inside TLS without
// intercepting it. The relay is plain HTTP(S) between the sandbox and Flow.

const (
	// GitRelayRoute is the relay's path prefix on the runtime API.
	GitRelayRoute = "/internal/coding-runtime/git/"
	// GitRelayUsername is the username the credential helper gets with the
	// placeholder (the relay ignores it).
	GitRelayUsername = "flow-git-relay"
	// GitRelayPlaceholderPrefix marks relay placeholders (store.GitRelayPlaceholderPrefix).
	GitRelayPlaceholderPrefix = "fgr_"
)

// Git smart-HTTP services the relay forwards.
const (
	GitServiceUploadPack  = "git-upload-pack"
	GitServiceReceivePack = "git-receive-pack"
)

// GitRelayURL is the push URL of a repository through Flow's relay.
func GitRelayURL(runtimeURL, hostName, repository string) string {
	runtimeURL = strings.TrimRight(runtimeURL, "/")
	if runtimeURL == "" || hostName == "" || repository == "" {
		return ""
	}
	return runtimeURL + GitRelayRoute + strings.ToLower(hostName) + "/" + repository + ".git"
}

// usesGitRelay: the session pushes as its creator (it was started by a
// member with a linked account on the session's host), so its pushes go
// through the relay.
func (s *Service) usesGitRelay(session domain.CodingSession) bool {
	return s.Members != nil && session.CreatorID != "" && session.PushIdentity != nil && session.PushIdentity.Via == domain.CodeHostAccountActorMember &&
		strings.TrimSpace(s.cfg.RuntimeURL) != "" && session.HostName != "" && session.Repository != ""
}

// gitRelayPlaceholder is the session-bound placeholder for a sandbox token:
// stable for the token (git asks the helper on every operation), useless
// without Flow, and not reversible to the token.
func gitRelayPlaceholder(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("flow git relay placeholder v1"))
	return GitRelayPlaceholderPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// GitRelayPath is a parsed relay path: <host>/<repository>.git/<rest>.
type GitRelayPath struct {
	HostName   string
	Repository string
	// Service is git-upload-pack or git-receive-pack.
	Service string
	// Advertisement is true for GET info/refs?service=…, false for the
	// POST <service> request.
	Advertisement bool
}

// ParseGitRelayPath parses the path after GitRelayRoute for a smart-HTTP
// request (method, path and the service query parameter). Anything that is
// not one of the three smart-HTTP endpoints of a repository is refused.
func ParseGitRelayPath(method, path, serviceParam string) (GitRelayPath, bool) {
	path = strings.TrimPrefix(path, "/")
	if path == "" || strings.Contains(path, "//") || strings.Contains(path, "\\") || strings.ContainsAny(path, "?#%") {
		return GitRelayPath{}, false
	}
	host, rest, found := strings.Cut(path, "/")
	if !found || host == "" {
		return GitRelayPath{}, false
	}
	index := strings.Index(rest, ".git/")
	if index <= 0 {
		return GitRelayPath{}, false
	}
	repository, endpoint := rest[:index], rest[index+len(".git/"):]
	segments := strings.Split(repository, "/")
	if len(segments) < 2 {
		return GitRelayPath{}, false
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return GitRelayPath{}, false
		}
	}
	result := GitRelayPath{HostName: strings.ToLower(host), Repository: repository}
	switch {
	case method == "GET" && endpoint == "info/refs" && (serviceParam == GitServiceUploadPack || serviceParam == GitServiceReceivePack):
		result.Service, result.Advertisement = serviceParam, true
	case method == "POST" && (endpoint == GitServiceUploadPack || endpoint == GitServiceReceivePack):
		result.Service = endpoint
	default:
		return GitRelayPath{}, false
	}
	return result, true
}

// gitRelayCredentialPath reports whether a credential helper request is for
// the relay URL, and returns the relay path (host/repository[.git]) after
// the route.
func gitRelayCredentialPath(path string) (string, bool) {
	path = strings.TrimPrefix(path, "/")
	route := strings.TrimPrefix(GitRelayRoute, "/")
	index := strings.Index(path, route)
	if index < 0 {
		return "", false
	}
	return path[index+len(route):], true
}

// relayCredential answers the credential helper for the relay URL: the
// placeholder, only for the session's own repository.
func (s *Service) relayCredential(ctx context.Context, token string, grant SandboxGrant, request CredentialRequest, relayPath string) (CredentialResponse, error) {
	if grant.Kind == sandbox.KindSnapshot || grant.Kind == sandbox.KindDebug || (request.Purpose != "" && request.Purpose != "git") {
		return CredentialResponse{}, ErrForbidden
	}
	session, err := s.sessionFor(ctx, grant)
	if err != nil {
		return CredentialResponse{}, err
	}
	if !s.usesGitRelay(session) {
		return CredentialResponse{}, ErrForbidden
	}
	if runtime, err := url.Parse(s.cfg.RuntimeURL); err == nil && runtime.Host != "" && request.Host != "" && !strings.EqualFold(request.Host, runtime.Host) {
		return CredentialResponse{}, ErrForbidden
	}
	host, repository, _ := strings.Cut(strings.TrimSuffix(strings.Trim(relayPath, "/"), ".git"), "/")
	if !strings.EqualFold(host, session.HostName) || !strings.EqualFold(repository, session.Repository) {
		return CredentialResponse{}, ErrForbidden
	}
	placeholder := gitRelayPlaceholder(token)
	if err := s.store.RegisterGitRelayPlaceholder(ctx, token, placeholder); err != nil {
		return CredentialResponse{}, err
	}
	return CredentialResponse{Username: GitRelayUsername, Password: placeholder, ExpiresAt: grant.ExpiresAt}, nil
}

// GitRelayTarget is where and how the relay forwards one request.
type GitRelayTarget struct {
	CodeHost   string
	HostName   string
	Repository string
	// Username and Password authenticate the forwarded request (HTTP Basic,
	// the form git itself sends). Never returned to the sandbox.
	Username string
	Password string
	// Member is true when the credential is the creator's own token (false:
	// the workspace credential, e.g. after the member unlinked).
	Member    bool
	SessionID string
}

// GitRelay authenticates a relay request by its placeholder and returns the
// credential to forward it with: the session creator's own token for the
// session's one repository, or the workspace's repository-scoped credential
// when the creator's token is unavailable. Any other repository or host, a
// finished session, or a revoked or expired sandbox is refused.
func (s *Service) GitRelay(ctx context.Context, placeholder string, path GitRelayPath) (GitRelayTarget, error) {
	if !strings.HasPrefix(placeholder, GitRelayPlaceholderPrefix) || len(placeholder) > 512 {
		return GitRelayTarget{}, ErrTokenInvalid
	}
	grant, err := s.store.AuthenticateGitRelayPlaceholder(ctx, placeholder)
	if err != nil {
		return GitRelayTarget{}, ErrTokenInvalid
	}
	if !grant.ExpiresAt.IsZero() && !s.now().Before(grant.ExpiresAt) {
		return GitRelayTarget{}, ErrTokenInvalid
	}
	if grant.Kind == sandbox.KindSnapshot || grant.Kind == sandbox.KindDebug {
		return GitRelayTarget{}, ErrForbidden
	}
	session, err := s.sessionFor(ctx, grant)
	if err != nil {
		return GitRelayTarget{}, err
	}
	if !s.usesGitRelay(session) || !strings.EqualFold(path.HostName, session.HostName) || !strings.EqualFold(path.Repository, session.Repository) {
		return GitRelayTarget{}, ErrForbidden
	}
	host, ok := s.hosts[session.CodeHost]
	if !ok {
		return GitRelayTarget{}, codehost.ErrNotConnected
	}
	credential, member, err := s.memberGitCredential(ctx, session, "git", codehost.AccessWrite)
	if err != nil {
		return GitRelayTarget{}, err
	}
	if !member {
		credential, err = host.MintCredential(ctx, codehost.CredentialRequest{WorkspaceKey: session.WorkspaceKey, SessionID: session.ID, SandboxID: grant.SandboxID, Repo: codehost.RepoRef{CodeHost: session.CodeHost, HostName: session.HostName, FullName: session.Repository}, Access: codehost.AccessWrite})
		if err != nil {
			return GitRelayTarget{}, err
		}
	}
	if credential.Password == "" {
		return GitRelayTarget{}, codehost.ErrNoAccess
	}
	return GitRelayTarget{CodeHost: session.CodeHost, HostName: strings.ToLower(session.HostName), Repository: session.Repository, Username: credential.Username, Password: credential.Password, Member: member, SessionID: session.ID}, nil
}

// GitRelayTimeout bounds one relayed request (a large clone or push).
const GitRelayTimeout = 30 * time.Minute

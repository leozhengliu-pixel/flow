// Package codehost abstracts GitHub (through the GitHub App, track D) and
// GitLab (track J) for coding sessions: repository listing for the
// environment editor, short-lived repository-scoped sandbox credentials,
// draft PR/MR creation and lookup, comments, and normalized webhooks that the
// session layer maps to follow-ups. See docs/coding-sessions-design.md §13-§14.
//
// The runner pushes branches itself with a credential minted here (fetched by
// the git credential helper through Flow's runtime API); Flow, not the agent,
// creates the PR/MR, so linking is reliable and idempotent.
package codehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// Kind is a code host: domain.CodeHostGitHub or domain.CodeHostGitLab.
type Kind = string

var (
	ErrNotConnected   = errors.New("codehost: not connected")           // → noGitHubConnection / noGitLabConnection
	ErrNoAccess       = errors.New("codehost: no access to repository") // → noCodeAccess
	ErrUserNoAccess   = errors.New("codehost: user has no access")      // → personalAccessDenied
	ErrTransient      = errors.New("codehost: transient error")         // → githubTransientError / gitlabTransientError
	ErrNotFound       = errors.New("codehost: not found")
	ErrNotImplemented = errors.New("codehost: not implemented")
	ErrBadSignature   = errors.New("codehost: invalid webhook signature")
	// ErrUserNotLinked: the policy needs the requesting user's linked host
	// account and there is none (→ requiresGithubConnection / requiresGitlabConnection).
	ErrUserNotLinked = errors.New("codehost: user has not linked a host account")
	// ErrRepoNotInPolicy: the repository is outside what the workspace or
	// Loop policy allows on this host (→ repoNotInPolicy).
	ErrRepoNotInPolicy = errors.New("codehost: repository not allowed by policy")
)

// RepoRef names a repository on a host.
type RepoRef struct {
	CodeHost Kind   `json:"codeHost"`
	HostName string `json:"hostName"` // github.com, GHE host, gitlab.com, self-managed host
	FullName string `json:"fullName"` // owner/name or group/subgroup/project
	ID       string `json:"id,omitempty"`
}

// Repository is a repository the workspace can use.
type Repository struct {
	RepoRef
	DefaultBranch string `json:"defaultBranch"`
	CloneURL      string `json:"cloneUrl"` // https URL; credentials come from the helper
	WebURL        string `json:"webUrl"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
	// InstallationID is the GitHub App installation or GitLab connection id.
	InstallationID string `json:"installationId,omitempty"`
}

// ListOptions filters ListRepositories.
type ListOptions struct {
	HostName string
	Query    string
	Cursor   string
	Limit    int
}

// RepositoryPage is a page of repositories.
type RepositoryPage struct {
	Repositories []Repository `json:"repositories"`
	NextCursor   string       `json:"nextCursor,omitempty"`
}

// Access is what a sandbox credential may do.
type Access string

const (
	AccessRead  Access = "read"  // clone/fetch, read API (CI status, MR/PR comments)
	AccessWrite Access = "write" // plus push branches and comment
)

// CredentialRequest asks for a sandbox credential for one repository.
type CredentialRequest struct {
	WorkspaceKey string
	SessionID    string
	SandboxID    string
	Repo         RepoRef
	Access       Access
}

// Credential is a short-lived, repository-scoped secret. It is handed to the
// runner (git credential helper, GH_TOKEN / GITLAB_TOKEN) and never persisted
// in plaintext; RevokeID lets Flow revoke it early (GitLab project tokens).
type Credential struct {
	Username  string    `json:"username"`
	Password  string    `json:"password"`
	ExpiresAt time.Time `json:"expiresAt"`
	RevokeID  string    `json:"-"`
	// CLIEnv is the env for gh/glab (GH_TOKEN/GH_HOST or GITLAB_TOKEN/GITLAB_HOST).
	CLIEnv map[string]string `json:"cliEnv,omitempty"`
}

// CredentialInvalidator is implemented by hosts whose minted credentials can
// go stale before their expiry (GitLab project tokens: rotated or revoked).
// git reports a credential the host rejected through the helper's "erase";
// fingerprint is CredentialFingerprint of the rejected password. The host
// drops its current credential for the request only when the fingerprint
// matches it and the host confirms it is no longer valid, so the next mint
// returns a fresh one. dropped reports whether it did.
type CredentialInvalidator interface {
	InvalidateCredential(ctx context.Context, request CredentialRequest, fingerprint string) (dropped bool, err error)
}

// CredentialFingerprint identifies a credential value without revealing it
// (hex SHA-256; logs use its first 12 characters).
func CredentialFingerprint(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

// UserIdentity is the requesting Flow user's identity on the host.
type UserIdentity struct {
	FlowUserID string
	Login      string // host username, from the user's linked account
	Email      string
}

// ChangeRequest is a GitHub pull request or a GitLab merge request.
type ChangeRequest struct {
	Repo        RepoRef `json:"repo"`
	Number      int     `json:"number"` // PR number or MR iid
	ExternalID  string  `json:"externalId"`
	URL         string  `json:"url"`
	Title       string  `json:"title"`
	State       string  `json:"state"` // open | closed | merged
	Draft       bool    `json:"draft"`
	HeadBranch  string  `json:"headBranch"`
	BaseBranch  string  `json:"baseBranch"`
	HeadSHA     string  `json:"headSha"`
	AuthorLogin string  `json:"authorLogin,omitempty"`
	// Body is the current description (markdown). Hosts fill it in
	// GetChangeRequest so Flow can rewrite only its own sections
	// (coding.MergeArtifactSection, track I).
	Body string `json:"body,omitempty"`

	// HeadRepository is the head's repository full name when it differs
	// from Repo (a fork); "" means the same repository. (track L, additive)
	HeadRepository string `json:"headRepository,omitempty"`

	// OpenedByMember is true when CreateChangeRequest opened the PR/MR with
	// the input's ActAs token (the member's own account).
	OpenedByMember bool `json:"-"`
}

// ActingUser is a member's own short-lived host credential, used to open a
// PR/MR as that member. It is only ever built for the member who asked for
// the action, used for one request, and never persisted or logged.
type ActingUser struct {
	FlowUserID string
	Login      string
	Token      string
	// Bearer sends "Authorization: Bearer"; otherwise GitLab's PRIVATE-TOKEN
	// header (personal access tokens). GitHub user tokens are always Bearer.
	Bearer bool
}

// CreateChangeRequestInput opens a draft PR/MR.
type CreateChangeRequestInput struct {
	Repo   RepoRef
	Head   string
	Base   string
	Title  string
	Body   string // markdown: summary, issue link, screenshots
	Draft  bool
	Labels []string
	// ActAs, when set, opens the PR/MR as the member. When the host rejects
	// the member's token the workspace credential opens it instead
	// (ChangeRequest.OpenedByMember tells which).
	ActAs *ActingUser
}

// CIStatus is the combined check/pipeline status of a ref.
type CIStatus struct {
	State   string `json:"state"` // pending | success | failure | none
	URL     string `json:"url,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// WebhookEventType is a normalized webhook event.
type WebhookEventType string

const (
	EventInstallation        WebhookEventType = "installation"
	EventChangeRequestOpened WebhookEventType = "changeRequestOpened"
	EventChangeRequestClosed WebhookEventType = "changeRequestClosed"
	EventChangeRequestMerged WebhookEventType = "changeRequestMerged"
	EventChangeRequestUpdate WebhookEventType = "changeRequestUpdated" // push to head, ready for review, title
	EventComment             WebhookEventType = "comment"              // PR/MR conversation comment
	EventReviewComment       WebhookEventType = "reviewComment"        // inline diff comment
	EventReview              WebhookEventType = "review"               // submitted review (changes requested…)
	EventCIStatus            WebhookEventType = "ciStatus"             // check suite / pipeline finished
	EventIgnored             WebhookEventType = "ignored"
)

// WebhookEvent is a verified, normalized webhook delivery.
type WebhookEvent struct {
	DeliveryID string
	Type       WebhookEventType
	// Action is the host's raw action ("created", "deleted", "suspend",
	// "added", "synchronize", "ready_for_review"…), for consumers that need
	// more than Type (installation lifecycle, draft changes).
	Action         string
	CodeHost       Kind
	HostName       string
	InstallationID string
	Repo           RepoRef
	ChangeRequest  *ChangeRequest
	CommentID      string
	CommentBody    string
	CommentPath    string // inline comment file
	CommentLine    int
	AuthorLogin    string
	AuthorIsBot    bool
	ReviewState    string // approved | changes_requested | commented
	CI             *CIStatus
	Installation   *domain.CodeHostInstallation

	// Track L additions: the diff hunk an inline review comment is on, a
	// link to the comment, and the commenter's public email when the host
	// sends one (helps map the commenter to a Flow member).
	CommentDiffHunk string
	CommentURL      string
	AuthorEmail     string
}

// Host is one code host implementation.
type Host interface {
	Kind() Kind
	// Connected reports whether the workspace can use this host at all.
	Connected(ctx context.Context, workspaceKey string) (bool, error)
	ListRepositories(ctx context.Context, workspaceKey string, options ListOptions) (RepositoryPage, error)
	ResolveRepository(ctx context.Context, workspaceKey string, repo RepoRef) (Repository, error)
	// CheckUserAccess verifies the requesting user may push to the repo
	// (the personal access check); ErrUserNoAccess when not.
	CheckUserAccess(ctx context.Context, workspaceKey string, repo RepoRef, user UserIdentity) error
	MintCredential(ctx context.Context, request CredentialRequest) (Credential, error)
	RevokeCredential(ctx context.Context, workspaceKey string, credential Credential) error
	// BranchHead returns the SHA of a branch ("" + ErrNotFound when absent);
	// Flow uses it to verify the runner's push before opening a PR/MR.
	BranchHead(ctx context.Context, workspaceKey string, repo RepoRef, branch string) (string, error)
	FindChangeRequest(ctx context.Context, workspaceKey string, repo RepoRef, head string) (*ChangeRequest, error)
	GetChangeRequest(ctx context.Context, workspaceKey string, repo RepoRef, number int) (ChangeRequest, error)
	// CreateChangeRequest is idempotent: an open PR/MR for Head is returned as is.
	CreateChangeRequest(ctx context.Context, workspaceKey string, input CreateChangeRequestInput) (ChangeRequest, error)
	UpdateChangeRequestBody(ctx context.Context, workspaceKey string, repo RepoRef, number int, body string) error
	Comment(ctx context.Context, workspaceKey string, repo RepoRef, number int, body string) error
	CIStatus(ctx context.Context, workspaceKey string, repo RepoRef, ref string) (CIStatus, error)
	// ParseWebhook verifies and normalizes a delivery for this host.
	ParseWebhook(ctx context.Context, header http.Header, body []byte) (WebhookEvent, error)
	// EgressHosts are the hostnames sandboxes must reach for this host (egress allowlist).
	EgressHosts(ctx context.Context, workspaceKey string) ([]string, error)
}

// ChangeRequestCloser is implemented by hosts that can close a PR/MR without
// merging it (archiving a session closes its open pull requests).
type ChangeRequestCloser interface {
	CloseChangeRequest(ctx context.Context, workspaceKey string, repo RepoRef, number int) error
}

// Secrets reads encrypted connector secrets (store.ReadConnectorSecret).
type Secrets interface {
	ReadConnectorSecret(ctx context.Context, id string, consume bool) ([]byte, error)
}

// Installations persists GitHub App installations and finds GitLab
// connections (implemented by *store.SQLiteStore, track D/J).
type Installations interface {
	CodeHostInstallations(ctx context.Context, workspaceKey, codeHost string) ([]domain.CodeHostInstallation, error)
	WorkspacesForInstallation(ctx context.Context, codeHost, hostName, installationID string) ([]string, error)
	IntegrationConnections(ctx context.Context, workspaceKey, provider string) ([]domain.IntegrationConnection, error)
}

// Dependencies are what host implementations get from Flow.
type Dependencies struct {
	Config        config.CodingConfig
	HTTPClient    *http.Client
	Secrets       Secrets
	Installations Installations
	Now           func() time.Time
}

// Factory builds a host.
type Factory func(deps Dependencies) (Host, error)

var (
	registryMu sync.RWMutex
	registry   = map[Kind]Factory{}
)

// Register makes a host available (called from init in github/ and gitlab/).
func Register(kind Kind, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[kind]; exists {
		panic("codehost: registered twice: " + kind)
	}
	registry[kind] = factory
}

// OpenAll builds every registered host.
func OpenAll(deps Dependencies) (map[Kind]Host, error) {
	registryMu.RLock()
	kinds := make([]Kind, 0, len(registry))
	for kind := range registry {
		kinds = append(kinds, kind)
	}
	registryMu.RUnlock()
	sort.Strings(kinds)
	hosts := map[Kind]Host{}
	for _, kind := range kinds {
		registryMu.RLock()
		factory := registry[kind]
		registryMu.RUnlock()
		host, err := factory(deps)
		if err != nil {
			return nil, fmt.Errorf("codehost %s: %w", kind, err)
		}
		hosts[kind] = host
	}
	return hosts, nil
}

// ErrorCode maps a host error to the session error code.
func ErrorCode(kind Kind, err error) domain.CodingErrorCode {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNotConnected) && kind == domain.CodeHostGitLab:
		return domain.CodingErrNoGitLabConnection
	case errors.Is(err, ErrNotConnected):
		return domain.CodingErrNoGitHubConnection
	case errors.Is(err, ErrNoAccess):
		return domain.CodingErrNoCodeAccess
	case errors.Is(err, ErrUserNoAccess):
		return domain.CodingErrPersonalAccessDenied
	case errors.Is(err, ErrUserNotLinked) && kind == domain.CodeHostGitLab:
		return domain.CodingErrRequiresGitLabConnection
	case errors.Is(err, ErrUserNotLinked):
		return domain.CodingErrRequiresGitHubConnection
	case errors.Is(err, ErrRepoNotInPolicy):
		return domain.CodingErrRepoNotInPolicy
	case errors.Is(err, ErrTransient) && kind == domain.CodeHostGitLab:
		return domain.CodingErrGitLabTransientError
	case errors.Is(err, ErrTransient):
		return domain.CodingErrGitHubTransientError
	default:
		return domain.CodingErrInternal
	}
}

// ErrorDescriber is implemented by hosts that refine ErrorCode for their own
// errors and carry user-facing messages (track J's *gitlab.Host). The session
// core uses it through DescribeError.
type ErrorDescriber interface {
	DescribeError(err error) (code domain.CodingErrorCode, message string)
}

// DescribeError maps a host error to a session error code and message,
// preferring the host's own mapping. message falls back to err.Error().
func DescribeError(host Host, kind Kind, err error) (domain.CodingErrorCode, string) {
	if err == nil {
		return "", ""
	}
	code, message := ErrorCode(kind, err), ""
	if describer, ok := host.(ErrorDescriber); ok {
		code, message = describer.DescribeError(err)
	}
	if message == "" {
		message = err.Error()
	}
	return code, message
}

package coding

import (
	"context"
	"errors"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// Track B's persistence and collaborator interfaces beyond the frozen
// store.go contracts. *store.SQLiteStore implements CoreStore
// (api/internal/store/coding_sessions.go, coding_usage.go) and AnchorStore
// (the existing agent task methods). Other tracks' stores are reached through
// the narrow optional interfaces below, so this package compiles and tests
// before they land.

// Service errors.
var (
	ErrDisabled      = errors.New("coding: coding sessions are disabled")
	ErrInvalidInput  = domain.ErrCodingInvalidInput
	ErrConflict      = errors.New("coding: the session cannot do that in its current state")
	ErrGone          = errors.New("coding: the sandbox no longer belongs to a live session")
	ErrNotBound      = errors.New("coding: the warm sandbox is not bound to a session yet")
	ErrNoAgent       = errors.New("coding: the built-in Flow agent is not installed in this workspace")
	ErrForbidden     = errors.New("coding: not allowed for this sandbox")
	ErrQuotaExceeded = errors.New("coding: spend limit reached")
	ErrAlreadyUsed   = errors.New("coding: setup secrets were already served to this sandbox")
)

// CoreStore is the session store plus track B's additional methods.
type CoreStore interface {
	SessionStore
	UsageStore
	// PendingCodingTurns counts turn items that were not delivered to a runner yet.
	PendingCodingTurns(ctx context.Context, sessionID string) (int, error)
	// MarkCodingInboxDelivered stamps delivered_at on an inbox item (idempotent).
	MarkCodingInboxDelivered(ctx context.Context, sessionID string, seq int64, at time.Time) error
	// UndeliveredCodingInbox lists items not delivered yet, oldest first.
	UndeliveredCodingInbox(ctx context.Context, sessionID string) ([]InboxItem, error)
	CodingCommits(ctx context.Context, sessionID string) ([]domain.CodingCommit, error)
	CodingSandbox(ctx context.Context, id string) (sandbox.Record, error)
	// TouchCodingSandboxHeartbeat records a runner heartbeat on the sandbox row.
	TouchCodingSandboxHeartbeat(ctx context.Context, sandboxID string, at time.Time) error
	// MarkSetupSecretsUsed flips the token's setup_secrets_used flag once; false when already used.
	MarkSetupSecretsUsed(ctx context.Context, token string) (bool, error)
	// ClaimRunnerEvent reserves a runner event id for a session; false when it
	// was already ingested (or is being ingested and the claim is younger than staleBefore).
	ClaimRunnerEvent(ctx context.Context, workspaceKey, sessionID, eventID string, now, staleBefore time.Time) (bool, error)
	CompleteRunnerEvent(ctx context.Context, sessionID, eventID, activityID string) error
	ReleaseRunnerEvent(ctx context.Context, sessionID, eventID string) error
	// CodingSandboxBilledSeconds and AdvanceCodingSandboxBilled implement the
	// runtime billing CAS on coding_sandboxes.billed_seconds.
	CodingSandboxBilledSeconds(ctx context.Context, sandboxID string) (int64, error)
	AdvanceCodingSandboxBilled(ctx context.Context, sandboxID string, from, to int64) (bool, error)
	// BuiltinAgentUserID is the built-in Flow application's user id ("" when not installed).
	BuiltinAgentUserID(ctx context.Context, workspaceKey string) (string, error)
}

// AnchorStore is the existing agent session protocol (application agent
// tasks), implemented by *store.SQLiteStore.
type AnchorStore interface {
	AgentTask(ctx context.Context, workspace, id string) (domain.AgentTask, error)
	AppendAgentActivity(ctx context.Context, workspace, id string, expected int64, activity domain.AgentActivity) (domain.AgentTask, error)
	UpdateAgentTaskSession(ctx context.Context, workspace, id string, mutate func(*domain.AgentTask) error) (domain.AgentTask, error)
}

// EnvironmentReader is the part of EnvironmentStore (track K) the session core reads.
type EnvironmentReader interface {
	CodingEnvironment(ctx context.Context, workspaceKey, id string) (domain.CodingEnvironment, error)
	ListCodingEnvironments(ctx context.Context, workspaceKey string, includeArchived bool) ([]domain.CodingEnvironment, error)
	CodingEnvironmentSecretValues(ctx context.Context, workspaceKey, environmentID string) ([]domain.CodingEnvVar, error)
}

// SnapshotResolver picks the ready snapshot a session restores (track K's
// config hash); ok is false when the session must run setup inline.
type SnapshotResolver interface {
	ResolveCodingSnapshot(ctx context.Context, workspaceKey string, environment domain.CodingEnvironment) (snapshot domain.CodingEnvironmentSnapshot, ok bool, err error)
}

// PullRequestStore is the part of ChangeRequestStore (track D) the session core writes.
type PullRequestStore interface {
	UpsertSessionPullRequest(ctx context.Context, workspaceKey string, pullRequest domain.SessionPullRequest) error
	SessionPullRequests(ctx context.Context, workspaceKey, sessionID string) ([]domain.SessionPullRequest, error)
}

// ArtifactReader lists session artifacts (track I).
type ArtifactReader interface {
	CodingArtifacts(ctx context.Context, workspaceKey, sessionID string) ([]domain.CodingArtifact, error)
}

// SigningKeys checks whether a user has signing key material (track E).
type SigningKeys interface {
	SigningKeyMaterial(ctx context.Context, userID string) (format string, privateKey []byte, err error)
}

// Signer signs commit payloads with the session creator's key (track E,
// api/internal/coding/signing.go). The runtime's Sign hook calls it.
type Signer interface {
	SignCommit(ctx context.Context, workspaceKey, userID, format string, payload []byte) (string, error)
}

// WorkspaceInfo is what the session core reads about a workspace.
type WorkspaceInfo struct {
	Settings  domain.WorkspaceSettings
	CreatedAt time.Time
	// Location is the zone spend-limit resets are computed in (UTC when nil).
	Location *time.Location
}

// WorkspaceSource resolves workspace settings.
type WorkspaceSource interface {
	CodingWorkspace(ctx context.Context, workspaceKey string) (WorkspaceInfo, error)
}

// IssueContext is the issue a session works on (session core, track B)
// and what triggers know about it (track L).
type IssueContext struct {
	ID          string
	TeamID      string
	TeamKey     string
	TeamName    string
	Identifier  string
	Title       string
	Description string
	URL         string
	Labels      []string
	// LinkedRepositories are repositories the issue references (triggers
	// pick the environment from them).
	LinkedRepositories []LinkedRepository
	// Origin says whether the issue (or the triggering comment) came from an
	// external source.
	Origin ExternalOrigin
	// Guidance is the team's agent guidance, when any.
	Guidance string
}

// UserInfo is a Flow user as a commit author.
type UserInfo struct {
	ID    string
	Name  string
	Email string
	// Login is the user's linked code host username, when known.
	Login string
	// SigningFormat / SigningPublicKey describe the user's commit signing key.
	SigningFormat    string
	SigningPublicKey string
}

// MemberAccount is a member's linked code host account, without secrets.
type MemberAccount struct {
	CodeHost   string
	HostName   string
	Login      string
	ExternalID string
	// Name is the member's display name in the workspace; Email the commit
	// email for the host (GitHub noreply, GitLab commit email or noreply).
	Name  string
	Email string
	// Username is the member's Flow username (their branch prefix).
	Username string
}

// MemberCodeHost resolves members' own linked GitHub / GitLab accounts
// (implemented by the server). Every method is keyed by the one member a
// session belongs to; implementations never return another member's token.
type MemberCodeHost interface {
	// MemberAccount is the member's linked account on the host (ok false
	// when there is none or it needs a reconnect).
	MemberAccount(ctx context.Context, workspaceKey, userID, codeHost, hostName string) (MemberAccount, bool, error)
	// MemberGitCredential is the member's credential for the repository,
	// used only server side by Flow's git relay (git_relay.go), which adds it
	// to the session's own smart-HTTP requests; it is never served to a
	// sandbox, the CLI env or the bootstrap. It may be long-lived (a GitLab
	// personal access token or a non-expiring OAuth token; ExpiresAt is then
	// zero). It returns codehost.ErrUserNotLinked when the member has no
	// usable account on the host.
	MemberGitCredential(ctx context.Context, workspaceKey, userID string, repo codehost.RepoRef) (codehost.Credential, error)
	// MemberActor is the member's token for Flow's own call that opens the
	// PR/MR (server side; never sent to a sandbox). nil when not linked.
	MemberActor(ctx context.Context, workspaceKey, userID string, repo codehost.RepoRef) (*codehost.ActingUser, error)
}

// Directory resolves issues and users (implemented by the server).
type Directory interface {
	CodingIssue(ctx context.Context, workspaceKey, issueID string) (IssueContext, error)
	CodingUser(ctx context.Context, workspaceKey, userID string) (UserInfo, error)
}

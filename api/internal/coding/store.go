package coding

import (
	"context"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// Store errors. They alias the domain errors so that internal/store, which
// cannot import this package, returns errors callers match with errors.Is.
var (
	ErrNotFound        = domain.ErrCodingNotFound
	ErrVersionConflict = domain.ErrCodingVersionConflict
	ErrLeaseLost       = domain.ErrCodingLeaseLost
	ErrSecretExists    = domain.ErrCodingSecretExists // secrets cannot be changed once saved
	ErrRepoInUse       = domain.ErrCodingRepoInUse
	ErrTokenInvalid    = domain.ErrCodingTokenInvalid
)

// SessionFilter narrows ListCodingSessions.
type SessionFilter struct {
	IssueID   string
	CreatorID string
	States    []domain.CodingSessionState
	Source    string
	Limit     int
}

// SandboxGrant is an authenticated sandbox token.
type SandboxGrant struct {
	WorkspaceKey string
	SandboxID    string
	SessionID    string
	SnapshotID   string
	Kind         sandbox.Kind
	ExpiresAt    time.Time
	// EnvironmentID is the environment the sandbox works in (the session's,
	// or the snapshot's for snapshot and debug sandboxes); egress checks use
	// it to resolve the network mode.
	EnvironmentID string
}

// SpendQuery sums metered cost in a window.
type SpendQuery struct {
	WorkspaceKey string
	UserID       string
	LoopID       string
	Since        time.Time
}

// SessionStore persists sessions, their inbox and sandboxes (track B,
// api/internal/store/coding_sessions.go).
type SessionStore interface {
	// CreateCodingSession inserts the session and its AgentTask anchor in one
	// transaction; an existing session with the same idempotency key is returned.
	CreateCodingSession(ctx context.Context, session domain.CodingSession, anchor domain.AgentTask, idempotencyKey string) (domain.CodingSession, bool, error)
	CodingSession(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error)
	CodingSessionByAgentSession(ctx context.Context, workspaceKey, agentSessionID string) (domain.CodingSession, error)
	ListCodingSessions(ctx context.Context, workspaceKey string, filter SessionFilter) ([]domain.CodingSession, error)
	// UpdateCodingSession applies mutate under optimistic concurrency.
	UpdateCodingSession(ctx context.Context, workspaceKey, id string, mutate func(*domain.CodingSession) error) (domain.CodingSession, error)
	// LeaseCodingSessions returns non-terminal sessions due for reconciliation, leased to owner.
	LeaseCodingSessions(ctx context.Context, owner string, lease time.Duration, limit int) ([]domain.CodingSession, error)
	ReleaseCodingSession(ctx context.Context, owner, id string, nextReconcileAt time.Time) error
	CountActiveCodingSessions(ctx context.Context, workspaceKey string) (workspace, total int, err error)

	EnqueueCodingInbox(ctx context.Context, workspaceKey, sessionID string, item InboxItem) (int64, error)
	CodingInbox(ctx context.Context, sessionID string, after int64) ([]InboxItem, error)

	CreateCodingSandbox(ctx context.Context, record sandbox.Record) error
	CodingSandboxes(ctx context.Context, workspaceKey, sessionID string) ([]sandbox.Record, error)
	SetCodingSandboxDesired(ctx context.Context, id string, desired sandbox.Desired) error
	BindWarmSandbox(ctx context.Context, size, region, sessionID string) (sandbox.Record, error)
	AuthenticateSandboxToken(ctx context.Context, token string) (SandboxGrant, error)
	RevokeSandboxTokens(ctx context.Context, sandboxID string) error
	// RegisterGitRelayPlaceholder stores a placeholder derived from a live
	// sandbox token, bound to its sandbox and expiring and revoked with it;
	// AuthenticateGitRelayPlaceholder resolves one (git_relay.go). Sandbox
	// token checks never accept a placeholder.
	RegisterGitRelayPlaceholder(ctx context.Context, token, placeholder string) error
	AuthenticateGitRelayPlaceholder(ctx context.Context, placeholder string) (SandboxGrant, error)

	AddCodingCommits(ctx context.Context, sessionID string, commits []domain.CodingCommit) error
}

// EnvironmentStore persists environments, secrets and snapshots (track K,
// api/internal/store/coding_environments.go).
type EnvironmentStore interface {
	CreateCodingEnvironment(ctx context.Context, environment domain.CodingEnvironment) (domain.CodingEnvironment, error)
	CodingEnvironment(ctx context.Context, workspaceKey, id string) (domain.CodingEnvironment, error)
	CodingEnvironmentForRepository(ctx context.Context, workspaceKey, codeHost, hostName, repository string) (domain.CodingEnvironment, error)
	ListCodingEnvironments(ctx context.Context, workspaceKey string, includeArchived bool) ([]domain.CodingEnvironment, error)
	UpdateCodingEnvironment(ctx context.Context, workspaceKey, id string, version int64, mutate func(*domain.CodingEnvironment) error) (domain.CodingEnvironment, error)
	SetCodingEnvironmentArchived(ctx context.Context, workspaceKey, id string, archived bool) (domain.CodingEnvironment, error)

	// PutCodingEnvironmentSecret encrypts and stores a new secret (ErrSecretExists if present).
	PutCodingEnvironmentSecret(ctx context.Context, workspaceKey, environmentID, name, value, createdBy string) error
	DeleteCodingEnvironmentSecret(ctx context.Context, workspaceKey, environmentID, name string) error
	// CodingEnvironmentSecretValues decrypts secrets for a sandbox's setup phase only.
	CodingEnvironmentSecretValues(ctx context.Context, workspaceKey, environmentID string) ([]domain.CodingEnvVar, error)

	CreateCodingSnapshot(ctx context.Context, workspaceKey string, snapshot domain.CodingEnvironmentSnapshot) error
	CodingSnapshot(ctx context.Context, workspaceKey, id string) (domain.CodingEnvironmentSnapshot, error)
	ListCodingSnapshots(ctx context.Context, workspaceKey, environmentID string, limit int) ([]domain.CodingEnvironmentSnapshot, error)
	UpdateCodingSnapshot(ctx context.Context, workspaceKey, id string, mutate func(*domain.CodingEnvironmentSnapshot) error) (domain.CodingEnvironmentSnapshot, error)
	LatestReadyCodingSnapshot(ctx context.Context, workspaceKey, environmentID, configHash string) (domain.CodingEnvironmentSnapshot, error)
	LeaseCodingSnapshots(ctx context.Context, owner string, lease time.Duration, limit int) ([]domain.CodingEnvironmentSnapshot, error)
}

// ChangeRequestStore links PRs/MRs to sessions (tracks D/J/L,
// api/internal/store/coding_pull_requests.go).
type ChangeRequestStore interface {
	UpsertSessionPullRequest(ctx context.Context, workspaceKey string, pullRequest domain.SessionPullRequest) error
	SessionPullRequests(ctx context.Context, workspaceKey, sessionID string) ([]domain.SessionPullRequest, error)
	// SessionsForChangeRequest finds sessions linked to a PR/MR (webhook routing).
	SessionsForChangeRequest(ctx context.Context, codeHost, hostName, repository string, number int) ([]domain.CodingSession, error)
	UpsertCodeHostInstallation(ctx context.Context, installation domain.CodeHostInstallation) error
	DeleteCodeHostInstallation(ctx context.Context, codeHost, hostName, installationID string) error
}

// ArtifactStore records session artifacts (track I, api/internal/store/coding_artifacts.go).
type ArtifactStore interface {
	AddCodingArtifact(ctx context.Context, workspaceKey string, artifact domain.CodingArtifact, objectKey string) error
	CodingArtifacts(ctx context.Context, workspaceKey, sessionID string) ([]domain.CodingArtifact, error)
}

// UsageStore meters spend (track B, api/internal/store/coding_usage.go). The
// LLM proxy (track E) and the sandbox reconciler (track A) write records.
type UsageStore interface {
	RecordCodingUsage(ctx context.Context, record domain.CodingUsageRecord) error
	CodingSpendMicros(ctx context.Context, query SpendQuery) (int64, error)
	CodingUsage(ctx context.Context, workspaceKey string, since time.Time) ([]domain.CodingUsageRecord, error)
}

// SigningKeyStore keeps users' encrypted commit-signing private keys (track E,
// api/internal/store/coding_signing_keys.go). Only the API process decrypts them.
type SigningKeyStore interface {
	PutSigningKeyMaterial(ctx context.Context, userID string, format string, privateKey []byte) error
	SigningKeyMaterial(ctx context.Context, userID string) (format string, privateKey []byte, err error)
	DeleteSigningKeyMaterial(ctx context.Context, userID string) error
}

// Store is everything the coding layer persists; *store.SQLiteStore
// implements it once every track has landed (track B adds the compile-time
// assertion in api/cmd/server/coding_runtime.go).
type Store interface {
	SessionStore
	EnvironmentStore
	ChangeRequestStore
	ArtifactStore
	UsageStore
	SigningKeyStore
	sandbox.Store
}

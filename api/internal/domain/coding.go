package domain

import (
	"errors"
	"slices"
	"time"
)

// Coding sessions (docs/coding-sessions-design.md). A coding session is the
// Flow agent writing code in a sandbox for an issue, a chat, a comment, a
// loop or a pull/merge request, and opening a draft PR/MR. Each session is
// anchored to one AgentTask (application_agent_tasks) of the built-in Flow
// agent, so its activities, plan and external URLs use the existing agent
// session protocol and UI; the coding-specific state lives in the types below.

// Coding store errors. internal/coding re-exports them (coding.ErrNotFound,
// …) because internal/store cannot import internal/coding (config imports
// store), yet store methods must return errors coding callers can match with
// errors.Is. Added by track K (additive). These are the ONE set of coding
// store errors: internal/store and internal/coding both alias them, so no
// layer translates errors.
var (
	ErrCodingNotFound        = errors.New("coding: not found")
	ErrCodingVersionConflict = errors.New("coding: version conflict")
	ErrCodingLeaseLost       = errors.New("coding: lease lost")
	ErrCodingSecretExists    = errors.New("coding: secret already exists")
	ErrCodingRepoInUse       = errors.New("coding: repository already has an active environment")
	ErrCodingTokenInvalid    = errors.New("coding: sandbox token is invalid, expired or revoked")
	ErrCodingInvalidInput    = errors.New("coding: invalid input")
	// ErrCodingAnchorRevoked refuses a session on an ended AgentTask whose
	// access was withdrawn (a system cancel, the delegation removed, the
	// application uninstalled or deactivated): reusing it would restore it.
	ErrCodingAnchorRevoked = errors.New("coding: the session's agent task was revoked")
)

// CodingSessionState is the user-facing state, matching Linear's
// AgentSession states. UI labels: pending=Starting, active=Working,
// stopping=Stopping…, complete=Finished, awaitingInput=Needs input,
// error=Errored, stale=Stopped.
type CodingSessionState string

const (
	CodingSessionPending       CodingSessionState = "pending"
	CodingSessionActive        CodingSessionState = "active"
	CodingSessionStopping      CodingSessionState = "stopping"
	CodingSessionComplete      CodingSessionState = "complete"
	CodingSessionAwaitingInput CodingSessionState = "awaitingInput"
	CodingSessionError         CodingSessionState = "error"
	CodingSessionStale         CodingSessionState = "stale"
)

// CodingSessionPhase is the reconciler's fine-grained position inside a
// state (design §4). Each phase has a timeout, a retry cap and the error code
// used when it is exhausted (coding.PhasePolicies).
type CodingSessionPhase string

const (
	CodingPhaseAwaitingApproval CodingSessionPhase = "awaitingApproval" // trusted external sources gate (state awaitingInput)
	CodingPhaseQueued           CodingSessionPhase = "queued"           // waiting for capacity / spend check (pending)
	CodingPhaseProvisioning     CodingSessionPhase = "provisioning"     // credentials, snapshot resolution, volume (pending)
	CodingPhaseStarting         CodingSessionPhase = "starting"         // pod scheduled, runner bootstrapping (pending)
	CodingPhaseSetup            CodingSessionPhase = "setup"            // restore/clone, mise install, prepare script (active)
	CodingPhaseRunning          CodingSessionPhase = "running"          // a harness turn is executing (active)
	CodingPhaseAwaitingUser     CodingSessionPhase = "awaitingUser"     // the agent asked a question (awaitingInput)
	CodingPhasePublishing       CodingSessionPhase = "publishing"       // commit, push, draft PR/MR (active)
	CodingPhaseIdle             CodingSessionPhase = "idle"             // turn finished, sandbox kept warm for follow-ups (complete)
	CodingPhaseStopping         CodingSessionPhase = "stopping"         // stop requested; sandbox draining (stopping)
	CodingPhaseDone             CodingSessionPhase = "done"             // terminal: complete / stale / error
)

// CodingErrorCode is an explicit, stable error reason. The first group are
// Linear's codes; the rest are Flow's sandbox and runtime codes.
type CodingErrorCode string

const (
	CodingErrNoCodeAccess             CodingErrorCode = "noCodeAccess"
	CodingErrNoGitHubConnection       CodingErrorCode = "noGitHubConnection"
	CodingErrNoGitLabConnection       CodingErrorCode = "noGitLabConnection"
	CodingErrMissingCommitSigningKey  CodingErrorCode = "missingCommitSigningKey"
	CodingErrNoPolicyRepos            CodingErrorCode = "noPolicyRepos"
	CodingErrPersonalAccessDenied     CodingErrorCode = "personalAccessDenied"
	CodingErrGitHubTransientError     CodingErrorCode = "githubTransientError"
	CodingErrGitLabTransientError     CodingErrorCode = "gitlabTransientError"
	CodingErrRepoNotInPolicy          CodingErrorCode = "repoNotInPolicy"
	CodingErrRequiresGitHubConnection CodingErrorCode = "requiresGithubConnection"
	CodingErrRequiresGitLabConnection CodingErrorCode = "requiresGitlabConnection"
	CodingErrQuotaExceeded            CodingErrorCode = "quotaExceeded"
	CodingErrPermissionDenied         CodingErrorCode = "permissionDenied"

	CodingErrApprovalExpired     CodingErrorCode = "approvalExpired"
	CodingErrCapacityUnavailable CodingErrorCode = "capacityUnavailable"
	CodingErrSandboxDisabled     CodingErrorCode = "sandboxDisabled"
	CodingErrSandboxStartFailed  CodingErrorCode = "sandboxStartFailed"
	CodingErrImagePullFailed     CodingErrorCode = "imagePullFailed"
	CodingErrUnschedulable       CodingErrorCode = "unschedulable"
	CodingErrEnvironmentSetup    CodingErrorCode = "environmentSetupFailed"
	CodingErrSnapshotFailed      CodingErrorCode = "snapshotFailed"
	CodingErrRunnerLost          CodingErrorCode = "runnerLost"
	CodingErrSandboxLost         CodingErrorCode = "sandboxLost"
	CodingErrTurnTimeout         CodingErrorCode = "turnTimeout"
	CodingErrSessionTimeout      CodingErrorCode = "sessionTimeout"
	CodingErrHarnessFailed       CodingErrorCode = "harnessFailed"
	CodingErrModelUnavailable    CodingErrorCode = "modelUnavailable"
	CodingErrPushFailed          CodingErrorCode = "pushFailed"
	CodingErrPullRequestFailed   CodingErrorCode = "pullRequestFailed"
	CodingErrSigningFailed       CodingErrorCode = "signingFailed"
	CodingErrInternal            CodingErrorCode = "internal"
)

// Code hosts.
const (
	CodeHostGitHub = "github"
	CodeHostGitLab = "gitlab"
)

// Harnesses (Linear: Codex default, Claude Code, Open source).
const (
	CodingHarnessCodex      = "codex"
	CodingHarnessClaudeCode = "claudeCode"
	CodingHarnessOpenSource = "openSource"
)

// CodingModelAuto lets Flow route the model per harness (FLOW_CODING_MODELS_*).
const CodingModelAuto = "auto"

// Sandbox sizes: small 1 vCPU/8 GB, medium 2/16, large 4/32 (overridable).
const (
	CodingSizeSmall  = "small"
	CodingSizeMedium = "medium"
	CodingSizeLarge  = "large"
)

// Dependency management modes.
const (
	CodingDependenciesCustomOnly         = "custom-only"
	CodingDependenciesCustomAndGenerated = "custom-and-generated"
)

// Environment network modes (enforced by the egress proxy ACL).
const (
	CodingNetworkDefault  = "default"  // the global allowlist
	CodingNetworkExtended = "extended" // global allowlist plus CodingEnvironment.AllowedDomains
	CodingNetworkOpen     = "open"     // any public host (private ranges and metadata stay blocked)
	CodingNetworkNone     = "none"     // Flow and the code host only
)

// CodingMiseFile is a file placed in the sandbox home directory.
type CodingMiseFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// CodingEnvVar is a plain (non-secret) environment variable.
type CodingEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CodingMiseConfig is the environment's toolchain config.
type CodingMiseConfig struct {
	// Tools maps a mise tool to its version, e.g. {"node":"22","go":"1.26"}.
	Tools                map[string]string `json:"tools,omitempty"`
	Files                []CodingMiseFile  `json:"files,omitempty"`
	EnvironmentVariables []CodingEnvVar    `json:"environmentVariables,omitempty"`
}

// CodingSecretRef is a saved secret as the API returns it: the value is never
// returned (masked) and cannot be changed; delete and re-add instead.
type CodingSecretRef struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy string    `json:"createdBy,omitempty"`
}

// CodingEnvironment v2 (replaces CodingAgentEnvironment). One repository per
// environment and at most one active (not archived) environment per repo.
type CodingEnvironment struct {
	ID             string           `json:"id"`
	WorkspaceKey   string           `json:"-"`
	Name           string           `json:"name"`
	CodeHost       string           `json:"codeHost"`               // github | gitlab
	HostName       string           `json:"hostName"`               // github.com, a GHE host, gitlab.com or self-managed
	Repository     string           `json:"repository"`             // owner/name or group/subgroup/project
	RepositoryID   string           `json:"repositoryId,omitempty"` // host id (GitHub node/numeric id, GitLab project id)
	DefaultBranch  string           `json:"defaultBranch,omitempty"`
	OwnerIDs       []string         `json:"ownerIds,omitempty"`
	Harness        string           `json:"harness,omitempty"` // "" = workspace default
	Model          string           `json:"model,omitempty"`   // "" = workspace default
	Network        string           `json:"network,omitempty"` // default | extended | open | none
	AllowedDomains []string         `json:"allowedDomains,omitempty"`
	SandboxSize    string           `json:"sandboxSize,omitempty"` // small | medium | large
	Mise           CodingMiseConfig `json:"mise"`
	PrepareScript  string           `json:"prepareScript,omitempty"`
	CustomGuidance string           `json:"customGuidance,omitempty"`
	// DependencyManagement: custom-only | custom-and-generated (default).
	DependencyManagement string            `json:"dependencyManagement,omitempty"`
	Secrets              []CodingSecretRef `json:"secrets,omitempty"`
	// PendingProposal is the latest "Configure with agent" / "Fix with agent"
	// result, applied or discarded by a user.
	PendingProposal *CodingEnvironmentProposal `json:"pendingProposal,omitempty"`
	// LatestSnapshot is the newest snapshot of any status (for the editor).
	LatestSnapshot *CodingEnvironmentSnapshot `json:"latestSnapshot,omitempty"`
	ArchivedAt     *time.Time                 `json:"archivedAt,omitempty"`
	CreatedBy      string                     `json:"createdBy,omitempty"`
	Version        int64                      `json:"version"`
	CreatedAt      time.Time                  `json:"createdAt"`
	UpdatedAt      time.Time                  `json:"updatedAt"`
}

// CodingEnvironmentProposal is an agent's proposed environment change. Only
// non-secret fields can be proposed; secrets are listed by name for the user
// to add.
type CodingEnvironmentProposal struct {
	SessionID            string            `json:"sessionId"`
	Mise                 *CodingMiseConfig `json:"mise,omitempty"`
	PrepareScript        *string           `json:"prepareScript,omitempty"`
	CustomGuidance       *string           `json:"customGuidance,omitempty"`
	DependencyManagement *string           `json:"dependencyManagement,omitempty"`
	SandboxSize          *string           `json:"sandboxSize,omitempty"`
	MissingSecrets       []string          `json:"missingSecrets,omitempty"`
	Explanation          string            `json:"explanation"`
	// Verified is true when the agent ran the proposed setup successfully in its sandbox.
	Verified  bool      `json:"verified"`
	CreatedAt time.Time `json:"createdAt"`
}

// Snapshot statuses.
const (
	CodingSnapshotQueued     = "queued"
	CodingSnapshotBuilding   = "building"
	CodingSnapshotReady      = "ready"
	CodingSnapshotFailed     = "failed"
	CodingSnapshotCanceled   = "canceled"
	CodingSnapshotExpired    = "expired"
	CodingSnapshotSuperseded = "superseded"
)

// CodingEnvironmentSnapshot is a prepared workspace: the repo at BaseSHA after
// mise install and the prepare script, stored as a VolumeSnapshot or an
// archive in Flow's object store (FLOW_CODING_SNAPSHOT_STRATEGY).
type CodingEnvironmentSnapshot struct {
	ID            string          `json:"id"`
	EnvironmentID string          `json:"environmentId"`
	Status        string          `json:"status"`
	Strategy      string          `json:"strategy"`   // volumesnapshot | archive
	ConfigHash    string          `json:"configHash"` // hash of the env fields that invalidate a snapshot
	Image         string          `json:"image"`
	BaseRef       string          `json:"baseRef"`
	BaseSHA       string          `json:"baseSha,omitempty"`
	StorageRef    string          `json:"-"` // VolumeSnapshot name or object key; never exposed
	SizeBytes     int64           `json:"sizeBytes,omitempty"`
	SandboxID     string          `json:"sandboxId,omitempty"`
	ErrorCode     CodingErrorCode `json:"errorCode,omitempty"`
	Error         string          `json:"error,omitempty"`
	RequestedBy   string          `json:"requestedBy,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
	StartedAt     *time.Time      `json:"startedAt,omitempty"`
	FinishedAt    *time.Time      `json:"finishedAt,omitempty"`
	ExpiresAt     *time.Time      `json:"expiresAt,omitempty"`
}

// Session sources (what started it).
const (
	CodingSourceIssue            = "issue"            // delegation or "ask in a comment"
	CodingSourceAgentChat        = "agentChat"        // Flow Agent chat
	CodingSourceComment          = "comment"          // @mention in an issue comment
	CodingSourceLoop             = "loop"             // a Loop's "Start a coding session" action
	CodingSourcePullRequest      = "pullRequest"      // from a PR/MR, PR comment, "fix this PR"
	CodingSourceEnvironmentSetup = "environmentSetup" // "Configure with agent" / "Fix with agent"
	CodingSourceExternal         = "external"         // an external source (trusted sources gate)
)

// CodingSandboxRecord is one sandbox of a session (Linear sandboxes[]).
type CodingSandboxRecord struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"sessionId,omitempty"`
	Kind       string          `json:"kind"`   // session | snapshot | debug | warm
	Status     string          `json:"status"` // requested | creating | running | stopping | stopped | failed | lost
	Region     string          `json:"region,omitempty"`
	Size       string          `json:"size,omitempty"`
	Repository string          `json:"repository,omitempty"`
	BranchName string          `json:"branchName,omitempty"`
	BaseRef    string          `json:"baseRef,omitempty"`
	LogsURL    string          `json:"logsUrl,omitempty"`
	ErrorCode  CodingErrorCode `json:"errorCode,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
	StartedAt  *time.Time      `json:"startedAt,omitempty"`
	EndedAt    *time.Time      `json:"endedAt,omitempty"`
}

// SessionPullRequest links a PR/MR to a session (agentSessionToPullRequests).
type SessionPullRequest struct {
	SessionID  string `json:"sessionId"`
	CodeHost   string `json:"codeHost"`
	HostName   string `json:"hostName"`
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	ExternalID string `json:"externalId,omitempty"`
	URL        string `json:"url"`
	Title      string `json:"title,omitempty"`
	State      string `json:"state"` // open | closed | merged
	Draft      bool   `json:"draft"`
	HeadBranch string `json:"headBranch"`
	BaseBranch string `json:"baseBranch"`
	HeadSHA    string `json:"headSha,omitempty"`
	ReviewID   string `json:"reviewId,omitempty"` // the matching CodeReview, when synced
	// OpenedAs is the host login the PR/MR was opened as; OpenedVia is
	// "member" (the session creator's linked account) or "workspace" (the
	// workspace GitHub App / GitLab connection).
	OpenedAs  string    `json:"openedAs,omitempty"`
	OpenedVia string    `json:"openedVia,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CodingPushIdentity says whose code host identity a session pushes and
// opens its PR/MR with: "member" when the creator linked an account on the
// session's host, otherwise "workspace" (the App / connection token).
type CodingPushIdentity struct {
	Via      string `json:"via"`
	CodeHost string `json:"codeHost,omitempty"`
	HostName string `json:"hostName,omitempty"`
	Login    string `json:"login,omitempty"`
}

// CodeHostAccount is a member's personal GitHub or GitLab account linked in
// Settings › Account › Connected accounts. Tokens are never part of it; they
// live encrypted in the connector secret store.
type CodeHostAccount struct {
	ID             string `json:"id"`
	UserID         string `json:"-"`
	CodeHost       string `json:"codeHost"`
	HostName       string `json:"hostName"`
	BaseURL        string `json:"baseUrl"`
	ExternalUserID string `json:"externalUserId"`
	Login          string `json:"login"`
	// Email is the commit email the host reported (GitLab commit_email /
	// public email), when any.
	Email    string   `json:"-"`
	AuthType string   `json:"authType"` // githubApp | oauth | pat
	Scopes   []string `json:"scopes"`
	Status   string   `json:"status"` // active | reconnect
	// StatusReason explains a "reconnect" status.
	StatusReason string `json:"statusReason,omitempty"`
	// WorkspaceKey / ConnectionID: the workspace GitLab connection a GitLab
	// account was linked through (its OAuth application refreshes the token).
	WorkspaceKey   string     `json:"-"`
	ConnectionID   string     `json:"instanceId,omitempty"`
	TokenExpiresAt *time.Time `json:"tokenExpiresAt,omitempty"`
	LinkedAt       time.Time  `json:"linkedAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// Code host account auth types and statuses.
const (
	CodeHostAuthGitHubApp      = "githubApp"
	CodeHostAuthOAuth          = "oauth"
	CodeHostAuthPAT            = "pat"
	CodeHostAccountActive      = "active"
	CodeHostAccountReconnect   = "reconnect"
	CodeHostAccountActorMember = "member"
	CodeHostAccountActorBot    = "workspace"
)

// CodingCommit is a commit the runner pushed, keyed to the turn's activity.
type CodingCommit struct {
	SHA            string    `json:"sha"`
	Branch         string    `json:"branch"`
	TurnActivityID string    `json:"turnActivityId,omitempty"`
	Message        string    `json:"message,omitempty"`
	Signed         bool      `json:"signed"`
	CreatedAt      time.Time `json:"createdAt"`
}

// CodingDiffSummary is the per-file change summary of a session.
type CodingDiffSummary struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"` // added | modified | deleted | renamed
}

// CodingArtifact is a file the session produced (browser screenshots, logs).
type CodingArtifact struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"sessionId"`
	Kind        string    `json:"kind"`  // screenshot | video | log
	Label       string    `json:"label"` // before | after | free text
	URL         string    `json:"url"`
	ContentType string    `json:"contentType"`
	SizeBytes   int64     `json:"sizeBytes"`
	CreatedAt   time.Time `json:"createdAt"`
	// Track I additions (browser testing): the turn and action activity the
	// artifact belongs to, the flow name that pairs a "before" shot with its
	// "after" shot, the stable authenticated URL used by the web app, and the
	// expiry of URL when it is a signed link.
	TurnID     string     `json:"turnId,omitempty"`
	ActivityID string     `json:"activityId,omitempty"`
	Group      string     `json:"group,omitempty"`
	ContentURL string     `json:"contentUrl,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
}

// CodingSession is the coding part of an agent session.
type CodingSession struct {
	ID             string             `json:"id"`
	WorkspaceKey   string             `json:"-"`
	AgentSessionID string             `json:"agentSessionId"` // the AgentTask anchor
	IssueID        string             `json:"issueId,omitempty"`
	EnvironmentID  string             `json:"codingEnvironmentId,omitempty"`
	SnapshotID     string             `json:"snapshotId,omitempty"`
	CreatorID      string             `json:"creatorId"`
	Source         string             `json:"source"`
	SourceID       string             `json:"sourceId,omitempty"` // comment, loop, chat or PR id
	State          CodingSessionState `json:"state"`
	Phase          CodingSessionPhase `json:"phase"`
	Harness        string             `json:"harness"`
	ModelSelection string             `json:"modelSelection"`  // what was asked (auto or an id)
	Model          string             `json:"model,omitempty"` // what Auto resolved to
	// ModelReason is why Auto routed the session to Model; EscalationModel is
	// the next more capable catalog model ("Start over with <model>").
	ModelReason     string              `json:"modelReason,omitempty"`
	EscalationModel string              `json:"escalationModel,omitempty"`
	ReasoningEffort string              `json:"reasoningEffort,omitempty"`
	Region          string              `json:"region,omitempty"`
	DisplayTitle    string              `json:"displayTitle,omitempty"`
	Summary         string              `json:"summary,omitempty"`
	Plan            []AgentPlanStep     `json:"plan,omitempty"`
	ExternalURLs    []AgentExternalURL  `json:"externalUrls,omitempty"`
	Diffs           []CodingDiffSummary `json:"diffs,omitempty"`
	CodeHost        string              `json:"codeHost,omitempty"`
	Repository      string              `json:"repository,omitempty"`
	BranchName      string              `json:"branchName,omitempty"`
	BaseRef         string              `json:"baseRef,omitempty"`
	// PushIdentity is whose host identity the session pushes and opens its
	// PR/MR with (set when the session is created).
	PushIdentity            *CodingPushIdentity       `json:"pushIdentity,omitempty"`
	PullRequests            []SessionPullRequest      `json:"pullRequests,omitempty"`
	LatestPullRequest       *SessionPullRequest       `json:"latestPullRequest,omitempty"`
	CommitsByTurnActivityID map[string][]CodingCommit `json:"commitsByTurnActivityId,omitempty"`
	Sandboxes               []CodingSandboxRecord     `json:"sandboxes,omitempty"`
	Artifacts               []CodingArtifact          `json:"artifacts,omitempty"`
	ErrorCode               CodingErrorCode           `json:"errorCode,omitempty"`
	Error                   string                    `json:"error,omitempty"`
	// ApprovalState is "", "required", "approved" or "rejected" (trusted external sources gate).
	ApprovalState string     `json:"approvalState,omitempty"`
	ApprovedBy    string     `json:"approvedBy,omitempty"`
	CostMicros    int64      `json:"costMicros"`
	Version       int64      `json:"version"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	StartedAt     *time.Time `json:"startedAt,omitempty"`
	EndedAt       *time.Time `json:"endedAt,omitempty"`

	// Reconciler bookkeeping (track B). The indexed copies live in
	// coding_sessions columns; the document keeps them so a session read
	// from data alone is complete.
	PhaseStartedAt time.Time  `json:"phaseStartedAt"`
	PhaseDeadline  *time.Time `json:"phaseDeadline,omitempty"`
	// Attempts counts retries in the current phase; SandboxAttempts counts
	// sandbox starts (a failed start retries with a new sandbox id).
	Attempts        int    `json:"attempts,omitempty"`
	SandboxAttempts int    `json:"sandboxAttempts,omitempty"`
	StopRequested   bool   `json:"stopRequested,omitempty"`
	StoppedBy       string `json:"stoppedBy,omitempty"`
	// EnvironmentCandidates are the trigger's relevance-ranked environment
	// ids for an ambiguous repository (track L's suggestions, best first);
	// PreferredEnvironmentID is the clear leader among them, if any. The
	// provisioning select elicitation lists and preselects them in that order.
	EnvironmentCandidates  []string `json:"environmentCandidates,omitempty"`
	PreferredEnvironmentID string   `json:"preferredEnvironmentId,omitempty"`
	// LoopID attributes spend to a Loop (loopLimit / loopOverrides).
	LoopID      string `json:"loopId,omitempty"`
	SandboxSize string `json:"sandboxSize,omitempty"`
	// SandboxID is the session's current sandbox.
	SandboxID string `json:"sandboxId,omitempty"`
	// CurrentTurnID is the turn the runner works on; LastTurnID the last one that completed.
	CurrentTurnID string     `json:"currentTurnId,omitempty"`
	LastTurnID    string     `json:"lastTurnId,omitempty"`
	TurnStartedAt *time.Time `json:"turnStartedAt,omitempty"`
	// CurrentStep is the latest tool step of the running turn (the issue
	// status row's live line); it belongs to CurrentStep.TurnID.
	CurrentStep *CodingSessionStep `json:"currentStep,omitempty"`
	// ArchivedAt is set when the session is archived (its pull request was
	// closed without merging); archived sessions leave the issue's activity.
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
	// RunningMillis is the cumulative time spent running turns (sessionTimeout).
	RunningMillis   int64  `json:"runningMillis,omitempty"`
	HarnessResumeID string `json:"harnessResumeId,omitempty"`
	HeadSHA         string `json:"headSha,omitempty"`
	// Pushed is set by a turn that pushed commits; publishing clears it.
	Pushed         bool   `json:"pushed,omitempty"`
	ExistingBranch bool   `json:"existingBranch,omitempty"`
	HostName       string `json:"hostName,omitempty"`
	// SigningFormat / SigningPublicKey are resolved at provisioning when
	// signed commits are required (track E's commit signing).
	SigningFormat    string `json:"signingFormat,omitempty"`
	SigningPublicKey string `json:"signingPublicKey,omitempty"`
	// SubscriberIDs subscribed to the session ("Subscribe to session") and
	// UnsubscribedIDs opted out; the creator is subscribed unless they opt
	// out. Subscribers hear about completion, questions and errors.
	SubscriberIDs   []string `json:"subscriberIds,omitempty"`
	UnsubscribedIDs []string `json:"unsubscribedIds,omitempty"`
	// NextReconcileAt is when the reconciler looks at the session again.
	NextReconcileAt time.Time `json:"-"`
}

// CodingSessionStep is one tool step of a turn: the tool ("Bash", "Edit")
// and its argument (the command, the file).
type CodingSessionStep struct {
	TurnID    string    `json:"turnId,omitempty"`
	Action    string    `json:"action"`
	Parameter string    `json:"parameter,omitempty"`
	At        time.Time `json:"at"`
}

// CodingSessionSubscribed reports whether the user gets the session's
// notifications: an explicit subscriber, or the creator who did not opt out.
func CodingSessionSubscribed(session CodingSession, userID string) bool {
	if userID == "" || slices.Contains(session.UnsubscribedIDs, userID) {
		return false
	}
	return userID == session.CreatorID || slices.Contains(session.SubscriberIDs, userID)
}

// CodingUsageRecord is one metered charge: model tokens through the LLM
// proxy, or sandbox runtime.
type CodingUsageRecord struct {
	ID               string    `json:"id"`
	WorkspaceKey     string    `json:"-"`
	SessionID        string    `json:"sessionId,omitempty"`
	SandboxID        string    `json:"sandboxId,omitempty"`
	UserID           string    `json:"userId,omitempty"`
	LoopID           string    `json:"loopId,omitempty"`
	Kind             string    `json:"kind"` // llm | runtime
	Model            string    `json:"model,omitempty"`
	InputTokens      int64     `json:"inputTokens,omitempty"`
	OutputTokens     int64     `json:"outputTokens,omitempty"`
	CacheReadTokens  int64     `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int64     `json:"cacheWriteTokens,omitempty"`
	RuntimeSeconds   int64     `json:"runtimeSeconds,omitempty"`
	Size             string    `json:"size,omitempty"`
	CostMicros       int64     `json:"costMicros"`
	CreatedAt        time.Time `json:"createdAt"`
}

// CodeHostInstallation is a GitHub App installation (or a GitLab connection
// used as a code host) bound to a workspace.
type CodeHostInstallation struct {
	ID                  string     `json:"id"`
	WorkspaceKey        string     `json:"-"`
	CodeHost            string     `json:"codeHost"`
	HostName            string     `json:"hostName"`
	InstallationID      string     `json:"installationId"`
	AccountLogin        string     `json:"accountLogin"`
	AccountType         string     `json:"accountType,omitempty"`         // User | Organization | group
	RepositorySelection string     `json:"repositorySelection,omitempty"` // all | selected
	SuspendedAt         *time.Time `json:"suspendedAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

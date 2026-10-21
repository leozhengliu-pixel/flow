package coding

import (
	"time"

	"flow/api/internal/domain"
)

// Runner protocol: the JSON the in-sandbox runner (sandbox-runner/, track C)
// exchanges with Flow's runtime API (/internal/coding-runtime/*, track B).
// The runner authenticates with the sandbox token (prefix SandboxTokenPrefix)
// read from FLOW_TOKEN_FILE. The TypeScript mirror lives in
// sandbox-runner/src/protocol.ts and must be kept in sync with this file.

// SandboxTokenPrefix marks sandbox tokens (only their SHA-256 is stored).
const SandboxTokenPrefix = "fcs_"

// Runner modes (FLOW_RUNNER_MODE).
const (
	ModeSession  = "session"
	ModeSnapshot = "snapshot"
	ModeDebug    = "debug"
	ModeWarm     = "warm"
	ModeRestore  = "restore"
)

// BootstrapResponse is GET /internal/coding-runtime/bootstrap. A warm-pool
// sandbox calls it with ?wait=60s and gets 204 until it is bound to a session.
type BootstrapResponse struct {
	SandboxID   string             `json:"sandboxId"`
	SessionID   string             `json:"sessionId,omitempty"`
	SnapshotID  string             `json:"snapshotId,omitempty"`
	Mode        string             `json:"mode"`
	Repo        BootstrapRepo      `json:"repo"`
	Harness     BootstrapHarness   `json:"harness"`
	LLM         BootstrapLLM       `json:"llm"`
	Environment BootstrapEnv       `json:"environment"`
	Git         BootstrapGit       `json:"git"`
	Browser     BootstrapBrowser   `json:"browser"`
	Snapshot    *BootstrapSnapshot `json:"snapshot,omitempty"`
	// Restored is true when /workspace already holds a prepared snapshot.
	Restored bool `json:"restored"`
	// Turn is the first turn to run (session mode), when there is one.
	Turn   *Turn           `json:"turn,omitempty"`
	Limits BootstrapLimits `json:"limits"`
	// Guidance is the system prompt addition: workspace/team agent guidance,
	// the environment's customGuidance, issue context and browser-testing rules.
	Guidance string `json:"guidance"`
	// MCPServers are the extra MCP servers the harness may use: approved
	// workspace connectors exposed through Flow's MCP gateway. The runner
	// adds them to the harness config as streamable-HTTP servers
	// authenticated with the sandbox token; Flow forwards each call with the
	// stored credential, so neither the credential nor the server's network
	// is reachable from the sandbox.
	MCPServers []BootstrapMCPServer `json:"mcpServers,omitempty"`
}

// BootstrapMCPServer is one MCP server behind Flow's gateway.
type BootstrapMCPServer struct {
	// Name is the harness config key ([a-z0-9_-], never "flow").
	Name string `json:"name"`
	// Title is the workspace's display name for the server.
	Title string `json:"title"`
	// URL is {runtime}/internal/coding-runtime/mcp/{id}.
	URL string `json:"url"`
}

// BootstrapRepo is the repository to work on.
type BootstrapRepo struct {
	CodeHost string `json:"codeHost"`
	HostName string `json:"hostName"`
	FullName string `json:"fullName"`
	CloneURL string `json:"cloneUrl"`
	// PushURL, when set, is origin's push URL: Flow's git relay for a
	// session that pushes as its creator (git_relay.go). The runner sets it
	// with `git remote set-url --push origin`; fetches keep using CloneURL.
	PushURL    string `json:"pushUrl,omitempty"`
	BaseRef    string `json:"baseRef"`
	BaseSHA    string `json:"baseSha,omitempty"`
	BranchName string `json:"branchName"`
	// ExistingBranch is true for follow-ups on a pushed branch / PR fixes.
	ExistingBranch bool `json:"existingBranch"`
	// BranchUnconfirmed marks ExistingBranch as a guess: no push, PR/MR or
	// PR-sourced branch was ever recorded for the session. When the code host
	// definitely has no such branch the runner starts it again from the base
	// instead of failing with "branch not found".
	BranchUnconfirmed bool `json:"branchUnconfirmed,omitempty"`
	// coding sessions (track C): BranchPrefix is FLOW_CODING_BRANCH_PREFIX.
	// The runner refuses to push branches outside it unless ExistingBranch
	// (defense in depth, §19). Empty means the runner's default "flow/".
	BranchPrefix string `json:"branchPrefix,omitempty"`
}

// BootstrapHarness selects the agent CLI.
type BootstrapHarness struct {
	Name            string `json:"name"` // codex | claudeCode | openSource
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// ResumeID is the harness's own session/thread id from the previous turn.
	ResumeID string `json:"resumeId,omitempty"`
}

// BootstrapLLM points the harness at Flow's LLM proxy. The sandbox token is
// the API key (ANTHROPIC_AUTH_TOKEN / OPENAI_API_KEY); no provider key enters
// the sandbox.
type BootstrapLLM struct {
	AnthropicBaseURL  string `json:"anthropicBaseUrl"`  // {runtime}/internal/llm-proxy/anthropic
	OpenAIBaseURL     string `json:"openaiBaseUrl"`     // {runtime}/internal/llm-proxy/openai/v1
	OpenSourceBaseURL string `json:"openSourceBaseUrl"` // {runtime}/internal/llm-proxy/opensource/v1
}

// BootstrapEnv is the environment's non-secret setup.
type BootstrapEnv struct {
	EnvironmentID        string                  `json:"environmentId,omitempty"`
	Mise                 domain.CodingMiseConfig `json:"mise"`
	PrepareScript        string                  `json:"prepareScript,omitempty"`
	DependencyManagement string                  `json:"dependencyManagement"`
	// HasSecrets tells the runner to fetch setup secrets during setup only.
	HasSecrets bool `json:"hasSecrets"`
}

// BootstrapGit is the commit identity and signing setup.
type BootstrapGit struct {
	AuthorName     string `json:"authorName"`
	AuthorEmail    string `json:"authorEmail"`
	CommitterName  string `json:"committerName"`
	CommitterEmail string `json:"committerEmail"`
	// SigningFormat is "" (unsigned), "ssh" or "openpgp"; signatures are made
	// by Flow through POST /sign, the private key never enters the sandbox.
	SigningFormat    string `json:"signingFormat,omitempty"`
	SigningPublicKey string `json:"signingPublicKey,omitempty"` // ssh public key or PGP key id
	// CoAuthorTrailer is the full trailer line ("Co-Authored-By: Name <email>")
	// every commit of the session carries: the runner appends it to its own
	// commits and the sandbox's prepare-commit-msg hook to the agent's.
	CoAuthorTrailer string `json:"coAuthorTrailer,omitempty"`
}

// BootstrapBrowser configures browser testing.
type BootstrapBrowser struct {
	Enabled bool `json:"enabled"`
}

// BootstrapSnapshot is set in snapshot mode.
type BootstrapSnapshot struct {
	ID        string `json:"id"`
	Strategy  string `json:"strategy"`
	UploadURL string `json:"uploadUrl,omitempty"` // archive strategy
}

// BootstrapLimits are the runner-side timeouts.
type BootstrapLimits struct {
	TurnTimeoutSeconds  int `json:"turnTimeoutSeconds"`
	HeartbeatSeconds    int `json:"heartbeatSeconds"`
	SetupTimeoutSeconds int `json:"setupTimeoutSeconds"`
	StopGraceSeconds    int `json:"stopGraceSeconds"`
}

// Turn is one prompt for the harness.
type Turn struct {
	ID          string   `json:"id"` // the prompt activity id; commits key on it
	Prompt      string   `json:"prompt"`
	AuthorID    string   `json:"authorId,omitempty"`
	Attachments []string `json:"attachments,omitempty"` // signed URLs
	// Kind is "initial", "followUp", "prComment", "fixPr", "rebase", "lint", "test", "configureEnvironment", "fixEnvironment".
	Kind string `json:"kind"`
}

// SetupSecretsResponse is GET /internal/coding-runtime/setup-secrets. It is
// only answered while the sandbox is in its setup phase, once per sandbox.
type SetupSecretsResponse struct {
	Variables []domain.CodingEnvVar `json:"variables"`
}

// HeartbeatRequest is POST /internal/coding-runtime/heartbeat.
type HeartbeatRequest struct {
	Phase   string `json:"phase"` // setup | running | idle | publishing | snapshotting
	TurnID  string `json:"turnId,omitempty"`
	Message string `json:"message,omitempty"`
}

// HeartbeatResponse tells the runner to stop.
type HeartbeatResponse struct {
	Stop bool `json:"stop"`
}

// Runner event types (POST /internal/coding-runtime/events), mapped to
// AgentActivity types by the session layer.
const (
	EventThought     = "thought"
	EventAction      = "action"
	EventResponse    = "response"
	EventElicitation = "elicitation"
	EventError       = "error"
	EventPlan        = "plan"
	EventStatus      = "status"
	EventDiff        = "diff"
	EventUsage       = "usage" // informational; billing uses the LLM proxy's metering
)

// RunnerEvent is one normalized harness event.
type RunnerEvent struct {
	ID        string                     `json:"id"` // runner-generated, idempotency key
	TurnID    string                     `json:"turnId,omitempty"`
	Type      string                     `json:"type"`
	Ephemeral bool                       `json:"ephemeral,omitempty"`
	Body      string                     `json:"body,omitempty"`
	Action    *ActionDetail              `json:"action,omitempty"`
	Plan      []domain.AgentPlanStep     `json:"plan,omitempty"`
	Diff      []domain.CodingDiffSummary `json:"diff,omitempty"`
	Error     *ErrorDetail               `json:"error,omitempty"`
	Usage     *UsageDetail               `json:"usage,omitempty"`
	Phase     string                     `json:"phase,omitempty"` // status events
	CreatedAt time.Time                  `json:"createdAt"`
}

// ActionDetail is an action activity (tool call): "Ran command" `npm test`.
type ActionDetail struct {
	Action    string `json:"action"`
	Parameter string `json:"parameter,omitempty"`
	Result    string `json:"result,omitempty"`
	Tool      string `json:"tool,omitempty"`
	ExitCode  *int   `json:"exitCode,omitempty"`
}

// ErrorDetail is a harness error.
type ErrorDetail struct {
	Code    domain.CodingErrorCode `json:"code"`
	Message string                 `json:"message"`
}

// UsageDetail is the harness-reported usage of a turn.
type UsageDetail struct {
	Model        string  `json:"model"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CostUSD      float64 `json:"costUsd,omitempty"`
}

// EventsRequest batches events (max 100 per request).
type EventsRequest struct {
	Events []RunnerEvent `json:"events"`
}

// InboxItem is a follow-up or signal for the runner
// (GET /internal/coding-runtime/inbox?after=<seq>&wait=25s).
type InboxItem struct {
	Seq    int64          `json:"seq"`
	Kind   string         `json:"kind"` // turn | stop | signal
	Turn   *Turn          `json:"turn,omitempty"`
	Signal string         `json:"signal,omitempty"` // continue | auth | select
	Data   map[string]any `json:"data,omitempty"`
}

// InboxResponse is the long-poll answer (empty items on timeout).
type InboxResponse struct {
	Items []InboxItem `json:"items"`
}

// TurnCompleteRequest is POST /internal/coding-runtime/turns/{turnId}/complete.
type TurnCompleteRequest struct {
	Status           string                     `json:"status"` // success | error | stopped | awaitingInput
	Summary          string                     `json:"summary,omitempty"`
	DisplayTitle     string                     `json:"displayTitle,omitempty"`
	HarnessSessionID string                     `json:"harnessSessionId,omitempty"`
	Commits          []domain.CodingCommit      `json:"commits,omitempty"`
	Pushed           bool                       `json:"pushed"`
	HeadSHA          string                     `json:"headSha,omitempty"`
	Diff             []domain.CodingDiffSummary `json:"diff,omitempty"`
	Error            *ErrorDetail               `json:"error,omitempty"`
}

// CredentialRequest is POST /internal/coding-runtime/credentials (git
// credential helper "get", and gh/glab token refresh).
type CredentialRequest struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Path     string `json:"path,omitempty"`
	Purpose  string `json:"purpose"` // git | cli
	// Rejected reports a credential the code host refused (git's credential
	// helper "erase"); Fingerprint is its hex SHA-256, never the value. Flow
	// drops its current credential when it is that one and the host confirms
	// it is no longer valid, so the runner's single retry gets a fresh one.
	Rejected    bool   `json:"rejected,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// CredentialResponse is the minted short-lived credential.
type CredentialResponse struct {
	Username  string            `json:"username"`
	Password  string            `json:"password"`
	ExpiresAt time.Time         `json:"expiresAt"`
	CLIEnv    map[string]string `json:"cliEnv,omitempty"`
}

// SignRequest is POST /internal/coding-runtime/sign: the commit/tag payload
// git hands to gpg.ssh.program / gpg.program; Flow signs with the user's key.
type SignRequest struct {
	Format  string `json:"format"`  // ssh | openpgp
	Payload []byte `json:"payload"` // base64 in JSON
}

// SignResponse carries the armored signature.
type SignResponse struct {
	Signature string `json:"signature"`
}

// SnapshotLogsRequest is POST /internal/coding-runtime/snapshots/{id}/logs:
// setup output lines in order, batched by the runner (coding sessions, track C).
type SnapshotLogsRequest struct {
	Lines []string `json:"lines"`
}

// SnapshotCompleteRequest is POST /internal/coding-runtime/snapshots/{id}/complete.
type SnapshotCompleteRequest struct {
	Status    string                 `json:"status"` // ready | failed
	BaseSHA   string                 `json:"baseSha,omitempty"`
	SizeBytes int64                  `json:"sizeBytes,omitempty"`
	ErrorCode domain.CodingErrorCode `json:"errorCode,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// ArtifactResponse is POST /internal/coding-runtime/artifacts (multipart:
// file, kind, label, turnId).
type ArtifactResponse struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

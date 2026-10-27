// Mirror of api/internal/coding/protocol.go (the runner wire protocol).
// Keep the two files in sync: field names are the JSON names used by Go.

export const SANDBOX_TOKEN_PREFIX = 'fcs_'

export type RunnerMode = 'session' | 'snapshot' | 'debug' | 'warm' | 'restore'

export const RUNNER_MODES: readonly RunnerMode[] = ['session', 'snapshot', 'debug', 'warm', 'restore']

export type HarnessName = 'codex' | 'claudeCode' | 'openSource' | 'pi'

/** domain.CodingErrorCode values the runner reports. */
export type CodingErrorCode =
  | 'noCodeAccess'
  | 'quotaExceeded'
  | 'environmentSetupFailed'
  | 'snapshotFailed'
  | 'turnTimeout'
  | 'harnessFailed'
  | 'modelUnavailable'
  | 'pushFailed'
  | 'signingFailed'
  | 'internal'

export interface CodingMiseFile {
  path: string
  content: string
}

export interface CodingEnvVar {
  name: string
  value: string
}

export interface CodingMiseConfig {
  tools?: Record<string, string>
  files?: CodingMiseFile[]
  environmentVariables?: CodingEnvVar[]
}

export interface BootstrapRepo {
  codeHost: string
  hostName: string
  fullName: string
  cloneUrl: string
  /** origin's push URL: Flow's git relay for a session that pushes as its creator. */
  pushUrl?: string
  baseRef: string
  baseSha?: string
  branchName: string
  existingBranch: boolean
  /**
   * Additive: existingBranch is only an unconfirmed guess (no PR/MR and no push the session ever recorded). If origin
   * definitely has no such branch the runner starts it again instead of failing with "branch not found".
   */
  branchUnconfirmed?: boolean
  /** Additive (track C): FLOW_CODING_BRANCH_PREFIX, so the runner can refuse other branches. */
  branchPrefix?: string
}

export interface BootstrapHarness {
  name: HarnessName
  model: string
  reasoningEffort?: string
  resumeId?: string
  /**
   * Additive: the model's context window in tokens (FLOW_CODING_MODEL_INFO
   * contextWindow), sent only for Codex and Pi sessions whose model has one.
   * The Codex adapter writes it as model_context_window, the Pi adapter as
   * the model's contextWindow in models.json; absent, nothing is written.
   */
  contextWindow?: number
}

export interface BootstrapLLM {
  anthropicBaseUrl: string
  openaiBaseUrl: string
  openSourceBaseUrl: string
}

export interface BootstrapEnv {
  environmentId?: string
  mise: CodingMiseConfig
  prepareScript?: string
  dependencyManagement: string
  hasSecrets: boolean
}

export interface BootstrapGit {
  authorName: string
  authorEmail: string
  committerName: string
  committerEmail: string
  signingFormat?: '' | 'ssh' | 'openpgp'
  signingPublicKey?: string
  coAuthorTrailer?: string
}

export interface BootstrapBrowser {
  enabled: boolean
}

export interface BootstrapSnapshot {
  id: string
  strategy: string
  uploadUrl?: string
}

export interface BootstrapLimits {
  turnTimeoutSeconds: number
  heartbeatSeconds: number
  setupTimeoutSeconds: number
  stopGraceSeconds: number
}

export interface Turn {
  id: string
  prompt: string
  authorId?: string
  attachments?: string[]
  kind: string
  /** The write scope Flow decided when it queued the turn (explicit choice, rules or model). Older APIs omit it. */
  scope?: 'write' | 'noPublish' | 'readOnly'
  /** How Flow decided the scope: user, kind, rules or model. */
  scopeSource?: string
  /**
   * Commit the uncommitted changes that were already in the working tree before the turn too (the request asked
   * for it). Without it those paths are committed only when the turn changes them. Only write turns commit.
   */
  includeLeftovers?: boolean
}

export interface BootstrapResponse {
  sandboxId: string
  sessionId?: string
  snapshotId?: string
  mode: RunnerMode
  repo: BootstrapRepo
  harness: BootstrapHarness
  llm: BootstrapLLM
  environment: BootstrapEnv
  git: BootstrapGit
  browser: BootstrapBrowser
  snapshot?: BootstrapSnapshot
  restored: boolean
  turn?: Turn
  limits: BootstrapLimits
  guidance: string
  /**
   * Workspace MCP servers behind Flow's MCP gateway (streamable HTTP,
   * authenticated with the sandbox token). Flow forwards each call with the
   * server's stored credential; the sandbox never sees it.
   */
  mcpServers?: BootstrapMCPServer[]
}

export interface BootstrapMCPServer {
  /** Harness config key ([a-z0-9_-], never "flow"). */
  name: string
  title: string
  /** {runtime}/internal/coding-runtime/mcp/{id} */
  url: string
}

export interface SetupSecretsResponse {
  variables: CodingEnvVar[]
}

export type HeartbeatPhase = 'setup' | 'running' | 'idle' | 'publishing' | 'snapshotting'

export interface HeartbeatRequest {
  phase: HeartbeatPhase
  turnId?: string
  message?: string
}

export interface HeartbeatResponse {
  stop: boolean
}

export type RunnerEventType =
  | 'thought'
  | 'action'
  | 'response'
  | 'elicitation'
  | 'error'
  | 'plan'
  | 'status'
  | 'diff'
  | 'usage'

export type PlanStatus = 'pending' | 'inProgress' | 'completed' | 'canceled'

export interface AgentPlanStep {
  content: string
  status: PlanStatus
}

export interface CodingDiffSummary {
  path: string
  additions: number
  deletions: number
  status: 'added' | 'modified' | 'deleted' | 'renamed'
}

export interface ActionDetail {
  action: string
  parameter?: string
  result?: string
  tool?: string
  exitCode?: number
}

export interface ErrorDetail {
  code: CodingErrorCode
  message: string
}

export interface UsageDetail {
  model: string
  inputTokens: number
  outputTokens: number
  costUsd?: number
}

export interface RunnerEvent {
  id: string
  turnId?: string
  type: RunnerEventType
  ephemeral?: boolean
  body?: string
  action?: ActionDetail
  plan?: AgentPlanStep[]
  diff?: CodingDiffSummary[]
  error?: ErrorDetail
  usage?: UsageDetail
  phase?: string
  createdAt: string
}

export interface EventsRequest {
  events: RunnerEvent[]
}

export const MAX_EVENTS_PER_REQUEST = 100

export interface InboxItem {
  seq: number
  kind: 'turn' | 'stop' | 'signal'
  turn?: Turn
  signal?: 'continue' | 'auth' | 'select' | string
  data?: Record<string, unknown>
}

export interface InboxResponse {
  items: InboxItem[]
}

export interface CodingCommit {
  sha: string
  branch: string
  turnActivityId?: string
  message?: string
  signed: boolean
  createdAt: string
}

export type TurnStatus = 'success' | 'error' | 'stopped' | 'awaitingInput'

export interface TurnCompleteRequest {
  status: TurnStatus
  summary?: string
  displayTitle?: string
  harnessSessionId?: string
  commits?: CodingCommit[]
  pushed: boolean
  headSha?: string
  diff?: CodingDiffSummary[]
  error?: ErrorDetail
  /** The turn's write scope: only "write" turns publish (commit, push, draft PR). */
  scope?: 'write' | 'noPublish' | 'readOnly'
}

export interface CredentialRequest {
  protocol: string
  host: string
  path?: string
  purpose: 'git' | 'cli'
  /** git's "erase": the host rejected the credential with this fingerprint (hex SHA-256, never the value). */
  rejected?: boolean
  fingerprint?: string
}

export interface CredentialResponse {
  username: string
  password: string
  expiresAt: string
  cliEnv?: Record<string, string>
}

export interface SignRequest {
  format: 'ssh' | 'openpgp'
  /** base64, as Go encodes []byte. */
  payload: string
}

export interface SignResponse {
  signature: string
}

export interface SnapshotCompleteRequest {
  status: 'ready' | 'failed'
  baseSha?: string
  sizeBytes?: number
  errorCode?: CodingErrorCode
  error?: string
}

export interface ArtifactResponse {
  id: string
  url: string
}

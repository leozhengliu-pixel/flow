/**
 * Coding sessions (docs/coding-sessions-design.md). Mirrors
 * api/internal/domain/coding.go; keep both in sync. Re-exported from
 * "@/types/flow".
 */
/** Same shape as the agent session plan step (ApplicationPlanStep). */
export interface CodingPlanStep { content: string; status: "pending" | "inProgress" | "completed" | "canceled" }
/** Same shape as the agent session external URL (ApplicationExternalUrl). */
export interface CodingExternalURL { label: string; url: string }

/** Linear AgentSession states. Labels: Starting, Working, Stopping…, Finished, Needs input, Errored, Stopped. */
export type CodingSessionState = "pending" | "active" | "stopping" | "complete" | "awaitingInput" | "error" | "stale";

export type CodingSessionPhase =
  | "awaitingApproval" | "queued" | "provisioning" | "starting" | "setup" | "running"
  | "awaitingUser" | "publishing" | "idle" | "stopping" | "done";

export type CodingErrorCode =
  | "noCodeAccess" | "noGitHubConnection" | "noGitLabConnection" | "missingCommitSigningKey" | "noPolicyRepos"
  | "personalAccessDenied" | "githubTransientError" | "gitlabTransientError" | "repoNotInPolicy"
  | "requiresGithubConnection" | "requiresGitlabConnection" | "quotaExceeded" | "permissionDenied"
  | "approvalExpired" | "capacityUnavailable" | "sandboxDisabled" | "sandboxStartFailed" | "imagePullFailed"
  | "unschedulable" | "environmentSetupFailed" | "snapshotFailed" | "runnerLost" | "sandboxLost" | "turnTimeout"
  | "sessionTimeout" | "harnessFailed" | "modelUnavailable" | "pushFailed" | "pullRequestFailed" | "signingFailed"
  | "internal";

export type CodeHostKind = "github" | "gitlab";
export type CodingHarness = "codex" | "claudeCode" | "openSource";
export type CodingSandboxSize = "small" | "medium" | "large";
export type CodingNetworkMode = "default" | "extended" | "open" | "none";
export type CodingDependencyManagement = "custom-only" | "custom-and-generated";
export type CodingReasoningEffort = "" | "low" | "medium" | "high";
export type CodingSessionSource = "issue" | "agentChat" | "comment" | "loop" | "pullRequest" | "environmentSetup" | "external";

export interface CodingMiseFile { path: string; content: string }
export interface CodingEnvVar { name: string; value: string }
export interface CodingMiseConfig {
  tools?: Record<string, string>;
  files?: CodingMiseFile[];
  environmentVariables?: CodingEnvVar[];
}
/** A saved secret: the value is never returned and cannot be changed. */
export interface CodingSecretRef { name: string; createdAt: string; createdBy?: string }

export type CodingSnapshotStatus = "queued" | "building" | "ready" | "failed" | "canceled" | "expired" | "superseded";
export interface CodingEnvironmentSnapshot {
  id: string;
  environmentId: string;
  status: CodingSnapshotStatus;
  strategy: "archive" | "volumesnapshot";
  configHash: string;
  image: string;
  baseRef: string;
  baseSha?: string;
  sizeBytes?: number;
  sandboxId?: string;
  errorCode?: CodingErrorCode;
  error?: string;
  requestedBy?: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  expiresAt?: string;
}

/** "Configure with agent" / "Fix with agent" result awaiting apply/discard. */
export interface CodingEnvironmentProposal {
  sessionId: string;
  mise?: CodingMiseConfig;
  prepareScript?: string;
  customGuidance?: string;
  dependencyManagement?: CodingDependencyManagement;
  sandboxSize?: CodingSandboxSize;
  missingSecrets?: string[];
  explanation: string;
  verified: boolean;
  createdAt: string;
}

export interface CodingEnvironment {
  id: string;
  name: string;
  codeHost: CodeHostKind;
  hostName: string;
  repository: string;
  repositoryId?: string;
  defaultBranch?: string;
  ownerIds?: string[];
  harness?: CodingHarness | "";
  model?: string;
  network?: CodingNetworkMode;
  allowedDomains?: string[];
  sandboxSize?: CodingSandboxSize;
  mise: CodingMiseConfig;
  prepareScript?: string;
  customGuidance?: string;
  dependencyManagement?: CodingDependencyManagement;
  secrets?: CodingSecretRef[];
  pendingProposal?: CodingEnvironmentProposal;
  latestSnapshot?: CodingEnvironmentSnapshot;
  archivedAt?: string;
  createdBy?: string;
  version: number;
  createdAt: string;
  updatedAt: string;
}

export type CodingEnvironmentInput = Partial<Omit<CodingEnvironment, "id" | "secrets" | "pendingProposal" | "latestSnapshot" | "archivedAt" | "createdAt" | "updatedAt" | "createdBy">>;

export interface CodingSandbox {
  id: string;
  sessionId?: string;
  kind: "session" | "snapshot" | "debug" | "warm";
  status: "requested" | "creating" | "running" | "stopping" | "stopped" | "failed" | "lost";
  region?: string;
  size?: CodingSandboxSize;
  repository?: string;
  branchName?: string;
  baseRef?: string;
  logsUrl?: string;
  errorCode?: CodingErrorCode;
  createdAt: string;
  startedAt?: string;
  endedAt?: string;
}

/** A PR (GitHub) or MR (GitLab) linked to a session. */
export interface SessionPullRequest {
  sessionId: string;
  codeHost: CodeHostKind;
  hostName: string;
  repository: string;
  number: number;
  externalId?: string;
  url: string;
  title?: string;
  state: "open" | "closed" | "merged";
  draft: boolean;
  headBranch: string;
  baseBranch: string;
  headSha?: string;
  reviewId?: string;
  /** Host login the PR/MR was opened as. */
  openedAs?: string;
  openedVia?: "member" | "workspace";
  createdAt: string;
  updatedAt: string;
}

export interface CodingCommit { sha: string; branch: string; turnActivityId?: string; message?: string; signed: boolean; createdAt: string }
export interface CodingDiffSummary { path: string; additions: number; deletions: number; status: "added" | "modified" | "deleted" | "renamed" }
export interface CodingArtifact {
  id: string; sessionId: string; kind: "screenshot" | "video" | "log"; label: string; url: string; contentType: string; sizeBytes: number; createdAt: string;
  /** Track I: turn and action activity of the capture, before/after pairing name, stable authenticated URL, signed URL expiry. */
  turnId?: string; activityId?: string; group?: string; contentUrl?: string; expiresAt?: string;
}

/** One tool step of a turn: the tool ("Bash", "Edit") and its argument. */
export interface CodingSessionStep {
  turnId?: string;
  action: string;
  parameter?: string;
  at: string;
}

export interface CodingSession {
  id: string;
  /** The AgentTask anchor; activities are read through the agent session API. */
  agentSessionId: string;
  issueId?: string;
  codingEnvironmentId?: string;
  snapshotId?: string;
  creatorId: string;
  source: CodingSessionSource;
  sourceId?: string;
  state: CodingSessionState;
  phase: CodingSessionPhase;
  harness: CodingHarness;
  modelSelection: string;
  model?: string;
  /** Why Auto routed the session to `model`. */
  modelReason?: string;
  /** The next more capable model, offered as "Start over with <model>". */
  escalationModel?: string;
  reasoningEffort?: CodingReasoningEffort;
  region?: string;
  displayTitle?: string;
  summary?: string;
  plan?: CodingPlanStep[];
  externalUrls?: CodingExternalURL[];
  diffs?: CodingDiffSummary[];
  codeHost?: CodeHostKind;
  repository?: string;
  branchName?: string;
  baseRef?: string;
  /** Whose code host identity the session pushes and opens its PR/MR with. */
  pushIdentity?: { via: "member" | "workspace"; codeHost?: CodeHostKind; hostName?: string; login?: string };
  pullRequests?: SessionPullRequest[];
  latestPullRequest?: SessionPullRequest;
  commitsByTurnActivityId?: Record<string, CodingCommit[]>;
  sandboxes?: CodingSandbox[];
  artifacts?: CodingArtifact[];
  errorCode?: CodingErrorCode;
  error?: string;
  approvalState?: "" | "required" | "approved" | "rejected";
  approvedBy?: string;
  costMicros: number;
  version: number;
  createdAt: string;
  updatedAt: string;
  startedAt?: string;
  endedAt?: string;
  /** Reconciler bookkeeping (track B). */
  phaseStartedAt?: string;
  phaseDeadline?: string;
  attempts?: number;
  sandboxAttempts?: number;
  stopRequested?: boolean;
  stoppedBy?: string;
  /** Trigger-ranked environment ids for an ambiguous repository (best first). */
  environmentCandidates?: string[];
  preferredEnvironmentId?: string;
  loopId?: string;
  sandboxSize?: CodingSandboxSize;
  sandboxId?: string;
  currentTurnId?: string;
  lastTurnId?: string;
  turnStartedAt?: string;
  /** The running turn's latest tool step (the status row's live line). */
  currentStep?: CodingSessionStep;
  /** Set when the session was archived (its pull request closed without merging). */
  archivedAt?: string;
  runningMillis?: number;
  harnessResumeId?: string;
  headSha?: string;
  pushed?: boolean;
  existingBranch?: boolean;
  hostName?: string;
  signingFormat?: string;
  signingPublicKey?: string;
  /** "Subscribe to session": explicit subscribers, and members who opted out (the creator is subscribed unless they did). */
  subscriberIds?: string[];
  unsubscribedIds?: string[];
}

/** One line of a diff hunk (GET /api/coding/sessions/{id}/diff). */
export interface CodingDiffLine { kind: "context" | "add" | "delete" | "note"; oldLine?: number; newLine?: number; text: string }
export interface CodingDiffHunk { header: string; oldStart: number; oldLines: number; newStart: number; newLines: number; lines: CodingDiffLine[] }
export interface CodingDiffFile {
  path: string;
  previousPath?: string;
  status: CodingDiffSummary["status"];
  additions: number;
  deletions: number;
  binary?: boolean;
  /** The host left the patch out, or it is past the response's line cap. */
  tooLarge?: boolean;
  hunks: CodingDiffHunk[];
}
/** The session's changes: the branch compared with its base on the code host, the runner's per-file summary, or none yet. */
export interface CodingSessionDiff {
  source: "codeHost" | "summary" | "none";
  base?: string;
  head?: string;
  headSha?: string;
  truncated?: boolean;
  errorCode?: CodingErrorCode;
  additions: number;
  deletions: number;
  files: CodingDiffFile[];
}

export interface CreateCodingSessionInput {
  source: CodingSessionSource;
  issueId?: string;
  sourceId?: string;
  prompt: string;
  environmentId?: string;
  harness?: CodingHarness;
  model?: string;
  reasoningEffort?: CodingReasoningEffort;
  baseRef?: string;
}

export interface CodingSettings {
  enabled: boolean;
  harness: CodingHarness;
  model: string;
  reasoningEffort?: CodingReasoningEffort;
  requireSignedCommits: boolean;
  pinnedRegion?: string;
  trustedExternalSources: string[];
  loopRepositoryAccess: "none" | "all" | "selected";
  loopRepositories: string[];
}

/** Model picker group: Auto first, then Frontier (with one-line descriptions), then Other models. */
export type CodingModelGroup = "auto" | "frontier" | "other";
/** `group` and `description` are optional: older servers omit them and the picker renders a flat list. */
export interface CodingModelOption { id: string; label: string; harness: CodingHarness; auto?: boolean; group?: CodingModelGroup; description?: string; reasoningEfforts?: CodingReasoningEffort[] }

export interface CodingPreflightCheck { name: string; ok: boolean; warning?: boolean; message: string }
export type GitLabCredentialMode = "project-token" | "oauth" | "connection";

export interface CodingStatus {
  enabled: boolean;
  provider: "disabled" | "fake" | "docker" | "kubernetes";
  snapshotStrategy: "archive" | "volumesnapshot";
  regions: string[];
  sizes: Record<CodingSandboxSize, { cpu: string; memory: string }>;
  codeHosts: {
    codeHost: CodeHostKind;
    hostName: string;
    connected: boolean;
    installUrl?: string;
    /** GitLab only: FLOW_CODING_GITLAB_CREDENTIAL_MODE. */
    credentialMode?: GitLabCredentialMode;
  }[];
  debugTerminal: boolean;
  browserTesting: boolean;
  preflight: CodingPreflightCheck[];
}

export interface CodeHostRepository {
  codeHost: CodeHostKind;
  hostName: string;
  fullName: string;
  id?: string;
  defaultBranch: string;
  webUrl: string;
  private: boolean;
  archived: boolean;
}

export interface CodingUsageSummary {
  since: string;
  totalCostMicros: number;
  llmCostMicros: number;
  runtimeCostMicros: number;
  byUser: Record<string, number>;
  byLoop: Record<string, number>;
  sessions: number;
}

export interface GitHubAppInstallationSummary {
  /** Flow's row id; empty for an installation the workspace has not linked yet. */
  id?: string;
  installationId: string;
  accountLogin: string;
  accountType?: string;
  repositorySelection?: string;
  suspendedAt?: string;
}

export interface GitHubAppStatus {
  configured: boolean;
  hostName: string;
  slug?: string;
  installations: (GitHubAppInstallationSummary & { id: string })[];
  /** User-to-server OAuth is configured, so members can link their GitHub account. */
  canLinkAccount?: boolean;
  /** The viewer's linked GitHub account, when linked. */
  linkedAccount?: { login: string; linkedAt: string };
}

export type ReviewCodingSessionKind = "fix" | "followUp" | "rebase" | "lint" | "test";

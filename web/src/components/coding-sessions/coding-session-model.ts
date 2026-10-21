/**
 * Pure helpers for the coding session view (docs/coding-sessions-design.md §4, §15, §24 track G): Linear's state labels,
 * phase descriptions, the error code → copy + next action map, PR/MR and CI status, cost, and repository suggestion
 * for picking an environment. No React here so every rule is unit-tested on its own.
 */
import { integrationSettingsPath, newSigningKeyPath, settingsPath, usageSettingsPath } from '@/lib/app-routes'
import type { AgentMessage, CodeReview, Issue } from '@/types/flow'
import type {
  CodeHostKind,
  CodingEnvironment,
  CodingErrorCode,
  CodingHarness,
  CodingSandbox,
  CodingSession,
  CodingSessionPhase,
  CodingSessionState,
  SessionPullRequest,
} from '@/types/coding-sessions'

/** The reference app's session status labels (session lists and menus). */
export const CODING_STATE_LABELS: Record<CodingSessionState, string> = {
  pending: 'Working…',
  active: 'Working…',
  stopping: 'Stopping…',
  complete: 'Finished',
  awaitingInput: 'Needs input',
  error: 'Error',
  stale: 'Stopped responding',
}

/** Failed, or ended without anyone stopping it (the agent stopped responding or never started): the red avatar dot. */
export const codingSessionStalled = (session: Pick<CodingSession, 'state' | 'stoppedBy'>) => session.state === 'error' || (session.state === 'stale' && !session.stoppedBy)

/** The state label of one session: a stale session was stopped by someone, never started, or stopped responding. */
export function codingStateLabel(session: Pick<CodingSession, 'state' | 'stoppedBy' | 'startedAt'>): string {
  if (session.state !== 'stale') return CODING_STATE_LABELS[session.state] ?? session.state
  if (session.stoppedBy) return 'Stopped'
  return session.startedAt ? 'Stopped responding' : 'Didn’t start'
}

/** The finer progress line under the state badge. `done` and `idle` add nothing to the state label. */
export const CODING_PHASE_LABELS: Record<CodingSessionPhase, string> = {
  awaitingApproval: 'Waiting for approval',
  queued: 'Queued',
  provisioning: 'Preparing sandbox',
  starting: 'Starting sandbox',
  setup: 'Setting up environment',
  running: 'Running agent',
  awaitingUser: 'Waiting for your answer',
  publishing: 'Opening pull request',
  idle: '',
  stopping: 'Stopping sandbox',
  done: '',
}

/** Ordered progress steps shown while a session starts (the phases a user waits through). */
export const CODING_PROGRESS_STEPS: CodingSessionPhase[] = ['queued', 'provisioning', 'starting', 'setup', 'running', 'publishing']

export const CODING_HARNESS_LABELS: Record<CodingHarness, string> = { codex: 'Codex', claudeCode: 'Claude Code', openSource: 'Open source' }

export const isCodingSessionTerminal = (session: Pick<CodingSession, 'state'>) => session.state === 'complete' || session.state === 'error' || session.state === 'stale'
/** States in which the reconciler still changes the session on its own (a fallback poll runs only then). */
export const isCodingSessionLive = (session: Pick<CodingSession, 'state'>) => session.state === 'pending' || session.state === 'active' || session.state === 'stopping'

export type CodingSessionCapabilities = {
  stop: boolean
  prompt: boolean
  restart: boolean
  restartWithDefaultModel: boolean
  /** "Start over with <model>": a finished session with a more capable model to run it again with. */
  startOver: boolean
  approve: boolean
  createPullRequest: boolean
  dismiss: boolean
}

/** Which actions a session offers (§4.3). */
export function codingSessionCapabilities(session: CodingSession): CodingSessionCapabilities {
  const terminal = isCodingSessionTerminal(session)
  const awaitingApproval = session.phase === 'awaitingApproval'
  const hasPullRequest = Boolean(session.latestPullRequest || session.pullRequests?.length)
  const pullRequestFailed = session.errorCode === 'pullRequestFailed'
  const pushFailed = session.errorCode === 'pushFailed'
  return {
    stop: !terminal && session.state !== 'stopping' && !awaitingApproval,
    prompt: session.state !== 'stopping' && !awaitingApproval,
    restart: session.state === 'error' || session.state === 'stale',
    restartWithDefaultModel: (session.state === 'error' || session.state === 'stale') && session.modelSelection !== 'auto',
    startOver: terminal && (session.phase === 'idle' || session.phase === 'done') && session.approvalState !== 'rejected' && Boolean(session.escalationModel),
    approve: awaitingApproval,
    createPullRequest: !hasPullRequest && Boolean(session.branchName) && (pullRequestFailed || (branchPublished(session) && (pushFailed || terminal))),
    dismiss: terminal,
  }
}

const hasCommits = (session: CodingSession) => Object.values(session.commitsByTurnActivityId ?? {}).some(list => list.length > 0)

/**
 * Whether the server knows the branch reached the code host: a turn pushed it (`pushed` / `existingBranch`) or
 * recorded commits (only pushed turns record them). `headSha` is no evidence: a turn stopped before its first
 * commit reports the clone baseline.
 */
const branchPublished = (session: CodingSession) => Boolean(session.pushed || session.existingBranch) || hasCommits(session)

export type CodingErrorAction =
  | { kind: 'link'; label: string; href: string }
  | { kind: 'restart' }
  | { kind: 'restartWithDefaultModel' }
  | { kind: 'createPullRequest' }
  | { kind: 'fixWithAgent' }
  | { kind: 'viewLogs' }

export type CodingErrorInfo = { title: string; message: string; actions: CodingErrorAction[]; /** Admins see the raw reason (§4.4). */ adminDetail: boolean }

type ErrorContext = { workspace: string; codeHost?: CodeHostKind }

const restart: CodingErrorAction = { kind: 'restart' }

/** Clear copy and the next action for every `CodingErrorCode` (§4.4). Unknown codes fall back to `internal`. */
export function codingErrorInfo(code: CodingErrorCode | undefined, context: ErrorContext): CodingErrorInfo {
  const { workspace } = context
  const host = context.codeHost ?? 'github'
  const link = (label: string, href: string): CodingErrorAction => ({ kind: 'link', label, href })
  const environments = link('Create environment', settingsPath(workspace, 'coding-sessions'))
  const info = (title: string, message: string, actions: CodingErrorAction[], adminDetail = false): CodingErrorInfo => ({ title, message, actions, adminDetail })
  switch (code) {
    case 'noCodeAccess': return info('No access to the repository', host === 'gitlab' ? 'The GitLab connection can’t reach this repository. Give it access in the integration settings, then restart.' : 'The GitHub App can’t reach this repository. Give it access in the integration settings, then restart.', [link('Open integration settings', integrationSettingsPath(workspace, host)), restart])
    case 'noGitHubConnection': return info('GitHub isn’t connected', 'Install the Flow GitHub App for this workspace so the agent can clone, push and open pull requests.', [link('Connect GitHub', integrationSettingsPath(workspace, 'github'))])
    case 'noGitLabConnection': return info('GitLab isn’t connected', 'Connect GitLab for this workspace so the agent can clone, push and open merge requests.', [link('Connect GitLab', integrationSettingsPath(workspace, 'gitlab'))])
    case 'requiresGithubConnection': return info('Link your GitHub account', 'This workspace requires coding sessions to run with your own GitHub access. Link your account, then restart.', [link('Link GitHub account', settingsPath(workspace, 'connections')), restart])
    case 'requiresGitlabConnection': return info('Link your GitLab account', 'This workspace requires coding sessions to run with your own GitLab access. Link your account, then restart.', [link('Link GitLab account', settingsPath(workspace, 'connections')), restart])
    case 'personalAccessDenied': return info('You can’t push to this repository', 'Your account lacks write access to the repository. Ask a repository admin for access.', [])
    case 'missingCommitSigningKey': return info('Upload a commit signing key', 'This workspace requires signed commits, and you have no usable signing key. Upload one, then restart.', [link('Upload signing key', newSigningKeyPath(workspace)), restart])
    case 'noPolicyRepos': return info('No environment for this repository', 'Create a coding environment for the repository the agent should work in.', [environments])
    case 'repoNotInPolicy': return info('Repository not allowed', 'The repository isn’t covered by an environment or by this workspace’s repository access. Create an environment for it.', [environments])
    case 'githubTransientError': return info('GitHub didn’t respond', 'GitHub kept failing after several retries. Try again in a moment.', [restart])
    case 'gitlabTransientError': return info('GitLab didn’t respond', 'GitLab kept failing after several retries. Try again in a moment.', [restart])
    case 'quotaExceeded': return info('Spend limit reached', 'The workspace or your coding spend limit is used up. Add credits or raise the limit to continue.', [link('Review usage & limits', usageSettingsPath(workspace, 'spend-limits'))])
    case 'permissionDenied': return info('Not allowed', 'You don’t have permission to run coding sessions for this team.', [])
    case 'approvalExpired': return info('Approval expired', 'Nobody approved the session or answered it in time. Restart it to try again.', [restart])
    case 'capacityUnavailable': return info('No sandbox capacity', 'All sandboxes are busy. Restart in a few minutes.', [restart], true)
    case 'sandboxDisabled': return info('Coding sessions are off', 'Sandboxes aren’t enabled on this Flow server. Ask an admin to turn them on.', [link('Open coding settings', settingsPath(workspace, 'coding-sessions'))], true)
    case 'sandboxStartFailed': return info('The sandbox didn’t start', 'Flow couldn’t start a sandbox for this session.', [restart], true)
    case 'imagePullFailed': return info('The sandbox image didn’t load', 'The sandbox image couldn’t be pulled.', [restart], true)
    case 'unschedulable': return info('No room for the sandbox', 'The cluster had no node that could run the sandbox.', [restart], true)
    case 'environmentSetupFailed': return info('Environment setup failed', 'The environment’s setup or prepare script failed. Check the logs, or let the agent fix the environment.', [{ kind: 'fixWithAgent' }, { kind: 'viewLogs' }])
    case 'snapshotFailed': return info('Snapshot failed', 'The environment snapshot couldn’t be restored or built. Check the logs, or let the agent fix the environment.', [{ kind: 'fixWithAgent' }, { kind: 'viewLogs' }])
    case 'runnerLost': return info('The agent stopped responding', 'The sandbox runner went silent. Work that was pushed is kept; restart to continue.', [restart])
    case 'sandboxLost': return info('The sandbox was lost', 'The sandbox stopped unexpectedly (evicted or out of memory). Work that was pushed is kept; restart to continue.', [restart])
    case 'turnTimeout': return info('The turn took too long', 'The agent hit the time limit for one turn. Restart to continue from the pushed branch.', [restart])
    case 'sessionTimeout': return info('Session time limit reached', 'The session ran for its maximum duration. Restart to continue from the pushed branch.', [restart])
    case 'harnessFailed': return info('The coding agent failed', 'The coding agent exited with an error.', [restart, { kind: 'restartWithDefaultModel' }])
    case 'modelUnavailable': return info('Model unavailable', 'The selected model was rejected. Restart with the default model to continue.', [{ kind: 'restartWithDefaultModel' }])
    case 'pushFailed': return info('Push failed', 'The branch couldn’t be pushed (for example a protected branch or a hook).', [{ kind: 'createPullRequest' }, restart])
    case 'pullRequestFailed': return info('Pull request failed', 'The branch was pushed, but the pull request couldn’t be opened.', [{ kind: 'createPullRequest' }, restart])
    case 'signingFailed': return info('Commit signing failed', 'Flow couldn’t sign the agent’s commits with your key.', [link('Upload signing key', newSigningKeyPath(workspace)), { kind: 'createPullRequest' }, restart])
    case 'internal':
    default: return info('Something went wrong', 'The session failed because of an internal error.', [restart], true)
  }
}

/** Every code the UI maps (mirrors `CodingErrorCode`; the tests check it is complete). */
export const CODING_ERROR_CODES: CodingErrorCode[] = [
  'noCodeAccess', 'noGitHubConnection', 'noGitLabConnection', 'missingCommitSigningKey', 'noPolicyRepos',
  'personalAccessDenied', 'githubTransientError', 'gitlabTransientError', 'repoNotInPolicy',
  'requiresGithubConnection', 'requiresGitlabConnection', 'quotaExceeded', 'permissionDenied',
  'approvalExpired', 'capacityUnavailable', 'sandboxDisabled', 'sandboxStartFailed', 'imagePullFailed',
  'unschedulable', 'environmentSetupFailed', 'snapshotFailed', 'runnerLost', 'sandboxLost', 'turnTimeout',
  'sessionTimeout', 'harnessFailed', 'modelUnavailable', 'pushFailed', 'pullRequestFailed', 'signingFailed',
  'internal',
]

export const isCodingErrorCode = (value: unknown): value is CodingErrorCode => typeof value === 'string' && (CODING_ERROR_CODES as string[]).includes(value)

/** "Claude Code · claude-sonnet-4-5 (Auto)". */
export function codingModelLabel(session: Pick<CodingSession, 'harness' | 'model' | 'modelSelection'>): { harness: string; model: string } {
  const harness = CODING_HARNESS_LABELS[session.harness] ?? session.harness
  const auto = !session.modelSelection || session.modelSelection === 'auto'
  const model = session.model ? (auto ? `${session.model} (Auto)` : session.model) : auto ? 'Auto' : session.modelSelection
  return { harness, model }
}

export type PullRequestDisplayState = 'draft' | 'open' | 'merged' | 'closed'
export const pullRequestDisplayState = (pr: Pick<SessionPullRequest, 'state' | 'draft'>): PullRequestDisplayState => pr.state === 'open' && pr.draft ? 'draft' : pr.state
export const PULL_REQUEST_STATE_LABELS: Record<PullRequestDisplayState, string> = { draft: 'Draft', open: 'Open', merged: 'Merged', closed: 'Closed' }
/** "#12" on GitHub, "!12" on GitLab. */
export const pullRequestNumberLabel = (pr: Pick<SessionPullRequest, 'codeHost' | 'number'>) => `${pr.codeHost === 'gitlab' ? '!' : '#'}${pr.number}`

export type CiStatus = 'none' | 'pending' | 'passed' | 'failed'
export const CI_STATUS_LABELS: Record<CiStatus, string> = { none: 'No checks', pending: 'Checks running', passed: 'Checks passed', failed: 'Checks failed' }

/** The review synced for a session PR/MR: by `reviewId`, else by provider + repository + number. */
export function reviewForPullRequest(pr: SessionPullRequest, reviews: readonly CodeReview[]): CodeReview | undefined {
  if (pr.reviewId) {
    const byId = reviews.find(review => review.id === pr.reviewId)
    if (byId) return byId
  }
  const repository = normalizeRepository(pr.repository)
  return reviews.find(review => review.provider === pr.codeHost && review.number === pr.number && normalizeRepository(`${review.repositoryOwner}/${review.repositoryName}`) === repository)
}

/** Failed wins over running, running over passed; skipped checks don't count. */
export function ciStatus(review: Pick<CodeReview, 'checks'> | undefined): CiStatus {
  const checks = (review?.checks ?? []).filter(check => check.status !== 'skipped')
  if (!checks.length) return 'none'
  if (checks.some(check => check.status === 'failed')) return 'failed'
  if (checks.some(check => check.status === 'pending' || check.status === 'running')) return 'pending'
  return 'passed'
}

/** The sandbox shown in the session view: the newest session sandbox. */
export function currentSandbox(session: Pick<CodingSession, 'sandboxes'>): CodingSandbox | undefined {
  const sandboxes = (session.sandboxes ?? []).filter(sandbox => sandbox.kind === 'session')
  return [...sandboxes].sort((a, b) => (Date.parse(b.createdAt) || 0) - (Date.parse(a.createdAt) || 0))[0]
}

/** "owner/repo" lowercased, from a full name, an https URL or an scp-like git URL. */
export function normalizeRepository(value: string): string {
  let text = value.trim().toLowerCase().replace(/\.git$/, '').replace(/\/+$/, '')
  const scp = /^[^@]+@[^:]+:(.+)$/.exec(text)
  if (scp) text = scp[1]
  else if (/^[a-z]+:\/\//.test(text)) {
    try { text = new URL(text).pathname.replace(/^\/+/, '') } catch { /* keep the raw text */ }
  }
  return text
}

export type EnvironmentSuggestion =
  | { kind: 'none' }
  | { kind: 'single'; environment: CodingEnvironment; reason: 'only' | 'linkedPullRequest' | 'lastUsed' }
  | { kind: 'ambiguous'; environments: CodingEnvironment[]; suggested?: CodingEnvironment }

/**
 * Picks the environment for an issue the way §15 resolves repositories on the server: the single active environment;
 * else the repository of the issue's linked PRs/MRs; else the team's last-used environment. Anything else is
 * ambiguous and the UI shows a picker (suggestions first).
 */
export function suggestEnvironment(environments: readonly CodingEnvironment[], input: { issue?: Pick<Issue, 'id'>; reviews?: readonly CodeReview[]; lastUsedId?: string }): EnvironmentSuggestion {
  const active = environments.filter(environment => !environment.archivedAt)
  if (!active.length) return { kind: 'none' }
  if (active.length === 1) return { kind: 'single', environment: active[0], reason: 'only' }
  if (input.issue) {
    const repositories = new Set((input.reviews ?? []).filter(review => review.issueIds.includes(input.issue!.id)).map(review => `${review.provider}:${normalizeRepository(`${review.repositoryOwner}/${review.repositoryName}`)}`))
    const linked = active.filter(environment => repositories.has(`${environment.codeHost}:${normalizeRepository(environment.repository)}`))
    if (linked.length === 1) return { kind: 'single', environment: linked[0], reason: 'linkedPullRequest' }
    if (linked.length > 1) return { kind: 'ambiguous', environments: [...linked, ...active.filter(environment => !linked.includes(environment))], suggested: linked[0] }
  }
  const lastUsed = input.lastUsedId ? active.find(environment => environment.id === input.lastUsedId) : undefined
  if (lastUsed) return { kind: 'single', environment: lastUsed, reason: 'lastUsed' }
  return { kind: 'ambiguous', environments: [...active].sort((a, b) => a.name.localeCompare(b.name)) }
}

const lastUsedKey = (workspace: string, teamId: string) => `flow:coding-sessions:last-environment:${workspace}:${teamId}`
export function readLastUsedEnvironment(workspace: string, teamId: string): string | undefined {
  try { return localStorage.getItem(lastUsedKey(workspace, teamId)) ?? undefined } catch { return undefined }
}
export function writeLastUsedEnvironment(workspace: string, teamId: string, environmentId: string) {
  try { localStorage.setItem(lastUsedKey(workspace, teamId), environmentId) } catch { /* a convenience only */ }
}

/**
 * A coding session id from an agent chat tool call (`start_coding_session`). The tool's result is the session JSON
 * (or `{session}` / `{sessionId}` / `{codingSessionId}`), possibly serialized.
 */
export function codingSessionIdFromToolResult(result: unknown): string | undefined {
  let value = result
  if (typeof value === 'string') {
    try { value = JSON.parse(value) } catch { return undefined }
  }
  if (!value || typeof value !== 'object') return undefined
  const record = value as Record<string, unknown>
  if (typeof record.codingSessionId === 'string') return record.codingSessionId
  if (record.session && typeof record.session === 'object') return codingSessionIdFromToolResult(record.session)
  if (typeof record.sessionId === 'string') return record.sessionId
  if (typeof record.id === 'string' && typeof record.agentSessionId === 'string') return record.id
  return undefined
}

export const CODING_SESSION_TOOL_NAMES = new Set(['start_coding_session', 'flow_start_coding_session'])

/** Coding session ids an agent chat reply started (its `start_coding_session` tool calls), in order, without repeats. */
export function codingSessionIdsFromMessage(message: Pick<AgentMessage, 'parts'>): string[] {
  const ids: string[] = []
  for (const part of message.parts ?? []) {
    const call = part.type === 'toolCall' ? part.toolCall : undefined
    if (!call || !CODING_SESSION_TOOL_NAMES.has(call.name) || call.status === 'error') continue
    const id = codingSessionIdFromToolResult(call.result)
    if (id && !ids.includes(id)) ids.push(id)
  }
  return ids
}

export type CodingStatusTone = 'working' | 'input' | 'done' | 'error' | 'stopped'
export type CodingStatusLine = { tone: CodingStatusTone; label: string; detail?: string; live: boolean; /** Replaces "{count}" in the translated label. */ count?: number; /** Replaces "{repo}" in the translated label. */ repo?: string; /** The detail is app copy (translated), not the session's own text. */ translateDetail?: boolean; /** The label is the session's own text (a tool name), not app copy. */ rawLabel?: boolean; /** The detail is a command or path, shown as code. */ codeDetail?: boolean }

/**
 * The one-line status of a session, as the reference app's session rows show it: the label is translated by the caller,
 * the detail is the session's own text. `issue` is the issue activity row ("Setting up owner/repo…" while it starts,
 * "Finished" once a PR is open, "Changed 3 files" for unpublished changes, the running turn's latest tool step
 * "Bash `git status`"), `chat` the agent chat card ("Completed").
 */
export function codingStatusLine(session: Pick<CodingSession, 'state' | 'phase' | 'summary' | 'errorCode' | 'diffs' | 'stoppedBy' | 'startedAt' | 'repository' | 'latestPullRequest' | 'pullRequests'> & Partial<Pick<CodingSession, 'currentStep' | 'currentTurnId'>>, errorTitle?: string, surface: 'issue' | 'chat' = 'issue'): CodingStatusLine {
  switch (session.state) {
    case 'error': return { tone: 'error', label: 'Error', detail: errorTitle, live: false }
    case 'complete': {
      if (surface === 'chat') return { tone: 'done', label: 'Completed', detail: session.summary, live: false }
      const files = session.diffs?.length ?? 0
      const published = Boolean(session.latestPullRequest ?? session.pullRequests?.length)
      return files && !published ? { tone: 'done', label: files === 1 ? 'Changed {count} file' : 'Changed {count} files', count: files, detail: session.summary, live: false } : { tone: 'done', label: 'Finished', detail: session.summary, live: false }
    }
    case 'stale':
      if (session.stoppedBy) return { tone: 'stopped', label: 'Stopped', live: false }
      return session.startedAt ? { tone: 'error', label: 'Stopped responding', live: false } : { tone: 'error', label: 'Agent didn’t start', detail: 'The request may not have reached the agent', translateDetail: true, live: false }
    case 'stopping': return { tone: 'working', label: 'Stop requested', live: true }
    case 'awaitingInput': return { tone: 'input', label: session.phase === 'awaitingApproval' ? 'Waiting for approval' : 'Needs input', live: false }
    case 'pending': return session.repository && session.phase !== 'queued'
      ? { tone: 'working', label: 'Setting up {repo}…', repo: session.repository, live: true }
      : { tone: 'working', label: 'Starting coding session…', live: true }
    default: {
      const step = session.currentStep
      if (surface === 'issue' && step?.action && session.phase === 'running' && (!step.turnId || !session.currentTurnId || step.turnId === session.currentTurnId))
        return { tone: 'working', label: step.action, rawLabel: true, detail: step.parameter || undefined, codeDetail: true, live: true }
      return { tone: 'working', label: surface === 'chat' ? 'Working for' : 'Thinking…', live: true }
    }
  }
}

/** Elapsed time like the reference app's session timer: "02:42", "1:02:42". */
export function formatCodingElapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  const hours = Math.floor(total / 3600), minutes = Math.floor((total % 3600) / 60), seconds = total % 60
  const two = (value: number) => String(value).padStart(2, '0')
  return hours ? `${hours}:${two(minutes)}:${two(seconds)}` : `${two(minutes)}:${two(seconds)}`
}

/** When the current run started (the turn, else the session). */
export const codingRunStartedAt = (session: Pick<CodingSession, 'turnStartedAt' | 'startedAt' | 'createdAt'>) => session.turnStartedAt || session.startedAt || session.createdAt

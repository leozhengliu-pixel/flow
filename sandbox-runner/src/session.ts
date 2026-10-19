import { existsSync } from 'node:fs'
import { chmod, mkdir, rm, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { tarZstBuffer, extractTarZst } from './archive.js'
import { backoffDelay, sleep } from './backoff.js'
import { CliEnvRefresher } from './cli-env.js'
import type { RunnerConfig } from './config.js'
import { baseEnv, mcpCommand, runnerGuidance, writeRuntimeInfo } from './environment.js'
import { EventBuffer } from './event-buffer.js'
import { checkBranchAllowed, commitMessage, isSigningFailure, type Git } from './git/git.js'
import { resumeFailed, type HarnessAdapter, type HarnessContext } from './harness/types.js'
import { errorMessage, silentLogger, type Logger } from './log.js'
import { EventFactory, harnessError, type TurnOutcome } from './mapping/common.js'
import { elicitationMarker } from './mcp/tools/ask-user.js'
import { CommandError, spawnProcess, type RunningProcess, type SpawnOptions } from './process.js'
import type {
  BootstrapResponse,
  CodingCommit,
  CodingDiffSummary,
  CredentialRequest,
  CredentialResponse,
  ErrorDetail,
  HeartbeatPhase,
  HeartbeatRequest,
  HeartbeatResponse,
  InboxItem,
  InboxResponse,
  RunnerEvent,
  SetupSecretsResponse,
  Turn,
  TurnCompleteRequest,
  TurnStatus,
} from './protocol.js'
import { AsyncQueue } from './queue.js'
import { AuthError } from './runtime-client.js'
import { prepareRepository, runSetup, SetupError, SetupLog } from './setup.js'

// Session mode (§16): bootstrap → prepare workspace (and setup) → turns.
// A turn spawns the harness, streams mapped events, then commits, pushes,
// saves the harness state and reports turn-complete. Between turns the
// runner long-polls the inbox for follow-ups and stops. Heartbeats run the
// whole time; a stop (heartbeat, inbox, or SIGTERM) interrupts the harness,
// pushes a WIP commit and completes the turn as stopped.

/** The subset of RuntimeClient a session uses (fakes implement it in tests). */
export interface SessionApi {
  heartbeat(request: HeartbeatRequest, signal?: AbortSignal): Promise<HeartbeatResponse>
  postEvents(events: RunnerEvent[], signal?: AbortSignal): Promise<void>
  inbox(after: number, waitSeconds: number, signal?: AbortSignal): Promise<InboxResponse>
  completeTurn(turnId: string, request: TurnCompleteRequest, signal?: AbortSignal): Promise<void>
  credentials(request: CredentialRequest): Promise<CredentialResponse>
  setupSecrets(): Promise<SetupSecretsResponse>
  getHarnessState(): Promise<Uint8Array | null>
  putHarnessState(archive: Uint8Array): Promise<void>
}

/** The subset of Git a session uses. */
export type SessionGit = Pick<
  Git,
  | 'git'
  | 'repoDir'
  | 'clone'
  | 'isRepo'
  | 'configureCredentials'
  | 'configureIdentity'
  | 'fetch'
  | 'checkoutSessionBranch'
  | 'baseRevision'
  | 'head'
  | 'commitAll'
  | 'commitsSince'
  | 'push'
  | 'diffSummary'
>

export interface HarnessStateStore {
  restore(): Promise<void>
  save(): Promise<void>
}

export type SpawnFn = (command: string, args: string[], options: SpawnOptions) => RunningProcess

export interface SessionDeps {
  config: RunnerConfig
  api: SessionApi
  bootstrap: BootstrapResponse
  git: SessionGit
  harness: HarnessAdapter
  token: () => string
  logger?: Logger
  spawn?: SpawnFn
  harnessState?: HarnessStateStore
  /** Skip the gh/glab credential refresher (tests). */
  cliEnv?: boolean
  newId?: () => string
  now?: () => Date
  heartbeatMs?: number
  inboxWaitSeconds?: number
  pushRetries?: number
  sleep?: (ms: number, signal?: AbortSignal) => Promise<void>
  parentEnv?: NodeJS.ProcessEnv
}

export const HARNESS_STATE_MAX_BYTES = 64 * 1024 * 1024
const STDERR_TAIL_LINES = 40

interface StopRequest {
  by: string
  reason: 'user' | 'flow' | 'signal' | 'revoked'
}

export interface TurnReport extends TurnCompleteRequest {
  turnId: string
}

export class Session {
  private readonly config: RunnerConfig
  private readonly api: SessionApi
  private readonly bootstrap: BootstrapResponse
  private readonly git: SessionGit
  private readonly harness: HarnessAdapter
  private readonly logger: Logger
  private readonly buffer: EventBuffer
  private readonly events: EventFactory
  private readonly turns = new AsyncQueue<Turn>()
  private readonly seenTurns = new Set<string>()
  private readonly loops = new AbortController()
  private readonly spawn: SpawnFn
  private readonly sleep: (ms: number, signal?: AbortSignal) => Promise<void>
  private readonly harnessState: HarnessStateStore
  private readonly newId: () => string
  private phase: HeartbeatPhase = 'setup'
  private turnId: string | undefined
  private inboxAfter = 0
  private stop: StopRequest | undefined
  private current: RunningProcess | undefined
  private resumeId: string | undefined
  private cliRefresher: CliEnvRefresher | undefined
  private loopTasks: Promise<void>[] = []
  /** Completed turns, in order (exposed for tests and logs). */
  readonly reports: TurnReport[] = []

  constructor(private readonly deps: SessionDeps) {
    this.config = deps.config
    this.api = deps.api
    this.bootstrap = deps.bootstrap
    this.git = deps.git
    this.harness = deps.harness
    this.logger = deps.logger ?? silentLogger
    this.spawn = deps.spawn ?? spawnProcess
    this.sleep = deps.sleep ?? sleep
    this.newId = deps.newId ?? (() => crypto.randomUUID())
    this.events = new EventFactory({ newId: this.newId, now: deps.now })
    this.resumeId = deps.bootstrap.harness.resumeId || undefined
    this.buffer = new EventBuffer({
      post: (events) => this.api.postEvents(events),
      logger: this.logger,
      onFatal: () => this.requestStop({ by: 'Flow', reason: 'revoked' }),
    })
    this.harnessState = deps.harnessState ?? defaultHarnessState(this.api, this.config.paths.home, this.harness, this.logger)
  }

  /** Requests a graceful stop (idempotent). */
  requestStop(stop: StopRequest): void {
    if (this.stop) return
    this.stop = stop
    this.logger.info('stop requested', { by: stop.by, reason: stop.reason, turnId: this.turnId })
    this.turns.close()
    if (this.current) void this.current.interrupt(this.stopGraceMs())
  }

  get stopping(): boolean {
    return this.stop !== undefined
  }

  private stopGraceMs(): number {
    return Math.max(1, this.bootstrap.limits.stopGraceSeconds || 60) * 1000
  }

  private env(): NodeJS.ProcessEnv {
    return baseEnv(this.config, this.bootstrap, {}, this.deps.parentEnv)
  }

  /** Runs the session to the end; returns the process exit code. */
  async run(): Promise<number> {
    if (this.bootstrap.turn) this.seenTurns.add(this.bootstrap.turn.id)
    this.loopTasks = [this.heartbeatLoop(), this.inboxLoop()]
    let code = 0
    try {
      const ready = await this.prepare()
      if (!ready) {
        code = 1
      } else {
        let next: Turn | undefined = this.bootstrap.turn
        while (!this.stop) {
          if (!next) {
            this.setPhase('idle')
            next = await this.turns.take()
            if (!next) break
          }
          await this.runTurn(next)
          next = undefined
        }
      }
    } catch (error) {
      if (error instanceof AuthError) {
        this.logger.warn('sandbox token rejected, exiting', { status: error.status })
      } else {
        code = 1
        this.logger.error('session failed', { error: errorMessage(error) })
        this.buffer.push(this.events.error({ code: 'internal', message: `Runner error: ${errorMessage(error)}` }))
      }
    } finally {
      this.cliRefresher?.stop()
      this.buffer.close()
      await this.buffer.flush(30_000)
      this.loops.abort()
      await Promise.allSettled(this.loopTasks)
    }
    return code
  }

  private setPhase(phase: HeartbeatPhase, turnId?: string): void {
    this.phase = phase
    this.turnId = turnId
  }

  // ── loops ──────────────────────────────────────────────────────────────

  private async heartbeatLoop(): Promise<void> {
    const interval = this.deps.heartbeatMs ?? Math.max(1, this.bootstrap.limits.heartbeatSeconds || 15) * 1000
    const signal = this.loops.signal
    while (!signal.aborted) {
      try {
        const response = await this.api.heartbeat({ phase: this.phase, turnId: this.turnId }, signal)
        if (response.stop) this.requestStop({ by: 'Flow', reason: 'flow' })
      } catch (error) {
        if (signal.aborted) return
        if (error instanceof AuthError) {
          this.requestStop({ by: 'Flow', reason: 'revoked' })
          return
        }
        this.logger.warn('heartbeat failed', { error: errorMessage(error) })
      }
      try {
        await this.sleep(interval, signal)
      } catch {
        return
      }
    }
  }

  private async inboxLoop(): Promise<void> {
    const signal = this.loops.signal
    const wait = this.deps.inboxWaitSeconds ?? 25
    let failures = 0
    while (!signal.aborted) {
      try {
        const response = await this.api.inbox(this.inboxAfter, wait, signal)
        failures = 0
        for (const item of response.items ?? []) this.handleInboxItem(item)
      } catch (error) {
        if (signal.aborted) return
        if (error instanceof AuthError) {
          this.requestStop({ by: 'Flow', reason: 'revoked' })
          return
        }
        const delay = backoffDelay(failures++, 1000, 30_000)
        this.logger.warn('inbox poll failed', { error: errorMessage(error), delayMs: delay })
        try {
          await this.sleep(delay, signal)
        } catch {
          return
        }
      }
    }
  }

  /** Applies one inbox item (exported behaviour, tested directly). */
  handleInboxItem(item: InboxItem): void {
    if (item.seq > this.inboxAfter) this.inboxAfter = item.seq
    switch (item.kind) {
      case 'stop':
        this.requestStop({ by: stopperName(item.data), reason: 'user' })
        return
      case 'turn':
      case 'signal':
        if (item.signal === 'stop') {
          this.requestStop({ by: stopperName(item.data), reason: 'user' })
          return
        }
        if (item.turn && !this.seenTurns.has(item.turn.id)) {
          this.seenTurns.add(item.turn.id)
          this.turns.push(item.turn)
        } else if (!item.turn) {
          this.logger.info('inbox signal without a turn ignored', { seq: item.seq, signal: item.signal })
        }
        return
      default:
        this.logger.warn('unknown inbox item kind', { seq: item.seq, kind: item.kind })
    }
  }

  // ── preparation ────────────────────────────────────────────────────────

  private async prepare(): Promise<boolean> {
    this.setPhase('setup')
    await writeRuntimeInfo(this.config)
    const log = new SetupLog((line) => this.logger.info('setup', { line }))
    const setupStarted = Date.now()
    const deadline = setupStarted + Math.max(60, this.bootstrap.limits.setupTimeoutSeconds || 1200) * 1000
    const setupDeps = {
      bootstrap: this.bootstrap,
      paths: this.config.paths,
      git: this.git as Git,
      env: this.env(),
      log,
      emit: (event: RunnerEvent) => this.buffer.push(event),
      events: this.events,
      setupSecrets: () => this.api.setupSecrets(),
      deadline,
    }
    try {
      this.buffer.push(this.events.status('setup', this.bootstrap.restored ? 'Restoring workspace' : 'Preparing workspace'))
      await prepareRepository(setupDeps)
      await this.git.configureIdentity(this.bootstrap.git)
      if (!this.bootstrap.restored) await runSetup(setupDeps)
      if (this.resumeId) await this.harnessState.restore().catch((error) => this.logger.warn('restoring harness state failed', { error: errorMessage(error) }))
      if (this.deps.cliEnv !== false) {
        this.cliRefresher = new CliEnvRefresher({
          credentials: (request) => this.api.credentials(request),
          flowDir: this.config.paths.flowDir,
          home: this.config.paths.home,
          host: this.bootstrap.repo.hostName,
          codeHost: this.bootstrap.repo.codeHost,
          logger: this.logger,
        })
        await this.cliRefresher.start()
      }
      this.buffer.push(this.events.status('ready'))
      return true
    } catch (error) {
      if (error instanceof AuthError) throw error
      const message = error instanceof SetupError ? error.message : `Workspace preparation failed: ${errorMessage(error)}`
      const tail = error instanceof SetupError ? error.logTail : error instanceof CommandError ? error.result.stderr.slice(-2000) : ''
      const detail: ErrorDetail = { code: 'environmentSetupFailed', message: tail ? `${message}\n\n${tail}` : message }
      this.logger.error('setup failed', { error: message })
      this.buffer.push(this.events.error(detail))
      if (this.bootstrap.turn) {
        await this.complete(this.bootstrap.turn, { status: 'error', pushed: false, error: detail })
      }
      return false
    }
  }

  // ── turns ──────────────────────────────────────────────────────────────

  private async runTurn(turn: Turn): Promise<void> {
    this.setPhase('running', turn.id)
    this.logger.info('turn started', { turnId: turn.id, kind: turn.kind })
    const startHead = await this.git.head()
    await rm(elicitationMarker(this.config.paths.flowDir, turn.id), { force: true })

    let attempt = await this.runHarness(turn, this.resumeId)
    if (
      this.resumeId &&
      !this.stop &&
      !attempt.timedOut &&
      !attempt.outcome.completed &&
      resumeFailed(`${attempt.stderr}\n${attempt.outcome.error?.message ?? ''}`)
    ) {
      this.logger.warn('harness resume failed, starting a fresh harness session', { resumeId: this.resumeId })
      this.resumeId = undefined
      const fresh: Turn = {
        ...turn,
        prompt:
          'The previous agent session for this branch could not be resumed. Review the branch history (git log, git diff against the base) to see the work done so far, then handle this request:\n\n' +
          turn.prompt,
      }
      attempt = await this.runHarness(fresh, undefined)
    }
    const { outcome } = attempt
    if (outcome.harnessSessionId) this.resumeId = outcome.harnessSessionId

    let status: TurnStatus
    let error: ErrorDetail | undefined
    if (this.stop) {
      status = 'stopped'
    } else if (attempt.timedOut) {
      status = 'error'
      error = { code: 'turnTimeout', message: `The turn exceeded ${this.bootstrap.limits.turnTimeoutSeconds}s and was stopped.` }
    } else if (outcome.error) {
      status = 'error'
      error = outcome.error
    } else if (!outcome.completed) {
      status = 'error'
      const why = attempt.exitCode === 0 ? 'exited without finishing the turn' : `exited with code ${attempt.exitCode}`
      error = harnessError(`The agent ${why}.${attempt.stderr ? `\n${attempt.stderr.slice(-1500)}` : ''}`)
    } else if (outcome.elicited || existsSync(elicitationMarker(this.config.paths.flowDir, turn.id))) {
      status = 'awaitingInput'
    } else {
      status = 'success'
    }
    if (error && !outcome.error) this.buffer.push(this.events.error(error))

    await this.publish(turn, startHead, status, error, outcome)
  }

  private async runHarness(turn: Turn, resumeId: string | undefined): Promise<{ outcome: TurnOutcome; exitCode: number; timedOut: boolean; stderr: string }> {
    const ctx: HarnessContext = {
      bootstrap: this.bootstrap,
      token: this.deps.token(),
      paths: this.config.paths,
      turn,
      resumeId,
      guidance: runnerGuidance(this.bootstrap, this.config.paths.repo),
      mcp: mcpCommand(this.config, this.bootstrap, turn.id),
      env: this.env(),
    }
    // MCP tools (track I) read the turn from FLOW_TURN_ID or this file.
    await mkdir(this.config.paths.flowDir, { recursive: true, mode: 0o700 })
    await writeFile(join(this.config.paths.flowDir, 'current-turn'), turn.id, { mode: 0o600 })
    const invocation = this.harness.invocation(ctx)
    for (const file of invocation.files) {
      await mkdir(dirname(file.path), { recursive: true, mode: 0o700 })
      await writeFile(file.path, file.content, { mode: file.mode ?? 0o600 })
      await chmod(file.path, file.mode ?? 0o600)
    }
    const mapper = this.harness.createMapper({ turnId: turn.id, cwd: this.config.paths.repo, model: this.bootstrap.harness.model, newId: this.newId, now: this.deps.now })
    const stderr: string[] = []
    const timeoutMs = Math.max(1, this.bootstrap.limits.turnTimeoutSeconds || 3600) * 1000
    let timedOut = false
    const proc = this.spawn(invocation.command, invocation.args, {
      cwd: invocation.cwd,
      env: invocation.env,
      stdin: invocation.stdin,
      pidsLimit: this.config.pidsLimit,
      onStdoutLine: (line) => {
        const trimmed = line.trim()
        if (!trimmed.startsWith('{')) {
          if (trimmed) this.logger.debug('harness stdout', { line: trimmed.slice(0, 500) })
          return
        }
        let record: unknown
        try {
          record = JSON.parse(trimmed)
        } catch {
          this.logger.debug('harness emitted invalid JSON', { line: trimmed.slice(0, 200) })
          return
        }
        try {
          const events = mapper.map(record)
          if (events.length) this.buffer.push(...events)
        } catch (error) {
          this.logger.warn('mapping a harness event failed', { error: errorMessage(error) })
        }
      },
      onStderrLine: (line) => {
        stderr.push(line)
        if (stderr.length > STDERR_TAIL_LINES) stderr.shift()
        this.logger.debug('harness stderr', { line: line.slice(0, 500) })
      },
    })
    this.current = proc
    if (this.stop) void proc.interrupt(this.stopGraceMs())
    const timer = setTimeout(() => {
      timedOut = true
      this.logger.warn('turn timed out, interrupting the harness', { turnId: turn.id })
      void proc.interrupt(this.stopGraceMs())
    }, timeoutMs)
    timer.unref?.()
    const exit = await proc.exited
    clearTimeout(timer)
    this.current = undefined
    const tailEvents = mapper.finish()
    if (tailEvents.length) this.buffer.push(...tailEvents)
    const exitCode = exit.code ?? (exit.signal ? 128 : 1)
    this.logger.info('harness exited', { turnId: turn.id, exitCode, signal: exit.signal, timedOut })
    return { outcome: mapper.outcome(), exitCode, timedOut, stderr: stderr.join('\n') }
  }

  private async publish(turn: Turn, startHead: string, initialStatus: TurnStatus, initialError: ErrorDetail | undefined, outcome: TurnOutcome): Promise<void> {
    this.setPhase('publishing', turn.id)
    let status = initialStatus
    let error = initialError
    const repo = this.bootstrap.repo
    const identity = this.bootstrap.git
    const signed = Boolean(identity.signingFormat && identity.signingPublicKey)
    const summary = outcome.finalText ?? ''
    let message: string
    if (status === 'stopped') message = commitMessage(`WIP: stopped by ${this.stop?.by || 'user'}`, identity.coAuthorTrailer)
    else if (status === 'error') message = commitMessage(`WIP: ${error?.code === 'turnTimeout' ? 'turn timed out' : 'turn ended with an error'}`, identity.coAuthorTrailer)
    else message = commitMessage(summary, identity.coAuthorTrailer, status === 'awaitingInput' ? 'WIP: waiting for input' : undefined)

    const fail = (detail: ErrorDetail) => {
      this.buffer.push(this.events.error(detail))
      if (status === 'success' || status === 'awaitingInput') {
        status = 'error'
        error = detail
      }
    }

    try {
      await this.git.commitAll(message, identity)
    } catch (commitError) {
      this.logger.error('commit failed', { error: errorMessage(commitError) })
      fail({ code: isSigningFailure(commitError) ? 'signingFailed' : 'internal', message: `Committing failed: ${errorMessage(commitError)}` })
    }

    const commits: CodingCommit[] = await this.git.commitsSince(startHead, repo.branchName, turn.id, signed).catch(() => [])
    let pushed = false
    if (commits.length > 0) {
      try {
        checkBranchAllowed(repo, this.config.branchPrefix)
        await this.pushWithRetry(repo.branchName, turn.kind === 'rebase')
        pushed = true
        this.buffer.push(this.events.action({ action: 'Pushed branch', parameter: repo.branchName }))
      } catch (pushError) {
        const detail = pushError instanceof CommandError ? pushError.result.stderr.trim().slice(-2000) || pushError.message : errorMessage(pushError)
        this.logger.error('push failed', { error: detail })
        fail({ code: 'pushFailed', message: `Pushing ${repo.branchName} failed: ${detail}` })
      }
    }

    const headSha = await this.git.head()
    const diff: CodingDiffSummary[] = await this.git.diffSummary(this.git.baseRevision(repo)).catch(() => [])
    await this.harnessState.save().catch((saveError) => this.logger.warn('saving harness state failed', { error: errorMessage(saveError) }))

    const request: TurnCompleteRequest = {
      status,
      summary: summary || undefined,
      displayTitle: summary ? message.split('\n')[0] : undefined,
      harnessSessionId: this.resumeId,
      commits: commits.length ? commits : undefined,
      pushed,
      headSha: headSha || undefined,
      diff: diff.length ? diff : undefined,
      error,
    }
    await this.complete(turn, request)
  }

  private async pushWithRetry(branch: string, forceWithLease: boolean): Promise<void> {
    const attempts = this.deps.pushRetries ?? 3
    for (let attempt = 0; ; attempt++) {
      try {
        await this.git.push(branch, { forceWithLease })
        return
      } catch (error) {
        const stderr = error instanceof CommandError ? error.result.stderr : ''
        const permanent = /rejected|non-fast-forward|protected branch|pre-receive hook|permission|denied|403/i.test(stderr)
        if (permanent || attempt + 1 >= attempts) throw error
        await this.sleep(backoffDelay(attempt, 2000, 20_000))
      }
    }
  }

  private async complete(turn: Turn, request: TurnCompleteRequest): Promise<void> {
    const drained = await this.buffer.flush(60_000)
    if (!drained) this.logger.warn('events still pending at turn completion', { pending: this.buffer.pending })
    this.logger.info('turn complete', { turnId: turn.id, status: request.status, pushed: request.pushed, error: request.error?.code })
    await this.api.completeTurn(turn.id, request)
    this.reports.push({ turnId: turn.id, ...request })
    this.setPhase('idle')
  }
}

function stopperName(data: Record<string, unknown> | undefined): string {
  for (const key of ['by', 'userName', 'actorName', 'name']) {
    const value = data?.[key]
    if (typeof value === 'string' && value.trim()) return value.trim().slice(0, 80)
  }
  return 'user'
}

/** Harness session files ↔ GET/PUT /harness-state (tar.zst, at most 64 MiB). */
export function defaultHarnessState(api: SessionApi, home: string, harness: HarnessAdapter, logger: Logger): HarnessStateStore {
  return {
    async restore() {
      const archive = await api.getHarnessState()
      if (!archive) return
      await extractTarZst(archive, home)
      logger.info('harness state restored', { bytes: archive.length })
    },
    async save() {
      const archive = await tarZstBuffer(home, harness.stateDirs(), HARNESS_STATE_MAX_BYTES)
      if (!archive) return
      await api.putHarnessState(archive)
    },
  }
}

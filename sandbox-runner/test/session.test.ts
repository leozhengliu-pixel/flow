import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { test } from 'node:test'
import { sleep } from '../src/backoff.js'
import type { RunnerConfig } from '../src/config.js'
import { harnessFor } from '../src/harness/index.js'
import type { ExitResult, RunningProcess, SpawnOptions } from '../src/process.js'
import type {
  BootstrapResponse,
  CodingCommit,
  CredentialRequest,
  HeartbeatRequest,
  InboxItem,
  RunnerEvent,
  TurnCompleteRequest,
} from '../src/protocol.js'
import { RuntimeError } from '../src/runtime-client.js'
import { Session, type SessionApi, type SessionGit } from '../src/session.js'
import { createLogger } from '../src/log.js'
import { tempDir } from './helpers.js'

// Session orchestration with fakes: harness process, git, runtime API.
// Covers turns, follow-ups with resume, inbox stop, heartbeat stop, elicitation,
// timeouts, harness failures, branch policy and buffering while Flow is down.

const fixtures = join(import.meta.dirname, 'fixtures')

class FakeApi implements SessionApi {
  events: RunnerEvent[] = []
  completes: Array<{ turnId: string; request: TurnCompleteRequest }> = []
  heartbeats: HeartbeatRequest[] = []
  heartbeatStop = false
  failEvents = 0
  private inboxItems: InboxItem[] = []
  private inboxWaiters: Array<() => void> = []
  onComplete?: (turnId: string) => void

  pushInbox(item: InboxItem) {
    this.inboxItems.push(item)
    for (const wake of this.inboxWaiters.splice(0)) wake()
  }

  async heartbeat(request: HeartbeatRequest) {
    this.heartbeats.push(request)
    return { stop: this.heartbeatStop }
  }

  async postEvents(events: RunnerEvent[]) {
    if (this.failEvents > 0) {
      this.failEvents--
      throw new RuntimeError('POST /events: connect ECONNREFUSED', 0, true)
    }
    this.events.push(...events)
  }

  async inbox(after: number, _wait: number, signal?: AbortSignal) {
    for (;;) {
      const items = this.inboxItems.filter((item) => item.seq > after)
      if (items.length) return { items }
      await new Promise<void>((resolve, reject) => {
        this.inboxWaiters.push(resolve)
        signal?.addEventListener('abort', () => reject(signal.reason), { once: true })
      })
    }
  }

  async completeTurn(turnId: string, request: TurnCompleteRequest) {
    this.completes.push({ turnId, request })
    this.onComplete?.(turnId)
  }

  async credentials(_request: CredentialRequest) {
    return { username: 'x', password: 'y', expiresAt: new Date(Date.now() + 3600_000).toISOString() }
  }

  async setupSecrets() {
    return { variables: [] }
  }

  async getHarnessState() {
    return null
  }

  async putHarnessState() {}
}

class FakeGit {
  repoDir = '/workspace/repo'
  headSha = 'base000'
  dirty = false
  commitMessages: string[] = []
  pushes: Array<{ branch: string; forceWithLease?: boolean }> = []
  pushError: Error | undefined
  private commitCount = 0
  async git() {
    return { code: 0, stdout: '', stderr: '', timedOut: false }
  }
  async clone() {}
  async isRepo() {
    return true
  }
  async configureCredentials() {}
  async configureIdentity() {}
  async fetch() {}
  async checkoutSessionBranch() {
    return { source: 'remote' as const }
  }
  baseRevision() {
    return 'origin/main'
  }
  async head() {
    return this.headSha
  }
  async commitAll(message: string) {
    if (!this.dirty) return ''
    this.dirty = false
    this.commitMessages.push(message)
    this.headSha = `sha${++this.commitCount}`
    return this.headSha
  }
  async commitsSince(since: string, branch: string, turnId: string | undefined, signed: boolean): Promise<CodingCommit[]> {
    if (this.headSha === since) return []
    return [{ sha: this.headSha, branch, turnActivityId: turnId, message: this.commitMessages.at(-1)?.split('\n')[0], signed, createdAt: '2026-10-18T00:00:00.000Z' }]
  }
  async push(branch: string, options: { forceWithLease?: boolean } = {}) {
    if (this.pushError) throw this.pushError
    this.pushes.push({ branch, ...options })
  }
  async diffSummary() {
    return [{ path: 'hello.txt', additions: 1, deletions: 0, status: 'added' as const }]
  }
}

interface Spawned {
  command: string
  args: string[]
  options: SpawnOptions
  interrupted: boolean
}

/** A fake harness: plays a fixture, or hangs until interrupted. */
function fakeSpawner(script: (index: number) => { fixture?: string; hang?: boolean; exitCode?: number; stderr?: string; onStart?: () => void }) {
  const spawned: Spawned[] = []
  const spawn = (command: string, args: string[], options: SpawnOptions): RunningProcess => {
    const index = spawned.length
    const entry: Spawned = { command, args, options, interrupted: false }
    spawned.push(entry)
    const plan = script(index)
    let finish!: (result: ExitResult) => void
    const exited = new Promise<ExitResult>((resolve) => (finish = resolve))
    setImmediate(() => {
      plan.onStart?.()
      if (plan.fixture) {
        for (const line of readFileSync(join(fixtures, plan.fixture), 'utf8').split('\n')) if (line) options.onStdoutLine?.(line)
      }
      if (plan.stderr) options.onStderrLine?.(plan.stderr)
      if (!plan.hang) finish({ code: plan.exitCode ?? 0, signal: null, timedOut: false })
    })
    return {
      pid: 1000 + index,
      exited,
      interrupt: async () => {
        entry.interrupted = true
        finish({ code: null, signal: 'SIGINT', timedOut: false })
        return exited
      },
      kill: () => finish({ code: null, signal: 'SIGKILL', timedOut: false }),
    }
  }
  return { spawn, spawned }
}

function bootstrap(overrides: Partial<BootstrapResponse> = {}): BootstrapResponse {
  return {
    sandboxId: 'sbx1',
    sessionId: 'ses1',
    mode: 'session',
    restored: true,
    repo: { codeHost: 'github', hostName: 'github.com', fullName: 'acme/app', cloneUrl: 'https://github.com/acme/app.git', baseRef: 'main', branchName: 'flow/fix-dates', existingBranch: false },
    harness: { name: 'claudeCode', model: 'claude-sonnet-4-5' },
    llm: { anthropicBaseUrl: 'http://flow/internal/llm-proxy/anthropic', openaiBaseUrl: 'http://flow/internal/llm-proxy/openai/v1', openSourceBaseUrl: 'http://flow/internal/llm-proxy/opensource/v1' },
    environment: { mise: {}, dependencyManagement: 'custom-only', hasSecrets: false },
    git: { authorName: 'Ada', authorEmail: 'ada@example.com', committerName: 'flow[bot]', committerEmail: 'bot@example.com', coAuthorTrailer: 'Co-authored-by: Flow <bot@example.com>' },
    browser: { enabled: false },
    turn: { id: 'turn_1', prompt: 'Create hello.txt', kind: 'initial' },
    limits: { turnTimeoutSeconds: 600, heartbeatSeconds: 15, setupTimeoutSeconds: 600, stopGraceSeconds: 1 },
    guidance: 'Keep it small.',
    ...overrides,
  }
}

function setup(options: { bootstrap?: BootstrapResponse; script: Parameters<typeof fakeSpawner>[0]; heartbeatMs?: number; sleep?: (ms: number, signal?: AbortSignal) => Promise<void> }) {
  const tmp = tempDir('session')
  const config: RunnerConfig = {
    runtimeUrl: 'http://flow',
    tokenFile: join(tmp.path, 'token'),
    sandboxId: 'sbx1',
    mode: 'session',
    paths: { workspace: join(tmp.path, 'workspace'), repo: join(tmp.path, 'workspace', 'repo'), home: tmp.path, flowDir: join(tmp.path, '.flow'), binDir: '/opt/flow/bin' },
    branchPrefix: 'flow/',
    pidsLimit: 0,
  }
  const api = new FakeApi()
  const git = new FakeGit()
  const { spawn, spawned } = fakeSpawner(options.script)
  const boot = options.bootstrap ?? bootstrap()
  let n = 0
  const session = new Session({
    config,
    api,
    bootstrap: boot,
    git: git as unknown as SessionGit,
    harness: harnessFor(boot.harness.name),
    token: () => 'fcs_secret_token',
    spawn,
    cliEnv: false,
    harnessState: { restore: async () => {}, save: async () => {} },
    logger: process.env.TEST_DEBUG ? createLogger((line) => process.stderr.write(line + '\n'), 'debug') : undefined,
    newId: () => `ev${++n}`,
    heartbeatMs: options.heartbeatMs ?? 5,
    pushRetries: 1,
    sleep: options.sleep ?? ((ms, signal) => new Promise((resolve, reject) => {
      const timer = setTimeout(resolve, Math.min(ms, 5))
      signal?.addEventListener('abort', () => {
        clearTimeout(timer)
        reject(signal.reason)
      }, { once: true })
    })),
  })
  return { session, api, git, spawned, config, cleanup: tmp.cleanup }
}

test('runs the first turn, commits, pushes, completes, then resumes for a follow-up and stops', async () => {
  const t = setup({
    script: (index) => ({
      fixture: index === 0 ? 'claude-recorded-turn1.jsonl' : 'claude-recorded-turn2.jsonl',
      onStart: () => {
        t.git.dirty = index === 0
      },
    }),
  })
  t.api.onComplete = (turnId) => {
    if (turnId === 'turn_1') t.api.pushInbox({ seq: 1, kind: 'turn', turn: { id: 'turn_2', prompt: 'Which greeting?', kind: 'followUp' } })
    if (turnId === 'turn_2') t.api.pushInbox({ seq: 2, kind: 'stop', data: { by: 'Ada' } })
  }
  try {
    assert.equal(await t.session.run(), 0)
    assert.equal(t.spawned.length, 2)
    // First turn: harness command, proxy env, no provider keys, prompt on stdin.
    const first = t.spawned[0]
    assert.equal(first.command, 'claude')
    assert.ok(first.args.includes('stream-json') && first.args.includes('bypassPermissions'))
    assert.ok(!first.args.includes('--resume'))
    assert.equal(first.options.env?.ANTHROPIC_AUTH_TOKEN, 'fcs_secret_token')
    assert.equal(first.options.env?.ANTHROPIC_BASE_URL, 'http://flow/internal/llm-proxy/anthropic')
    assert.equal(first.options.env?.FLOW_SANDBOX, '1')
    assert.equal(first.options.env?.LINEAR_SANDBOX, '1')
    assert.equal(first.options.env?.FLOW_SNAPSHOT, undefined)
    assert.equal(first.options.stdin, 'Create hello.txt')
    // Follow-up resumes the Claude session from system/init.
    const sessionId = JSON.parse(readFileSync(join(fixtures, 'claude-recorded-turn1.jsonl'), 'utf8').split('\n')[0]).session_id
    const second = t.spawned[1]
    assert.deepEqual(second.args.slice(second.args.indexOf('--resume'), second.args.indexOf('--resume') + 2), ['--resume', sessionId])

    assert.equal(t.api.completes.length, 2)
    const [c1, c2] = t.api.completes
    assert.equal(c1.turnId, 'turn_1')
    assert.equal(c1.request.status, 'success')
    assert.equal(c1.request.pushed, true)
    assert.equal(c1.request.headSha, 'sha1')
    assert.equal(c1.request.harnessSessionId, sessionId)
    assert.equal(c1.request.commits?.[0].turnActivityId, 'turn_1')
    assert.equal(c1.request.summary, 'Created `hello.txt` containing a greeting.\n\nThe file prints `hello`.')
    assert.equal(t.git.commitMessages[0], 'Created `hello.txt` containing a greeting.\n\nCo-authored-by: Flow <bot@example.com>\n')
    assert.equal(c1.request.displayTitle, 'Created `hello.txt` containing a greeting.')
    assert.deepEqual(t.git.pushes, [{ branch: 'flow/fix-dates', forceWithLease: false }])
    assert.equal(c2.request.status, 'awaitingInput', 'ask_user ends the turn awaiting input')
    assert.equal(c2.request.pushed, false, 'nothing new to push')

    // Events were delivered before each completion, in order, with ids.
    const types = t.api.events.map((event) => event.type)
    assert.ok(types.includes('response') && types.includes('elicitation') && types.includes('status'))
    const responseIndex = t.api.events.findIndex((event) => event.type === 'response')
    assert.equal(t.api.events[responseIndex].turnId, 'turn_1')
    assert.ok(t.api.events.some((event) => event.type === 'action' && event.action?.action === 'Pushed branch'))
    assert.ok(t.api.heartbeats.length > 0)
    assert.ok(!JSON.stringify(t.api.events).includes('fcs_secret_token'))
  } finally {
    t.cleanup()
  }
})

test('a stop from the inbox interrupts the harness, pushes a WIP commit and completes as stopped', async () => {
  const t = setup({
    script: () => ({
      hang: true,
      onStart: () => {
        t.git.dirty = true
        setTimeout(() => t.api.pushInbox({ seq: 7, kind: 'stop', data: { by: 'Grace Hopper' } }), 10)
      },
    }),
  })
  try {
    assert.equal(await t.session.run(), 0)
    assert.equal(t.spawned[0].interrupted, true)
    assert.equal(t.api.completes.length, 1)
    assert.equal(t.api.completes[0].request.status, 'stopped')
    assert.equal(t.api.completes[0].request.pushed, true)
    assert.equal(t.git.commitMessages[0].split('\n')[0], 'WIP: stopped by Grace Hopper')
  } finally {
    t.cleanup()
  }
})

test('phase changes are reported at once, not at the next heartbeat interval', { timeout: 10_000 }, async () => {
  const t = setup({ heartbeatMs: 60_000, sleep, script: () => ({ fixture: 'claude-recorded-turn1.jsonl' }) })
  t.api.onComplete = () => {
    t.api.heartbeatStop = true
  }
  try {
    assert.equal(await t.session.run(), 0)
    const phases = t.api.heartbeats.map((heartbeat) => heartbeat.phase)
    assert.ok(phases.includes('running'), `phases: ${phases.join(',')}`)
    assert.ok(t.api.heartbeats.some((heartbeat) => heartbeat.phase === 'running' && heartbeat.turnId === 'turn_1'))
    assert.ok(phases.includes('idle'), `phases: ${phases.join(',')}`)
  } finally {
    t.cleanup()
  }
})

test('a follow-up whose harness conversation is gone starts a fresh harness session (recorded Claude Code error)', async () => {
  const t = setup({
    bootstrap: bootstrap({ harness: { name: 'claudeCode', model: 'claude-sonnet-4-5', resumeId: '0b6c4f1e-1111-4222-8333-444455556666' }, turn: { id: 'turn_1', prompt: 'Add a JSDoc line', kind: 'followUp' } }),
    script: (index) =>
      index === 0
        ? { fixture: 'claude-recorded-resume-missing.jsonl', stderr: 'No conversation found with session ID: 0b6c4f1e-1111-4222-8333-444455556666', exitCode: 1 }
        : { fixture: 'claude-recorded-turn1.jsonl' },
  })
  t.api.onComplete = () => {
    t.api.heartbeatStop = true
  }
  try {
    assert.equal(await t.session.run(), 0)
    assert.equal(t.spawned.length, 2)
    assert.ok(t.spawned[0].args.includes('--resume'))
    assert.ok(!t.spawned[1].args.includes('--resume'), 'the retry starts a fresh conversation')
    assert.match(String(t.spawned[1].options.stdin), /could not be resumed[\s\S]*Add a JSDoc line/)
    assert.equal(t.api.completes.length, 1)
    assert.equal(t.api.completes[0].request.status, 'success')
  } finally {
    t.cleanup()
  }
})

test('a heartbeat stop while idle exits without another turn', async () => {
  const t = setup({ bootstrap: bootstrap({ turn: undefined }), script: () => ({ fixture: 'claude-recorded-turn1.jsonl' }) })
  setTimeout(() => (t.api.heartbeatStop = true), 20)
  try {
    assert.equal(await t.session.run(), 0)
    assert.equal(t.spawned.length, 0)
    assert.equal(t.api.completes.length, 0)
    assert.ok(t.api.heartbeats.some((heartbeat) => heartbeat.phase === 'idle'))
  } finally {
    t.cleanup()
  }
})

test('inbox: duplicate turns are ignored, signals without a turn are ignored', async () => {
  const t = setup({ bootstrap: bootstrap({ turn: undefined }), script: () => ({ fixture: 'claude-recorded-turn1.jsonl' }) })
  try {
    t.session.handleInboxItem({ seq: 3, kind: 'signal', signal: 'continue' })
    t.session.handleInboxItem({ seq: 4, kind: 'turn', turn: { id: 'turn_9', prompt: 'x', kind: 'followUp' } })
    t.session.handleInboxItem({ seq: 5, kind: 'turn', turn: { id: 'turn_9', prompt: 'x', kind: 'followUp' } })
    t.session.handleInboxItem({ seq: 6, kind: 'signal', signal: 'stop' })
    assert.equal(t.session.stopping, true)
  } finally {
    t.cleanup()
  }
})

test('turn timeout interrupts the harness and reports turnTimeout', async () => {
  const t = setup({ bootstrap: bootstrap({ limits: { turnTimeoutSeconds: 1, heartbeatSeconds: 15, setupTimeoutSeconds: 60, stopGraceSeconds: 1 } }), script: () => ({ hang: true }) })
  t.api.onComplete = () => t.api.pushInbox({ seq: 1, kind: 'stop' })
  try {
    assert.equal(await t.session.run(), 0)
    assert.equal(t.spawned[0].interrupted, true)
    assert.equal(t.api.completes[0].request.status, 'error')
    assert.equal(t.api.completes[0].request.error?.code, 'turnTimeout')
  } finally {
    t.cleanup()
  }
})

test('a harness that exits without a result fails the turn with harnessFailed', async () => {
  const t = setup({ script: () => ({ exitCode: 1, stderr: 'Error: something broke' }) })
  t.api.onComplete = () => t.api.pushInbox({ seq: 1, kind: 'stop' })
  try {
    await t.session.run()
    const request = t.api.completes[0].request
    assert.equal(request.status, 'error')
    assert.equal(request.error?.code, 'harnessFailed')
    assert.match(request.error?.message ?? '', /exited with code 1[\s\S]*something broke/)
    assert.ok(t.api.events.some((event) => event.type === 'error'))
  } finally {
    t.cleanup()
  }
})

test('quota errors from the stream surface as quotaExceeded', async () => {
  const t = setup({ script: () => ({ fixture: 'claude-error.jsonl', exitCode: 1 }) })
  t.api.onComplete = () => t.api.pushInbox({ seq: 1, kind: 'stop' })
  try {
    await t.session.run()
    assert.equal(t.api.completes[0].request.error?.code, 'quotaExceeded')
  } finally {
    t.cleanup()
  }
})

test('branches outside the prefix are never pushed', async () => {
  const boot = bootstrap()
  boot.repo.branchName = 'main'
  const t = setup({ bootstrap: boot, script: () => ({ fixture: 'claude-recorded-turn1.jsonl', onStart: () => (t.git.dirty = true) }) })
  t.api.onComplete = () => t.api.pushInbox({ seq: 1, kind: 'stop' })
  try {
    await t.session.run()
    assert.deepEqual(t.git.pushes, [])
    assert.equal(t.api.completes[0].request.status, 'error')
    assert.equal(t.api.completes[0].request.error?.code, 'pushFailed')
    assert.equal(t.api.completes[0].request.pushed, false)
  } finally {
    t.cleanup()
  }
})

test('push failures report pushFailed with git stderr', async () => {
  const t = setup({ script: () => ({ fixture: 'claude-recorded-turn1.jsonl', onStart: () => (t.git.dirty = true) }) })
  t.git.pushError = new Error('remote: Permission to acme/app.git denied')
  t.api.onComplete = () => t.api.pushInbox({ seq: 1, kind: 'stop' })
  try {
    await t.session.run()
    assert.equal(t.api.completes[0].request.error?.code, 'pushFailed')
    assert.match(t.api.completes[0].request.error?.message ?? '', /denied/)
  } finally {
    t.cleanup()
  }
})

test('events buffered while Flow is unreachable are delivered before turn-complete', async () => {
  const t = setup({ script: () => ({ fixture: 'claude-tools.jsonl' }) })
  t.api.failEvents = 3
  let eventsAtComplete = -1
  t.api.onComplete = () => {
    eventsAtComplete = t.api.events.length
    t.api.pushInbox({ seq: 1, kind: 'stop' })
  }
  try {
    await t.session.run()
    assert.equal(t.api.completes[0].request.status, 'success')
    assert.ok(eventsAtComplete >= 18, `all turn events were posted first (${eventsAtComplete})`)
    const ids = t.api.events.map((event) => event.id)
    assert.equal(new Set(ids).size, ids.length, 'no duplicates after retries')
  } finally {
    t.cleanup()
  }
})

test('codex and opencode turns use their resume flags on follow-ups', async () => {
  for (const [name, fixture, flag] of [
    ['codex', 'codex-recorded-turn1.jsonl', 'resume'],
    ['openSource', 'opencode-recorded-turn1.jsonl', '--session'],
  ] as const) {
    const boot = bootstrap({ harness: { name, model: 'm1', reasoningEffort: 'high' } })
    const t = setup({ bootstrap: boot, script: () => ({ fixture }) })
    t.api.onComplete = (turnId) => {
      if (turnId === 'turn_1') t.api.pushInbox({ seq: 1, kind: 'turn', turn: { id: 'turn_2', prompt: 'more', kind: 'followUp' } })
      else t.api.pushInbox({ seq: 2, kind: 'stop' })
    }
    try {
      await t.session.run()
      assert.ok(!t.spawned[0].args.includes(flag), name)
      const index = t.spawned[1].args.indexOf(flag)
      assert.ok(index > 0, `${name} follow-up uses ${flag}`)
      assert.equal(t.spawned[1].args[index + 1], t.api.completes[0].request.harnessSessionId)
      assert.equal(t.spawned[0].options.env?.FLOW_LLM_TOKEN, 'fcs_secret_token')
      assert.equal(t.spawned[0].options.env?.OPENAI_API_KEY, undefined)
    } finally {
      t.cleanup()
    }
  }
})

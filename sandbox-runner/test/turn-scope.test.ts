import assert from 'node:assert/strict'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { test } from 'node:test'
import type { RunnerConfig } from '../src/config.js'
import { Git } from '../src/git/git.js'
import { harnessFor } from '../src/harness/index.js'
import type { ExitResult, RunningProcess, SpawnOptions } from '../src/process.js'
import type { BootstrapResponse, HarnessName, InboxItem, RunnerEvent, Turn, TurnCompleteRequest } from '../src/protocol.js'
import { Session, type SessionApi, type SessionGit } from '../src/session.js'
import { turnScope } from '../src/turn-scope.js'
import { sh, tempDir } from './helpers.js'

// A turn's write scope, end to end with a fake harness and real git: a
// temporary origin with the session branch already pushed (the existing
// draft PR's head), a real workspace clone, and the runner's publish stage
// observed through git (commit/push calls, origin's branch head) and the
// turn-complete requests Flow receives (which decide the PR update).

const fixtures = join(import.meta.dirname, 'fixtures')
const ID = ['-c', 'user.name=s', '-c', 'user.email=s@x']
const BRANCH = 'flow/abc-1-add-dark-mode-x1y2z3'
const OLD_ISSUE = 'ABC-1 Add dark mode\n\nAdd a dark theme toggle to the settings page.'

class Api implements SessionApi {
  events: RunnerEvent[] = []
  completes: Array<{ turnId: string; request: TurnCompleteRequest }> = []
  private items: InboxItem[] = []
  private waiters: Array<() => void> = []
  onComplete?: (turnId: string) => void
  push(item: InboxItem) {
    this.items.push(item)
    for (const wake of this.waiters.splice(0)) wake()
  }
  async heartbeat() {
    return { stop: false }
  }
  async postEvents(events: RunnerEvent[]) {
    this.events.push(...events)
  }
  async inbox(after: number, _wait: number, signal?: AbortSignal) {
    for (;;) {
      const items = this.items.filter((item) => item.seq > after)
      if (items.length) return { items }
      await new Promise<void>((resolve, reject) => {
        this.waiters.push(resolve)
        signal?.addEventListener('abort', () => reject(signal.reason), { once: true })
      })
    }
  }
  async completeTurn(turnId: string, request: TurnCompleteRequest) {
    this.completes.push({ turnId, request })
    this.onComplete?.(turnId)
  }
  async credentials() {
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

interface Plan {
  fixture: string
  /** What the "model" does in the workspace while the harness runs. */
  act?: (repo: string) => void
  stderr?: string
  exitCode?: number
}

interface Spawned {
  command: string
  args: string[]
  stdin: string
}

function world(options: { harness: HarnessName; resumeId?: string; turns: Turn[]; plans: Plan[]; dirty?: (repo: string) => void }) {
  const tmp = tempDir('scope')
  const env = { PATH: '/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin', HOME: tmp.path, GIT_CONFIG_NOSYSTEM: '1' }
  const origin = join(tmp.path, 'origin.git')
  const seed = join(tmp.path, 'seed')
  sh('git', ['init', '-q', '--bare', '-b', 'main', origin], { env })
  sh('git', ['init', '-q', '-b', 'main', seed], { env })
  writeFileSync(join(seed, 'README.md'), '# app\n')
  writeFileSync(join(seed, 'theme.ts'), 'export const theme = "light"\n')
  sh('git', [...ID, 'add', '.'], { cwd: seed, env })
  sh('git', [...ID, 'commit', '-q', '-m', 'init'], { cwd: seed, env })
  sh('git', ['push', '-q', origin, 'main'], { cwd: seed, env })
  // The earlier feature turns: the session branch and its draft PR exist.
  sh('git', ['checkout', '-q', '-b', BRANCH], { cwd: seed, env })
  writeFileSync(join(seed, 'theme.ts'), 'export const theme = "dark"\n')
  sh('git', [...ID, 'commit', '-q', '-am', 'Add dark mode'], { cwd: seed, env })
  sh('git', ['push', '-q', origin, BRANCH], { cwd: seed, env })
  const prHead = sh('git', ['rev-parse', 'HEAD'], { cwd: seed, env }).trim()

  const repoDir = join(tmp.path, 'workspace', 'repo')
  mkdirSync(join(tmp.path, 'workspace'))
  sh('git', ['clone', '-q', '-b', BRANCH, origin, repoDir], { env })
  options.dirty?.(repoDir)

  const real = new Git({ repoDir, env, credentialHelper: '/bin/false', signProgram: 's' })
  const calls = { commitAll: 0, commitPaths: 0, push: 0 }
  const git = new Proxy(real, {
    get(target, key, receiver) {
      const value = Reflect.get(target, key, receiver)
      if (typeof value !== 'function') return value
      return (...args: unknown[]) => {
        if (key === 'commitAll' || key === 'commitPaths' || key === 'push') calls[key]++
        return value.apply(target, args)
      }
    },
  })

  const config: RunnerConfig = {
    runtimeUrl: 'http://flow',
    tokenFile: join(tmp.path, 'token'),
    sandboxId: 'sbx1',
    mode: 'session',
    paths: { workspace: join(tmp.path, 'workspace'), repo: repoDir, home: tmp.path, flowDir: join(tmp.path, '.flow'), binDir: '/opt/flow/bin' },
    branchPrefix: 'flow/',
    pidsLimit: 0,
  }
  const boot: BootstrapResponse = {
    sandboxId: 'sbx1',
    sessionId: 'ses1',
    mode: 'session',
    restored: true,
    repo: { codeHost: 'github', hostName: 'github.com', fullName: 'acme/app', cloneUrl: origin, baseRef: 'main', branchName: BRANCH, existingBranch: true, branchPrefix: 'flow/' },
    harness: { name: options.harness, model: 'm1', resumeId: options.resumeId },
    llm: { anthropicBaseUrl: 'http://flow/internal/llm-proxy/anthropic', openaiBaseUrl: 'http://flow/internal/llm-proxy/openai/v1', openSourceBaseUrl: 'http://flow/internal/llm-proxy/opensource/v1' },
    environment: { mise: {}, dependencyManagement: 'custom-only', hasSecrets: false },
    git: { authorName: 'Ada', authorEmail: 'ada@example.com', committerName: 'flow[bot]', committerEmail: 'bot@example.com', coAuthorTrailer: 'Co-Authored-By: flow[bot] <bot@example.com>' },
    browser: { enabled: false },
    turn: options.turns[0],
    limits: { turnTimeoutSeconds: 600, heartbeatSeconds: 15, setupTimeoutSeconds: 600, stopGraceSeconds: 1 },
    // Flow's bootstrap guidance carries the issue the session started for.
    guidance: `## Issue (background)\n\n${OLD_ISSUE}`,
  }
  const api = new Api()
  api.onComplete = (turnId) => {
    const index = options.turns.findIndex((turn) => turn.id === turnId)
    const next = options.turns[index + 1]
    if (next) api.push({ seq: index + 1, kind: 'turn', turn: next })
    else api.push({ seq: 99, kind: 'stop' })
  }
  const spawned: Spawned[] = []
  const spawn = (command: string, args: string[], spawnOptions: SpawnOptions): RunningProcess => {
    const plan = options.plans[spawned.length]
    spawned.push({ command, args, stdin: String(spawnOptions.stdin ?? '') })
    let finish!: (result: ExitResult) => void
    const exited = new Promise<ExitResult>((resolve) => (finish = resolve))
    setImmediate(() => {
      plan.act?.(repoDir)
      for (const line of readFileSync(join(fixtures, plan.fixture), 'utf8').split('\n')) if (line) spawnOptions.onStdoutLine?.(line)
      if (plan.stderr) spawnOptions.onStderrLine?.(plan.stderr)
      finish({ code: plan.exitCode ?? 0, signal: null, timedOut: false })
    })
    return { pid: 1, exited, interrupt: async () => exited, kill: () => {} }
  }
  let n = 0
  const session = new Session({
    config,
    api,
    bootstrap: boot,
    git: git as unknown as SessionGit,
    harness: harnessFor(options.harness),
    token: () => 'fcs_token',
    spawn,
    cliEnv: false,
    harnessState: { restore: async () => {}, save: async () => {} },
    newId: () => `ev${++n}`,
    heartbeatMs: 5,
    pushRetries: 1,
    parentEnv: env,
    sleep: (ms, signal) => new Promise((resolve, reject) => {
      const timer = setTimeout(resolve, Math.min(ms, 5))
      signal?.addEventListener('abort', () => {
        clearTimeout(timer)
        reject(signal.reason)
      }, { once: true })
    }),
  })
  const inRepo = (args: string[]) => sh('git', args, { cwd: repoDir, env }).trim()
  const remoteHead = () => sh('git', ['rev-parse', `refs/heads/${BRANCH}`], { cwd: origin, env }).trim()
  return { session, api, spawned, calls, config, repoDir, prHead, inRepo, remoteHead, cleanup: tmp.cleanup }
}

const VERIFY = 'Verify that the restored workspace is intact: run the checks and report. Keep the repo and the existing PR unchanged.'

test('scope: the latest request decides; keep-unchanged requests are read-only, explicit dev work is write', () => {
  const turn = (prompt: string, kind = 'followUp'): Turn => ({ id: 't', prompt, kind })
  for (const prompt of [
    VERIFY,
    'Only verify the restored state. Do not make any changes.',
    'Please leave the branch and the pull request as is and just report what you see.',
    "Don't modify anything, read-only check of the build.",
    '只验证恢复后的状态，保持仓库和现有 PR 不变。',
    '检查一下测试是否通过，不要做任何修改。',
  ]) assert.equal(turnScope(turn(prompt)), 'readOnly', prompt)
  for (const prompt of ['Try the refactor locally but do not commit or push yet.', '先改一下，但不要提交。']) assert.equal(turnScope(turn(prompt)), 'noPublish', prompt)
  for (const prompt of ['Continue with the dark mode toggle and add tests.', 'Fix the failing test in theme.ts', 'Rename the API but keep the return type unchanged']) {
    assert.equal(turnScope(turn(prompt)), 'write', prompt)
  }
  // Flow's own branch-changing turns always write, whatever their text says.
  assert.equal(turnScope(turn('Rebase onto main; keep the branch unchanged otherwise', 'rebase')), 'write')
})

test('read-only follow-up on a resumed codex session with an existing PR: nothing is committed or pushed, the PR head stays', async () => {
  const w = world({
    harness: 'codex',
    resumeId: 'thread-old',
    turns: [{ id: 'turn_verify', prompt: VERIFY, kind: 'followUp' }],
    // The "model" only reads, writing its scratch output to a temporary file in the repo.
    plans: [{ fixture: 'codex-recorded-turn1.jsonl', act: (repo) => writeFileSync(join(repo, '.verify-output.txt'), 'all checks passed\n') }],
  })
  try {
    const headBefore = w.inRepo(['rev-parse', 'HEAD'])
    assert.equal(headBefore, w.prHead)
    assert.equal(await w.session.run(), 0)

    // The invocation: codex resumes the old thread, and both the per-turn
    // instructions file and the stdin prompt carry the current request's scope.
    const [call] = w.spawned
    assert.deepEqual(call.args.slice(0, 3), ['exec', 'resume', 'thread-old'])
    assert.ok(call.stdin.startsWith('[Flow: current request (followUp turn)]'), call.stdin)
    assert.ok(call.stdin.endsWith(VERIFY))
    assert.match(call.stdin, /Write scope: read-only/)
    const instructions = readFileSync(join(w.config.paths.home, '.codex', 'AGENTS.override.md'), 'utf8')
    assert.ok(instructions.indexOf('## Current turn') > instructions.indexOf('## Issue (background)'), 'the current turn comes after the old issue')
    assert.match(instructions, /Write scope: read-only/)
    assert.doesNotMatch(instructions, /Its first line becomes the commit message/)

    // The publish stage: no commit, no push; HEAD, index and origin unchanged.
    assert.deepEqual(w.calls, { commitAll: 0, commitPaths: 0, push: 0 })
    assert.equal(w.inRepo(['rev-parse', 'HEAD']), headBefore)
    assert.equal(w.remoteHead(), w.prHead, 'the existing PR head is unchanged')
    assert.equal(w.inRepo(['diff', '--cached', '--name-only']), '')
    assert.equal(w.inRepo(['status', '--porcelain']), '?? .verify-output.txt', 'the temporary file is neither committed nor deleted')

    const [complete] = w.api.completes
    assert.equal(complete.request.status, 'success')
    assert.equal(complete.request.scope, 'readOnly')
    assert.equal(complete.request.pushed, false)
    assert.equal(complete.request.commits, undefined)
    assert.equal(complete.request.headSha, undefined, 'the session keeps the published head')
    assert.equal(complete.request.displayTitle, undefined, 'the session (PR) title is not replaced')
  } finally {
    w.cleanup()
  }
})

test('read-only follow-up where the model continues the old feature task: the turn fails, nothing is committed, pushed or reverted', async () => {
  const w = world({
    harness: 'claudeCode',
    resumeId: 'sess-old',
    turns: [{ id: 'turn_verify', prompt: VERIFY, kind: 'followUp' }],
    plans: [{ fixture: 'claude-recorded-turn1.jsonl', act: (repo) => writeFileSync(join(repo, 'theme.ts'), 'export const theme = "dark"\nexport const toggle = true\n') }],
  })
  try {
    assert.equal(await w.session.run(), 0)
    const [call] = w.spawned
    const system = call.args[call.args.indexOf('--append-system-prompt') + 1]
    assert.match(system, /## Current turn[\s\S]*Write scope: read-only/)
    assert.deepEqual(call.args.slice(call.args.indexOf('--resume'), call.args.indexOf('--resume') + 2), ['--resume', 'sess-old'])

    assert.deepEqual(w.calls, { commitAll: 0, commitPaths: 0, push: 0 })
    assert.equal(w.inRepo(['rev-parse', 'HEAD']), w.prHead)
    assert.equal(w.remoteHead(), w.prHead)
    assert.equal(readFileSync(join(w.repoDir, 'theme.ts'), 'utf8'), 'export const theme = "dark"\nexport const toggle = true\n', 'the change is kept for review')
    const [complete] = w.api.completes
    assert.equal(complete.request.status, 'error')
    assert.equal(complete.request.pushed, false)
    assert.equal(complete.request.error?.code, 'harnessFailed')
    assert.match(complete.request.error?.message ?? '', /keep the repository and the pull request unchanged[\s\S]*theme\.ts[\s\S]*did not commit or push/)
  } finally {
    w.cleanup()
  }
})

test('read-only follow-up whose harness session cannot be resumed: the fresh session gets the same scope, and nothing is published', async () => {
  const w = world({
    harness: 'claudeCode',
    resumeId: '0b6c4f1e-1111-4222-8333-444455556666',
    turns: [{ id: 'turn_verify', prompt: VERIFY, kind: 'followUp' }],
    plans: [
      { fixture: 'claude-recorded-resume-missing.jsonl', stderr: 'No conversation found with session ID: 0b6c4f1e-1111-4222-8333-444455556666', exitCode: 1 },
      { fixture: 'claude-recorded-turn1.jsonl' },
    ],
  })
  try {
    assert.equal(await w.session.run(), 0)
    assert.equal(w.spawned.length, 2)
    const fresh = w.spawned[1]
    assert.ok(!fresh.args.includes('--resume'))
    assert.match(fresh.stdin, /^\[Flow: current request[\s\S]*Write scope: read-only[\s\S]*could not be resumed[\s\S]*Keep the repo and the existing PR unchanged\.$/)
    assert.match(fresh.args[fresh.args.indexOf('--append-system-prompt') + 1], /Write scope: read-only/)
    assert.deepEqual(w.calls, { commitAll: 0, commitPaths: 0, push: 0 })
    assert.equal(w.remoteHead(), w.prHead)
    assert.equal(w.api.completes[0].request.status, 'success')
    assert.equal(w.api.completes[0].request.scope, 'readOnly')
  } finally {
    w.cleanup()
  }
})

test('a dirty restored workspace: a development turn commits only its own changes; leftovers stay uncommitted and unchanged', async () => {
  const w = world({
    harness: 'claudeCode',
    resumeId: 'sess-old',
    turns: [{ id: 'turn_dev', prompt: 'Add a CONTRIBUTING.md with one line.', kind: 'followUp' }],
    plans: [{ fixture: 'claude-recorded-turn1.jsonl', act: (repo) => writeFileSync(join(repo, 'CONTRIBUTING.md'), 'Be kind.\n') }],
    dirty: (repo) => {
      // Left over from before this turn: an unstaged edit and a staged new file.
      writeFileSync(join(repo, 'README.md'), '# app (local edit)\n')
      writeFileSync(join(repo, 'staged.txt'), 'staged\n')
      sh('git', ['add', 'staged.txt'], { cwd: repo })
    },
  })
  try {
    assert.equal(await w.session.run(), 0)
    const [call] = w.spawned
    const system = call.args[call.args.indexOf('--append-system-prompt') + 1]
    assert.match(system, /already had uncommitted changes before this turn \(README\.md, staged\.txt\)/)

    assert.deepEqual(w.calls, { commitAll: 0, commitPaths: 1, push: 1 })
    const head = w.inRepo(['rev-parse', 'HEAD'])
    assert.notEqual(head, w.prHead)
    assert.equal(w.remoteHead(), head, 'the turn was pushed')
    assert.equal(w.inRepo(['diff-tree', '--no-commit-id', '--name-only', '-r', 'HEAD']), 'CONTRIBUTING.md', 'only the turn\'s own file')
    assert.equal(readFileSync(join(w.repoDir, 'README.md'), 'utf8'), '# app (local edit)\n')
    assert.equal(w.inRepo(['diff', '--name-only']), 'README.md', 'the leftover edit is still unstaged')
    assert.equal(w.inRepo(['diff', '--cached', '--name-only']), 'staged.txt', 'the leftover staged file is still staged, not committed')
    const [complete] = w.api.completes
    assert.equal(complete.request.status, 'success')
    assert.equal(complete.request.pushed, true)
    assert.equal(complete.request.scope, 'write')
    assert.ok(w.api.events.some((event) => event.type === 'thought' && /did not commit 2 path/.test(event.body ?? '')))
  } finally {
    w.cleanup()
  }
})

test('read-only turn on a dirty workspace: leftovers are neither committed nor flagged as this turn\'s changes', async () => {
  const w = world({
    harness: 'openSource',
    resumeId: 'ses_old',
    turns: [{ id: 'turn_verify', prompt: VERIFY, kind: 'followUp' }],
    plans: [{ fixture: 'opencode-recorded-turn1.jsonl' }],
    dirty: (repo) => writeFileSync(join(repo, 'README.md'), '# app (local edit)\n'),
  })
  try {
    assert.equal(await w.session.run(), 0)
    const [call] = w.spawned
    assert.deepEqual(call.args.slice(call.args.indexOf('--session'), call.args.indexOf('--session') + 2), ['--session', 'ses_old'])
    const guidance = readFileSync(join(w.config.paths.flowDir, 'guidance.md'), 'utf8')
    assert.match(guidance, /Write scope: read-only[\s\S]*already had uncommitted changes before this turn \(README\.md\)/)
    assert.deepEqual(w.calls, { commitAll: 0, commitPaths: 0, push: 0 })
    assert.equal(w.inRepo(['status', '--porcelain']), 'M README.md')
    assert.equal(w.api.completes[0].request.status, 'success')
  } finally {
    w.cleanup()
  }
})

test('a normal development follow-up after a read-only turn still commits, pushes and reports the push for the draft PR', async () => {
  const w = world({
    harness: 'codex',
    resumeId: 'thread-old',
    turns: [
      { id: 'turn_verify', prompt: VERIFY, kind: 'followUp' },
      { id: 'turn_dev', prompt: 'Now continue the dark mode work: add the toggle export.', kind: 'followUp' },
    ],
    plans: [
      { fixture: 'codex-recorded-turn1.jsonl' },
      { fixture: 'codex-recorded-turn1.jsonl', act: (repo) => writeFileSync(join(repo, 'theme.ts'), 'export const theme = "dark"\nexport const toggle = true\n') },
    ],
  })
  try {
    assert.equal(await w.session.run(), 0)
    assert.equal(w.spawned.length, 2)
    assert.match(w.spawned[1].stdin, /Write scope: changes allowed; Flow commits and pushes/)
    assert.deepEqual(w.calls, { commitAll: 1, commitPaths: 0, push: 1 }, 'exactly one publish, for the development turn')
    const head = w.inRepo(['rev-parse', 'HEAD'])
    assert.equal(w.remoteHead(), head)
    assert.equal(w.inRepo(['status', '--porcelain']), '')
    const [verify, dev] = w.api.completes
    assert.equal(verify.request.pushed, false)
    assert.equal(verify.request.scope, 'readOnly')
    assert.equal(dev.request.status, 'success')
    assert.equal(dev.request.pushed, true)
    assert.equal(dev.request.scope, 'write')
    assert.equal(dev.request.headSha, head)
    assert.equal(dev.request.commits?.length, 1)
  } finally {
    w.cleanup()
  }
})

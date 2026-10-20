import assert from 'node:assert/strict'
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { test } from 'node:test'
import { extractTarZst, snapshotExcludes, tarZstBuffer } from '../src/archive.js'
import { glabConfigCommands, normalizeCliEnv, readCliEnvState, refreshCliEnv, refreshDelay, renderCliEnv, shellQuote } from '../src/cli-env.js'
import type { RunnerConfig } from '../src/config.js'
import { loadConfig } from '../src/config.js'
import { baseEnv, mcpCommand, runnerGuidance } from '../src/environment.js'
import { checkBranchAllowed, commitMessage, Git, parseNumstat, summaryTitle } from '../src/git/git.js'
import { claudeHarness } from '../src/harness/claude.js'
import { codexConfig, codexHarness } from '../src/harness/codex.js'
import { opencodeConfig, opencodeHarness } from '../src/harness/opencode.js'
import { harnessFor } from '../src/harness/index.js'
import type { HarnessContext } from '../src/harness/types.js'
import { McpServer } from '../src/mcp/server.js'
import { elicitationMarker } from '../src/mcp/tools/ask-user.js'
import { registerBuiltinTools } from '../src/mcp/tools/index.js'
import type { BootstrapResponse, CredentialRequest, SnapshotCompleteRequest } from '../src/protocol.js'
import { detectInstallers, miseConfigToml, resolveHomeFile, runSetup, SetupError, SetupLog } from '../src/setup.js'
import { EventFactory } from '../src/mapping/common.js'
import { runSnapshot } from '../src/snapshot.js'
import { sh, tempDir } from './helpers.js'

function bootstrap(): BootstrapResponse {
  return {
    sandboxId: 'sbx1',
    mode: 'session',
    restored: false,
    repo: { codeHost: 'github', hostName: 'github.com', fullName: 'acme/app', cloneUrl: '', baseRef: 'main', branchName: 'flow/x', existingBranch: false },
    harness: { name: 'codex', model: 'gpt-5-codex', reasoningEffort: 'high' },
    llm: { anthropicBaseUrl: 'http://flow:8090/internal/llm-proxy/anthropic', openaiBaseUrl: 'http://flow:8090/internal/llm-proxy/openai/v1', openSourceBaseUrl: 'http://flow:8090/internal/llm-proxy/opensource/v1' },
    environment: { mise: { environmentVariables: [{ name: 'APP_ENV', value: 'test' }, { name: 'bad-name', value: 'x' }] }, dependencyManagement: 'custom-only', hasSecrets: false },
    git: { authorName: 'Ada', authorEmail: 'ada@example.com', committerName: 'bot', committerEmail: 'bot@example.com' },
    browser: { enabled: true },
    limits: { turnTimeoutSeconds: 60, heartbeatSeconds: 15, setupTimeoutSeconds: 60, stopGraceSeconds: 1 },
    guidance: 'Workspace guidance.',
  }
}

function config(root: string): RunnerConfig {
  return {
    runtimeUrl: 'http://flow:8090',
    tokenFile: '/var/run/flow/token',
    sandboxId: 'sbx1',
    mode: 'session',
    paths: { workspace: join(root, 'workspace'), repo: join(root, 'workspace', 'repo'), home: join(root, 'home'), flowDir: join(root, 'home', '.flow'), binDir: '/opt/flow/bin' },
    branchPrefix: 'flow/',
    pidsLimit: 0,
  }
}

function harnessContext(root: string, boot = bootstrap()): HarnessContext {
  const cfg = config(root)
  return {
    bootstrap: boot,
    token: 'fcs_tok',
    paths: cfg.paths,
    turn: { id: 't1', prompt: '--help me', kind: 'initial', attachments: ['https://flow/files/a.png'] },
    guidance: 'G',
    mcp: mcpCommand(cfg, boot, 't1'),
    env: { PATH: '/usr/bin', OPENAI_API_KEY: 'sk-real', ANTHROPIC_API_KEY: 'sk-ant-real' },
  }
}

test('config: required runtime URL, modes and defaults', () => {
  assert.throws(() => loadConfig({}), /FLOW_RUNTIME_URL/)
  assert.throws(() => loadConfig({ FLOW_RUNTIME_URL: 'http://f' }, 'nope'), /unknown runner mode/)
  const cfg = loadConfig({ FLOW_RUNTIME_URL: 'http://f/', HOME: '/home/flow', FLOW_RUNNER_MODE: 'warm', FLOW_PIDS_LIMIT: '8192' })
  assert.equal(cfg.runtimeUrl, 'http://f')
  assert.equal(cfg.mode, 'warm')
  assert.equal(cfg.tokenFile, '/var/run/flow/token')
  assert.equal(cfg.paths.repo, '/workspace/repo')
  assert.equal(cfg.paths.flowDir, '/home/flow/.flow')
  assert.equal(cfg.pidsLimit, 8192)
})

test('environment: FLOW_SANDBOX/LINEAR_SANDBOX always, FLOW_SNAPSHOT only while snapshotting', () => {
  const cfg = config('/r')
  const env = baseEnv(cfg, bootstrap(), {}, { PATH: '/usr/bin:/opt/flow/bin', FLOW_SNAPSHOT: '1', FLOW_RUNNER_MODE: 'session', HTTPS_PROXY: 'http://proxy:3128' })
  assert.equal(env.FLOW_SANDBOX, '1')
  assert.equal(env.LINEAR_SANDBOX, '1')
  assert.equal(env.FLOW_SNAPSHOT, undefined)
  assert.equal(env.FLOW_RUNNER_MODE, undefined)
  assert.equal(env.HTTPS_PROXY, 'http://proxy:3128')
  assert.equal(env.APP_ENV, 'test')
  assert.equal(env['bad-name'], undefined)
  assert.equal(env.PATH, '/opt/flow/bin:/r/home/.local/share/mise/shims:/usr/bin')
  assert.equal(env.GIT_COMMITTER_NAME, 'bot')
  const snap = baseEnv(cfg, bootstrap(), { snapshot: true }, { PATH: '/usr/bin' })
  assert.equal(snap.FLOW_SNAPSHOT, '1')
  assert.equal(snap.LINEAR_SNAPSHOT, '1')
  const mcp = mcpCommand(cfg, bootstrap(), 'turn_7')
  assert.deepEqual([mcp.command, mcp.args], ['/opt/flow/bin/flow-runner', ['mcp']])
  assert.equal(mcp.env.FLOW_TURN_ID, 'turn_7')
  assert.equal(mcp.env.FLOW_RUNTIME_URL, 'http://flow:8090')
  assert.equal(mcp.env.FLOW_TOKEN_FILE, '/var/run/flow/token')
  assert.equal(mcp.env.FLOW_BROWSER, '1')
  assert.ok(!Object.values(mcp.env).some((value) => value.startsWith('fcs_')), 'the MCP config never holds the token')
  const guidance = runnerGuidance(bootstrap(), '/workspace/repo')
  assert.match(guidance, /Do not create pull or merge requests yourself/)
  assert.match(guidance, /ask_user/)
  assert.ok(guidance.trimEnd().endsWith('Workspace guidance.'))
})

test('claude invocation points at the LLM proxy with the sandbox token, never a real key', () => {
  const tmp = tempDir('claude')
  try {
    const boot = bootstrap()
    boot.harness = { name: 'claudeCode', model: 'claude-sonnet-4-5', reasoningEffort: 'medium', resumeId: 'ignored-here' }
    const inv = claudeHarness.invocation({ ...harnessContext(tmp.path, boot), resumeId: 'sess-1' })
    assert.equal(inv.command, 'claude')
    assert.deepEqual(inv.args.slice(0, 4), ['-p', '--output-format', 'stream-json', '--verbose'])
    assert.ok(inv.args.join(' ').includes('--permission-mode bypassPermissions'))
    assert.deepEqual(inv.args.slice(inv.args.indexOf('--resume'), inv.args.indexOf('--resume') + 2), ['--resume', 'sess-1'])
    assert.equal(inv.env.ANTHROPIC_BASE_URL, 'http://flow:8090/internal/llm-proxy/anthropic')
    assert.equal(inv.env.ANTHROPIC_AUTH_TOKEN, 'fcs_tok')
    assert.equal(inv.env.ANTHROPIC_API_KEY, undefined)
    assert.equal(inv.env.OPENAI_API_KEY, undefined)
    assert.equal(inv.env.MAX_THINKING_TOKENS, '16000')
    assert.equal(inv.env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC, '1')
    assert.equal(inv.stdin, '--help me\n\nAttachments:\n- https://flow/files/a.png', 'the prompt goes to stdin, so it cannot become a flag')
    const mcp = JSON.parse(inv.files[0].content)
    assert.equal(mcp.mcpServers.flow.command, '/opt/flow/bin/flow-runner')
  } finally {
    tmp.cleanup()
  }
})

test('codex invocation: config.toml provider "flow", resume syntax, AGENTS.override.md', () => {
  const tmp = tempDir('codex')
  try {
    const ctx = harnessContext(tmp.path)
    const first = codexHarness.invocation(ctx)
    assert.deepEqual(first.args.slice(0, 2), ['exec', '--json'])
    assert.ok(first.args.includes('--dangerously-bypass-approvals-and-sandbox'))
    assert.deepEqual(first.args.slice(-3), ['-C', ctx.paths.repo, '-'])
    assert.ok(first.args.includes('model_reasoning_effort="high"'))
    assert.equal(first.env.FLOW_LLM_TOKEN, 'fcs_tok')
    assert.equal(first.env.OPENAI_API_KEY, undefined)
    const resumed = codexHarness.invocation({ ...ctx, resumeId: 'thread-1' })
    assert.deepEqual(resumed.args.slice(0, 3), ['exec', 'resume', 'thread-1'])
    assert.ok(!resumed.args.includes('-C'))
    const toml = codexConfig(ctx)
    assert.match(toml, /^model_provider = "flow"$/m)
    assert.match(toml, /^base_url = "http:\/\/flow:8090\/internal\/llm-proxy\/openai\/v1"$/m)
    assert.match(toml, /^env_key = "FLOW_LLM_TOKEN"$/m)
    assert.match(toml, /^wire_api = "responses"$/m)
    assert.match(toml, /\[mcp_servers\.flow\]\ncommand = "\/opt\/flow\/bin\/flow-runner"/)
    assert.ok(!toml.includes('fcs_tok'), 'the token is read from the env, not written to disk')
    assert.ok(first.files.some((file) => file.path.endsWith('.codex/AGENTS.override.md') && file.content === 'G'))
  } finally {
    tmp.cleanup()
  }
})

test('opencode invocation: OpenAI-compatible provider at the proxy, --session on follow-ups', () => {
  const tmp = tempDir('opencode')
  try {
    const boot = bootstrap()
    boot.harness = { name: 'openSource', model: 'qwen3-coder', reasoningEffort: 'high' }
    const ctx = harnessContext(tmp.path, boot)
    const inv = opencodeHarness.invocation({ ...ctx, resumeId: 'ses_1' })
    assert.deepEqual(inv.args.slice(0, 4), ['run', '--format', 'json', '--auto'])
    assert.ok(inv.args.join(' ').includes('-m flow/qwen3-coder'))
    assert.ok(inv.args.join(' ').includes('--variant high'))
    assert.ok(inv.args.join(' ').includes('--session ses_1'))
    const cfg = JSON.parse(opencodeConfig(ctx))
    assert.equal(cfg.provider.flow.npm, '@ai-sdk/openai-compatible')
    assert.equal(cfg.provider.flow.options.baseURL, 'http://flow:8090/internal/llm-proxy/opensource/v1')
    assert.equal(cfg.provider.flow.options.apiKey, '{env:FLOW_LLM_TOKEN}')
    assert.equal(cfg.mcp.flow.type, 'local')
    assert.equal(inv.env.OPENCODE_CONFIG, join(ctx.paths.flowDir, 'opencode.json'))
    assert.equal(harnessFor('claude').name, 'claudeCode')
    assert.equal(harnessFor('auto').name, 'codex')
    assert.throws(() => harnessFor('gemini'))
  } finally {
    tmp.cleanup()
  }
})

test('git helpers: commit subject, numstat parsing, branch policy', () => {
  assert.equal(commitMessage('## Use local dates in formatDate\n\nDetails', 'Co-authored-by: Flow <b@x>'), 'Use local dates in formatDate\n\nCo-authored-by: Flow <b@x>\n')
  assert.equal(commitMessage('', undefined), 'Update from Flow coding session\n')
  assert.equal(commitMessage('x'.repeat(100)).split('\n')[0].length, 72)
  // A title-sized first line becomes the session (and PR) title; a long prose line is never cut into one.
  assert.equal(summaryTitle('## Use local dates in formatDate\n\nDetails'), 'Use local dates in formatDate')
  assert.equal(summaryTitle('x'.repeat(100)), undefined)
  assert.equal(summaryTitle(''), undefined)
  assert.deepEqual(parseNumstat('3\t1\tsrc/a.ts\n-\t-\tlogo.png\n0\t9\told.ts\n', 'M\tsrc/a.ts\nA\tlogo.png\nD\told.ts\n'), [
    { path: 'src/a.ts', additions: 3, deletions: 1, status: 'modified' },
    { path: 'logo.png', additions: 0, deletions: 0, status: 'added' },
    { path: 'old.ts', additions: 0, deletions: 9, status: 'deleted' },
  ])
  const repo = bootstrap().repo
  assert.doesNotThrow(() => checkBranchAllowed(repo, 'flow/'))
  assert.throws(() => checkBranchAllowed({ ...repo, branchName: 'main' }, 'flow/'), /refusing to push main/)
  assert.doesNotThrow(() => checkBranchAllowed({ ...repo, branchName: 'feature/pr-head', existingBranch: true }, 'flow/'))
  assert.throws(() => checkBranchAllowed({ ...repo, branchName: 'flow/x', branchPrefix: 'agents/' }, 'flow/'))
})

test('Git against a real repository: clone, branch, signed-off identity, commit, push, diff', async () => {
  const tmp = tempDir('git')
  try {
    const origin = join(tmp.path, 'origin.git')
    const seed = join(tmp.path, 'seed')
    const env = { PATH: '/usr/bin:/bin', HOME: tmp.path, GIT_CONFIG_NOSYSTEM: '1' }
    sh('git', ['init', '-q', '--bare', '-b', 'main', origin], { env })
    sh('git', ['init', '-q', '-b', 'main', seed], { env })
    writeFileSync(join(seed, 'README.md'), '# app\n')
    sh('git', ['-c', 'user.name=s', '-c', 'user.email=s@x', 'add', '.'], { cwd: seed, env })
    sh('git', ['-c', 'user.name=s', '-c', 'user.email=s@x', 'commit', '-q', '-m', 'init'], { cwd: seed, env })
    sh('git', ['push', '-q', origin, 'main'], { cwd: seed, env })
    const repoDir = join(tmp.path, 'workspace', 'repo')
    mkdirSync(join(tmp.path, 'workspace'))
    const git = new Git({ repoDir, env, credentialHelper: '/opt/flow/bin/flow-git-credential', signProgram: '/opt/flow/bin/flow-sign' })
    await git.clone(origin)
    await git.configureCredentials()
    const repo = { ...bootstrap().repo, cloneUrl: origin }
    await git.checkoutSessionBranch(repo)
    await git.configureIdentity({ ...bootstrap().git, signingFormat: 'ssh', signingPublicKey: 'ssh-ed25519 AAAAC3Nza dev' })
    assert.equal(sh('git', ['config', '--get', 'credential.helper'], { cwd: repoDir, env }).trim(), '/opt/flow/bin/flow-git-credential')
    assert.equal(sh('git', ['config', '--get', 'gpg.ssh.program'], { cwd: repoDir, env }).trim(), '/opt/flow/bin/flow-sign')
    assert.equal(sh('git', ['config', '--get', 'commit.gpgsign'], { cwd: repoDir, env }).trim(), 'true')
    await git.configureIdentity(bootstrap().git)
    const start = await git.head()
    assert.equal(await git.commitAll('nothing\n', bootstrap().git), '')
    writeFileSync(join(repoDir, 'hello.txt'), 'hello\n')
    const sha = await git.commitAll('Add hello\n\nCo-authored-by: Flow <b@x>\n', bootstrap().git)
    assert.match(sha, /^[0-9a-f]{40}$/)
    const log = sh('git', ['log', '-1', '--format=%an|%cn|%s'], { cwd: repoDir, env }).trim()
    assert.equal(log, 'Ada|bot|Add hello')
    const commits = await git.commitsSince(start, 'flow/x', 't1', false)
    assert.deepEqual(commits.map((c) => [c.sha, c.message, c.turnActivityId]), [[sha, 'Add hello', 't1']])
    await git.push('flow/x')
    assert.equal(sh('git', ['rev-parse', 'refs/heads/flow/x'], { cwd: origin, env }).trim(), sha)
    assert.deepEqual(await git.diffSummary(git.baseRevision(repo)), [{ path: 'hello.txt', additions: 1, deletions: 0, status: 'added' }])
    // A follow-up in a new sandbox checks out the pushed branch.
    const again = new Git({ repoDir: join(tmp.path, 'workspace', 'repo2'), env, credentialHelper: 'h', signProgram: 's' })
    await again.clone(origin)
    await again.checkoutSessionBranch({ ...repo, existingBranch: true })
    assert.equal(await again.head(), sha)
  } finally {
    tmp.cleanup()
  }
})

test('Git: a member session pushes through the relay push URL and fetches from origin', async () => {
  const tmp = tempDir('git-relay')
  try {
    const origin = join(tmp.path, 'origin.git')
    const relay = join(tmp.path, 'relay.git')
    const seed = join(tmp.path, 'seed')
    const env = { PATH: '/usr/bin:/bin', HOME: tmp.path, GIT_CONFIG_NOSYSTEM: '1' }
    sh('git', ['init', '-q', '--bare', '-b', 'main', origin], { env })
    sh('git', ['init', '-q', '--bare', '-b', 'main', relay], { env })
    sh('git', ['init', '-q', '-b', 'main', seed], { env })
    writeFileSync(join(seed, 'README.md'), '# app\n')
    sh('git', ['-c', 'user.name=s', '-c', 'user.email=s@x', 'add', '.'], { cwd: seed, env })
    sh('git', ['-c', 'user.name=s', '-c', 'user.email=s@x', 'commit', '-q', '-m', 'init'], { cwd: seed, env })
    sh('git', ['push', '-q', origin, 'main'], { cwd: seed, env })
    const repoDir = join(tmp.path, 'workspace', 'repo')
    mkdirSync(join(tmp.path, 'workspace'))
    const git = new Git({ repoDir, env, credentialHelper: 'h', signProgram: 's' })
    await git.clone(origin)
    await git.configureCredentials(relay)
    assert.equal(sh('git', ['remote', 'get-url', 'origin'], { cwd: repoDir, env }).trim(), origin)
    assert.equal(sh('git', ['remote', 'get-url', '--push', 'origin'], { cwd: repoDir, env }).trim(), relay)
    await git.configureCredentials(relay) // idempotent
    const repo = { ...bootstrap().repo, cloneUrl: origin, pushUrl: relay }
    await git.checkoutSessionBranch(repo)
    await git.configureIdentity(bootstrap().git)
    writeFileSync(join(repoDir, 'hello.txt'), 'hello\n')
    const sha = await git.commitAll('Add hello\n', bootstrap().git)
    await git.push('flow/x')
    assert.equal(sh('git', ['rev-parse', 'refs/heads/flow/x'], { cwd: relay, env }).trim(), sha)
    assert.throws(() => sh('git', ['rev-parse', '--verify', '--quiet', 'refs/heads/flow/x'], { cwd: origin, env }))
    await git.fetch()
    // Without a push URL (e.g. a restored workspace of a bot session) origin is used again.
    await git.configureCredentials()
    assert.equal(sh('git', ['remote', 'get-url', '--push', 'origin'], { cwd: repoDir, env }).trim(), origin)
  } finally {
    tmp.cleanup()
  }
})

test('setup: mise files stay in $HOME, tools toml, installers only for ignored outputs', async () => {
  const cfg = config('/r')
  assert.equal(resolveHomeFile('~/.npmrc', cfg.paths), '/r/home/.npmrc')
  assert.equal(resolveHomeFile('.config/pip/pip.conf', cfg.paths), '/r/home/.config/pip/pip.conf')
  assert.equal(resolveHomeFile('/r/workspace/repo/.env.local', cfg.paths), '/r/workspace/repo/.env.local')
  assert.throws(() => resolveHomeFile('../../etc/passwd', cfg.paths), SetupError)
  assert.throws(() => resolveHomeFile('/etc/passwd', cfg.paths), SetupError)
  assert.equal(miseConfigToml({ node: '22', python: '3.12' }), '# Written by flow-runner from the coding environment.\n[tools]\n"node" = "22"\n"python" = "3.12"\n')
  assert.equal(miseConfigToml({}), '')
  const tmp = tempDir('installers')
  try {
    writeFileSync(join(tmp.path, 'package-lock.json'), '{}')
    writeFileSync(join(tmp.path, 'go.mod'), 'module x')
    writeFileSync(join(tmp.path, 'requirements.txt'), 'x')
    const chosen = await detectInstallers(tmp.path, async (path) => path === 'node_modules')
    assert.deepEqual(chosen.map((installer) => installer.name), ['npm', 'go'], '.venv is not ignored, so pip is skipped')
    mkdirSync(join(tmp.path, 'node_modules'))
    assert.deepEqual((await detectInstallers(tmp.path, async () => true)).map((installer) => installer.name), ['pip', 'go'])
  } finally {
    tmp.cleanup()
  }
})

test('setup: the prepare script gets secrets once, logs are redacted, failure is environmentSetupFailed', async () => {
  const tmp = tempDir('setup')
  try {
    const cfg = config(tmp.path)
    mkdirSync(cfg.paths.repo, { recursive: true })
    const boot = bootstrap()
    boot.environment = {
      mise: { files: [{ path: '~/.config/app/settings', content: 'x=1\n' }] },
      prepareScript: 'echo "token is $DB_PASSWORD"\necho "env is $APP_ENV"\ntest "$FLOW_SNAPSHOT" = 1\n',
      dependencyManagement: 'custom-only',
      hasSecrets: true,
    }
    const lines: string[] = []
    let secretFetches = 0
    const deps = {
      bootstrap: boot,
      paths: cfg.paths,
      git: {} as Git,
      env: { ...baseEnv(cfg, boot, { snapshot: true }, { PATH: '/usr/bin:/bin' }), APP_ENV: 'ci' },
      log: new SetupLog((line) => lines.push(line)),
      emit: () => {},
      events: new EventFactory({}),
      setupSecrets: async () => {
        secretFetches++
        return { variables: [{ name: 'DB_PASSWORD', value: 'hunter2-secret' }] }
      },
      deadline: Date.now() + 60_000,
    }
    await runSetup(deps)
    assert.equal(secretFetches, 1)
    assert.equal(readFileSync(join(cfg.paths.home, '.config/app/settings'), 'utf8'), 'x=1\n')
    assert.ok(lines.includes('token is ***'), lines.join('\n'))
    assert.ok(lines.includes('env is ci'))
    assert.ok(!lines.join('\n').includes('hunter2-secret'))
    boot.environment.prepareScript = 'echo boom\nexit 3\n'
    boot.environment.hasSecrets = false
    await assert.rejects(runSetup(deps), (error: unknown) => error instanceof SetupError && /exited with 3/.test(error.message) && error.logTail.includes('boom'))
  } finally {
    tmp.cleanup()
  }
})

test('cli env: gh/glab exports, GHE token alias, glab defaults and refresh timing', async () => {
  assert.equal(shellQuote("it's"), `'it'\\''s'`)
  assert.equal(renderCliEnv({ GH_TOKEN: 'ghs_1', 'BAD-NAME': 'x', GH_HOST: 'github.com' }), "# Written by flow-runner; refreshed before expiry. Do not edit.\nexport GH_HOST='github.com'\nexport GH_TOKEN='ghs_1'\n")
  assert.deepEqual(normalizeCliEnv({ GH_TOKEN: 't', GH_HOST: 'ghe.acme.com' }, '/home/flow'), { GH_TOKEN: 't', GH_HOST: 'ghe.acme.com', GH_ENTERPRISE_TOKEN: 't' })
  assert.deepEqual(normalizeCliEnv({ GITLAB_TOKEN: 'glpat', GITLAB_HOST: 'gitlab.acme.com' }, '/home/flow'), {
    GITLAB_TOKEN: 'glpat',
    GITLAB_HOST: 'gitlab.acme.com',
    GLAB_CONFIG_DIR: '/home/flow/.config/glab-cli',
    GITLAB_API_PROTOCOL: 'https',
    NO_PROMPT: '1',
  })
  assert.deepEqual(glabConfigCommands('gitlab.acme.com')[0], ['config', 'set', '-g', 'host', 'gitlab.acme.com'])
  const now = Date.parse('2026-10-18T00:00:00Z')
  assert.equal(refreshDelay('2026-10-18T01:00:00Z', now), 50 * 60_000)
  assert.equal(refreshDelay('2026-10-18T00:20:00Z', now), 15 * 60_000)
  assert.equal(refreshDelay('2026-10-18T00:01:00Z', now), 30_000)

  const tmp = tempDir('clienv')
  try {
    const calls: Array<{ args: string[]; env?: NodeJS.ProcessEnv }> = []
    const requests: CredentialRequest[] = []
    const state = await refreshCliEnv({
      credentials: async (request) => {
        requests.push(request)
        return { username: 'oauth2', password: 'glpat-x', expiresAt: '2026-10-19T00:00:00Z', cliEnv: { GITLAB_TOKEN: 'glpat-x', GITLAB_HOST: 'gitlab.acme.com' } }
      },
      flowDir: join(tmp.path, '.flow'),
      home: tmp.path,
      host: 'gitlab.acme.com',
      codeHost: 'gitlab',
      run: async (_command, args, options) => {
        calls.push({ args, env: options?.env })
        return { code: 0, stdout: '', stderr: '', timedOut: false }
      },
    })
    assert.deepEqual(requests, [{ protocol: 'https', host: 'gitlab.acme.com', purpose: 'cli' }])
    assert.equal(state.env.GITLAB_TOKEN, 'glpat-x')
    const file = join(tmp.path, '.flow', 'cli-env')
    assert.match(readFileSync(file, 'utf8'), /export GITLAB_TOKEN='glpat-x'/)
    assert.equal(statSync(file).mode & 0o777, 0o600)
    const saved = await readCliEnvState(join(tmp.path, '.flow'))
    assert.deepEqual(saved?.env, {}, 'the state file never stores tokens')
    assert.equal(saved?.codeHost, 'gitlab')
    assert.equal(calls.length, 3)
    assert.equal(calls[0].env?.GITLAB_TOKEN, '', 'glab config never receives the token')
  } finally {
    tmp.cleanup()
  }
})

test('MCP server: initialize, tools/list, ask_user call leaves a marker, errors', async () => {
  const tmp = tempDir('mcp')
  try {
    const server = new McpServer({ sandboxId: 's', turnId: 'turn_3', repoDir: '/workspace/repo', flowDir: join(tmp.path, '.flow'), browserEnabled: false })
    registerBuiltinTools(server, {})
    const init = await server.handle({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-03-26' } })
    assert.equal((init?.result as { protocolVersion: string }).protocolVersion, '2025-03-26')
    const unknownVersion = await server.handle({ jsonrpc: '2.0', id: 2, method: 'initialize', params: { protocolVersion: '1999-01-01' } })
    assert.equal((unknownVersion?.result as { protocolVersion: string }).protocolVersion, '2025-06-18')
    assert.equal(await server.handle({ jsonrpc: '2.0', method: 'notifications/initialized' }), undefined)
    const list = await server.handle({ jsonrpc: '2.0', id: 3, method: 'tools/list' })
    assert.deepEqual((list?.result as { tools: Array<{ name: string }> }).tools.map((tool) => tool.name), ['ask_user'])
    const call = await server.handle({ jsonrpc: '2.0', id: 4, method: 'tools/call', params: { name: 'ask_user', arguments: { question: 'Which DB?', options: ['pg', 'mysql'] } } })
    assert.match(JSON.stringify(call?.result), /end your turn/)
    assert.deepEqual(JSON.parse(readFileSync(elicitationMarker(join(tmp.path, '.flow'), 'turn_3'), 'utf8')).options, ['pg', 'mysql'])
    const empty = await server.handle({ jsonrpc: '2.0', id: 5, method: 'tools/call', params: { name: 'ask_user', arguments: {} } })
    assert.equal((empty?.result as { isError: boolean }).isError, true)
    assert.equal((await server.handle({ jsonrpc: '2.0', id: 6, method: 'tools/call', params: { name: 'nope' } }))?.error?.code, -32602)
    assert.equal((await server.handle({ jsonrpc: '2.0', id: 7, method: 'resources/list' }))?.error?.code, -32601)
    assert.throws(() => server.registerTool('ask_user', { description: '', inputSchema: {} }, () => ({ content: [] })), /already registered/)
    assert.throws(() => server.registerTool('bad name', { description: '', inputSchema: {} }, () => ({ content: [] })))
  } finally {
    tmp.cleanup()
  }
})

test('MCP server: browser and propose_environment tools register when enabled', () => {
  const tmp = tempDir('mcp-tools')
  try {
    const server = new McpServer({ sandboxId: 's', turnId: 't', repoDir: '/workspace/repo', flowDir: join(tmp.path, '.flow'), browserEnabled: true })
    registerBuiltinTools(server, { FLOW_RUNTIME_URL: 'http://flow:8090', FLOW_TOKEN_FILE: join(tmp.path, 'token') })
    const names = server.toolNames()
    for (const name of ['ask_user', 'browser_open', 'browser_screenshot', 'attach_screenshot', 'propose_environment']) {
      assert.ok(names.includes(name), `${name} is registered (got ${names.join(', ')})`)
    }
    const disabled = new McpServer({ sandboxId: 's', turnId: 't', repoDir: '/workspace/repo', flowDir: join(tmp.path, '.flow'), browserEnabled: false })
    registerBuiltinTools(disabled, { FLOW_RUNTIME_URL: 'http://flow:8090', FLOW_TOKEN_FILE: join(tmp.path, 'token'), FLOW_BROWSER_ENABLED: '0' })
    assert.ok(!disabled.toolNames().some((name) => name.startsWith('browser_')), 'browser tools stay off when the bootstrap disables them')
  } finally {
    tmp.cleanup()
  }
})

test('archive: tar.zst round trip and snapshot excludes', async () => {
  const tmp = tempDir('archive')
  try {
    const src = join(tmp.path, 'src')
    mkdirSync(join(src, 'home', '.claude', 'projects'), { recursive: true })
    writeFileSync(join(src, 'home', '.claude', 'projects', 's.jsonl'), '{"a":1}\n')
    const archive = await tarZstBuffer(join(src, 'home'), ['.claude', '.missing'], 1024 * 1024)
    assert.ok(archive && archive.length > 0)
    const dest = join(tmp.path, 'dest')
    mkdirSync(dest)
    await extractTarZst(archive!, dest)
    assert.equal(readFileSync(join(dest, '.claude', 'projects', 's.jsonl'), 'utf8'), '{"a":1}\n')
    assert.equal(await tarZstBuffer(src, ['.nothing'], 10), null)
    await assert.rejects(tarZstBuffer(join(src, 'home'), ['.claude'], 10), /exceeds/)
    const cfg = config(tmp.path)
    mkdirSync(join(cfg.paths.repo, '.flow'), { recursive: true })
    writeFileSync(join(cfg.paths.repo, '.flow', 'snapshot-exclude'), '# caches\nnode_modules/.cache\n~/.cache/pip\n../escape\n')
    const excludes = snapshotExcludes(cfg.paths)
    assert.ok(excludes.some((pattern) => pattern.endsWith('home/.cache/ms-playwright')))
    assert.ok(excludes.some((pattern) => pattern.endsWith('home/.flow/cli-env')))
    assert.ok(excludes.some((pattern) => pattern.endsWith('workspace/repo/node_modules/.cache')))
    assert.ok(excludes.some((pattern) => pattern.endsWith('home/.cache/pip')))
    assert.ok(!excludes.some((pattern) => pattern.includes('escape')))
    assert.ok(!existsSync(join(tmp.path, 'never')))
  } finally {
    tmp.cleanup()
  }
})

test('snapshot mode: clone at base, setup with FLOW_SNAPSHOT=1, upload, report ready (or failed)', async () => {
  const tmp = tempDir('snapshot')
  try {
    const env = { PATH: '/usr/bin:/bin', HOME: tmp.path, GIT_CONFIG_NOSYSTEM: '1' }
    const origin = join(tmp.path, 'origin.git')
    const seed = join(tmp.path, 'seed')
    sh('git', ['init', '-q', '--bare', '-b', 'main', origin], { env })
    sh('git', ['init', '-q', '-b', 'main', seed], { env })
    writeFileSync(join(seed, 'README.md'), '# app\n')
    sh('git', ['add', '.'], { cwd: seed, env })
    sh('git', ['-c', 'user.name=s', '-c', 'user.email=s@x', 'commit', '-q', '-m', 'init'], { cwd: seed, env })
    sh('git', ['push', '-q', origin, 'main'], { cwd: seed, env })
    const base = sh('git', ['rev-parse', 'HEAD'], { cwd: seed, env }).trim()
    const cfg = config(tmp.path)
    mkdirSync(cfg.paths.home, { recursive: true })
    const boot: BootstrapResponse = {
      ...bootstrap(),
      mode: 'snapshot',
      snapshot: { id: 'snap1', strategy: 'archive' },
      repo: { ...bootstrap().repo, cloneUrl: origin, baseSha: base, branchName: '' },
      environment: { mise: {}, prepareScript: 'test "$FLOW_SNAPSHOT" = 1 && test "$LINEAR_SNAPSHOT" = 1 && echo prepared > prepared.txt', dependencyManagement: 'custom-only', hasSecrets: false },
    }
    const logs: string[] = []
    const completes: SnapshotCompleteRequest[] = []
    const api = {
      heartbeat: async () => ({ stop: false }),
      setupSecrets: async () => ({ variables: [] }),
      putSnapshotArchive: async () => {},
      postSnapshotLogs: async (_id: string, lines: string[]) => void logs.push(...lines),
      completeSnapshot: async (_id: string, request: SnapshotCompleteRequest) => void completes.push(request),
    }
    const git = new Git({ repoDir: cfg.paths.repo, env, credentialHelper: 'h', signProgram: 's' })
    const code = await runSnapshot({ config: cfg, api, bootstrap: boot, git, upload: async () => 1234, heartbeatMs: 5 })
    assert.equal(code, 0)
    assert.deepEqual(completes, [{ status: 'ready', baseSha: base, sizeBytes: 1234 }])
    assert.equal(readFileSync(join(cfg.paths.repo, 'prepared.txt'), 'utf8'), 'prepared\n')
    assert.ok(logs.some((line) => line.includes('uploaded snapshot archive')))

    const failing = { ...boot, environment: { ...boot.environment, prepareScript: 'exit 9' } }
    const cfg2 = config(join(tmp.path, 'second'))
    mkdirSync(cfg2.paths.home, { recursive: true })
    const code2 = await runSnapshot({ config: cfg2, api, bootstrap: failing, git: new Git({ repoDir: cfg2.paths.repo, env, credentialHelper: 'h', signProgram: 's' }), upload: async () => 1, heartbeatMs: 5 })
    assert.equal(code2, 1)
    assert.equal(completes[1].status, 'failed')
    assert.equal(completes[1].errorCode, 'environmentSetupFailed')
  } finally {
    tmp.cleanup()
  }
})

import { existsSync } from 'node:fs'
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname, isAbsolute, join, normalize, relative, resolve } from 'node:path'
import type { RunnerPaths } from './config.js'
import type { Git, Runner } from './git/git.js'
import { errorMessage } from './log.js'
import { CommandError, run } from './process.js'
import { networkFailureHint } from './proxy-env.js'
import type { BootstrapResponse, CodingEnvVar, CodingMiseConfig, RunnerEvent } from './protocol.js'
import type { EventFactory } from './mapping/common.js'

// Workspace preparation and the setup phase (§16 step 2):
// clone or fetch, check out the session branch, write the mise files into
// $HOME, install the mise tools, fetch the setup-only secrets, run the
// prepare script with them, optionally run detected dependency installers,
// and drop the secrets again. Shared by session and snapshot mode.

export class SetupError extends Error {
  constructor(
    message: string,
    readonly logTail: string,
  ) {
    super(message)
    this.name = 'SetupError'
  }
}

/** Collects setup output: forwards redacted lines and keeps a tail for error reports. */
export class SetupLog {
  private readonly lines: string[] = []
  private secrets: string[] = []

  constructor(
    private readonly sink: (line: string) => void,
    private readonly keep = 200,
  ) {}

  /** Values masked in every line from now on (setup secrets). */
  redact(values: string[]): void {
    this.secrets = [...new Set([...this.secrets, ...values.filter((value) => value.length >= 4)])].sort((a, b) => b.length - a.length)
  }

  line(text: string): void {
    let clean = text
    for (const secret of this.secrets) clean = clean.split(secret).join('***')
    this.lines.push(clean)
    if (this.lines.length > this.keep) this.lines.shift()
    this.sink(clean)
  }

  tail(count = 60): string {
    return this.lines.slice(-count).join('\n')
  }
}

export interface SetupDeps {
  bootstrap: BootstrapResponse
  paths: RunnerPaths
  git: Git
  /** Base environment for setup commands (PATH with mise shims, proxies, plain env, FLOW_SANDBOX…). */
  env: NodeJS.ProcessEnv
  log: SetupLog
  emit: (event: RunnerEvent) => void
  events: EventFactory
  setupSecrets: () => Promise<{ variables: CodingEnvVar[] }>
  run?: Runner
  /** Deadline for the whole setup (ms since epoch). */
  deadline: number
  now?: () => number
}

const ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/

/** Plain environment variables of the environment, as an env object. */
export function envVars(vars: CodingEnvVar[] | undefined): Record<string, string> {
  const out: Record<string, string> = {}
  for (const variable of vars ?? []) {
    if (ENV_NAME.test(variable.name)) out[variable.name] = variable.value
  }
  return out
}

/** Resolves a mise file path into $HOME (or the workspace); refuses anything else. */
export function resolveHomeFile(path: string, paths: RunnerPaths): string {
  const raw = path.startsWith('~/') ? path.slice(2) : path
  const target = isAbsolute(raw) ? normalize(raw) : resolve(paths.home, raw)
  for (const root of [paths.home, paths.workspace]) {
    const rel = relative(root, target)
    if (rel && !rel.startsWith('..') && !isAbsolute(rel)) return target
  }
  throw new SetupError(`mise file path ${JSON.stringify(path)} must be inside ${paths.home}`, '')
}

export function miseConfigToml(tools: Record<string, string> | undefined): string {
  const entries = Object.entries(tools ?? {}).filter(([name, version]) => name.trim() && String(version).trim())
  if (!entries.length) return ''
  const lines = ['# Written by flow-runner from the coding environment.', '[tools]']
  for (const [name, version] of entries) lines.push(`${JSON.stringify(name)} = ${JSON.stringify(String(version))}`)
  return lines.join('\n') + '\n'
}

function remaining(deps: SetupDeps): number {
  return Math.max(1000, deps.deadline - (deps.now ?? Date.now)())
}

/**
 * Clones (or fetches a restored workspace) and checks out the session branch.
 * Snapshot builds (`detach`) check out the base revision without a branch.
 */
export async function prepareRepository(deps: SetupDeps, options: { detach?: boolean } = {}): Promise<void> {
  const { bootstrap, git, log } = deps
  if (bootstrap.restored && (await git.isRepo())) {
    log.line(`$ git fetch origin (restored workspace)`)
    await git.configureCredentials(bootstrap.repo.pushUrl)
    await git.fetch()
  } else {
    await mkdir(dirname(deps.paths.repo), { recursive: true })
    log.line(`$ git clone ${bootstrap.repo.fullName || bootstrap.repo.cloneUrl}`)
    await git.clone(bootstrap.repo.cloneUrl, (line) => log.line(line))
    await git.configureCredentials(bootstrap.repo.pushUrl)
    deps.emit(deps.events.action({ action: 'Cloned repo', parameter: bootstrap.repo.fullName || bootstrap.repo.cloneUrl }))
  }
  if (options.detach) await git.git(['checkout', '--detach', git.baseRevision(bootstrap.repo)])
  else await git.checkoutSessionBranch(bootstrap.repo)
}

/** Writes mise files and tools, then runs `mise install`. */
export async function installTools(deps: SetupDeps): Promise<void> {
  const mise: CodingMiseConfig = deps.bootstrap.environment.mise ?? {}
  const runner = deps.run ?? run
  for (const file of mise.files ?? []) {
    const target = resolveHomeFile(file.path, deps.paths)
    await mkdir(dirname(target), { recursive: true })
    await writeFile(target, file.content)
    deps.log.line(`wrote ${file.path}`)
  }
  const toml = miseConfigToml(mise.tools)
  const configPath = join(deps.paths.home, '.config', 'mise', 'config.toml')
  if (toml) {
    await mkdir(dirname(configPath), { recursive: true })
    await writeFile(configPath, toml)
  }
  const repoHasMise = ['mise.toml', '.mise.toml', '.tool-versions', 'mise/config.toml', '.config/mise.toml'].some((name) =>
    existsSync(join(deps.paths.repo, name)),
  )
  if (!toml && !repoHasMise) return
  const onLine = (line: string) => deps.log.line(line)
  if (repoHasMise) {
    await runner('mise', ['trust', '--yes', deps.paths.repo], { cwd: deps.paths.repo, env: deps.env, onLine, check: false, timeoutMs: 60_000 })
  }
  deps.log.line('$ mise install')
  await runner('mise', ['install', '--yes'], { cwd: deps.paths.repo, env: { ...deps.env, MISE_YES: '1' }, onLine, timeoutMs: remaining(deps) })
  deps.emit(deps.events.action({ action: 'Installed tools', parameter: Object.keys(mise.tools ?? {}).join(', ') || 'mise.toml' }))
}

/** Detected installers for dependencyManagement "custom-and-generated". */
export interface Installer {
  name: string
  command: string
  args: string[]
  /** Lockfile or manifest that triggers it. */
  marker: string
  /** Output directory that must be git-ignored (and missing) for the installer to run. */
  output?: string
}

export const INSTALLERS: Installer[] = [
  { name: 'pnpm', command: 'pnpm', args: ['install', '--frozen-lockfile'], marker: 'pnpm-lock.yaml', output: 'node_modules' },
  { name: 'yarn', command: 'yarn', args: ['install'], marker: 'yarn.lock', output: 'node_modules' },
  { name: 'npm', command: 'npm', args: ['ci', '--no-audit', '--no-fund'], marker: 'package-lock.json', output: 'node_modules' },
  { name: 'pip', command: 'sh', args: ['-c', 'python3 -m venv .venv && .venv/bin/pip install -r requirements.txt'], marker: 'requirements.txt', output: '.venv' },
  { name: 'go', command: 'go', args: ['mod', 'download'], marker: 'go.mod' },
  { name: 'bundler', command: 'bundle', args: ['install'], marker: 'Gemfile.lock' },
  { name: 'cargo', command: 'cargo', args: ['fetch'], marker: 'Cargo.lock' },
]

/** Installers to run: marker present, output missing and git-ignored (never dirty the tree). */
export async function detectInstallers(repoDir: string, isIgnored: (path: string) => Promise<boolean>): Promise<Installer[]> {
  const chosen: Installer[] = []
  const outputs = new Set<string>()
  for (const installer of INSTALLERS) {
    if (!existsSync(join(repoDir, installer.marker))) continue
    if (installer.output) {
      if (outputs.has(installer.output) || existsSync(join(repoDir, installer.output))) continue
      if (!(await isIgnored(installer.output))) continue
      outputs.add(installer.output)
    }
    chosen.push(installer)
  }
  return chosen
}

async function runGeneratedInstallers(deps: SetupDeps): Promise<void> {
  const runner = deps.run ?? run
  const isIgnored = async (path: string) => {
    const result = await deps.git.git(['check-ignore', '-q', path], { check: false })
    return result.code === 0
  }
  for (const installer of await detectInstallers(deps.paths.repo, isIgnored)) {
    deps.log.line(`$ ${installer.command} ${installer.args.join(' ')} (detected ${installer.marker})`)
    const result = await runner(installer.command, installer.args, {
      cwd: deps.paths.repo,
      env: { ...deps.env, CI: 'true' },
      onLine: (line) => deps.log.line(line),
      check: false,
      timeoutMs: remaining(deps),
    })
    if (result.code !== 0) {
      const hint = networkFailureHint(deps.log.tail(40))
      deps.log.line(`warning: ${installer.name} install failed with exit code ${result.code}${hint ? ` (${hint})` : ''}; continuing`)
    }
  }
}

/**
 * Runs the full setup: tools, secrets, prepare script, generated installers.
 * The secrets only ever live in the prepare script's environment.
 */
export async function runSetup(deps: SetupDeps): Promise<void> {
  const { bootstrap, log } = deps
  const runner = deps.run ?? run
  try {
    await installTools(deps)
    let secrets: Record<string, string> = {}
    if (bootstrap.environment.hasSecrets) {
      const response = await deps.setupSecrets()
      secrets = envVars(response.variables)
      log.redact(Object.values(secrets))
      log.line(`fetched ${Object.keys(secrets).length} setup secret(s)`)
    }
    const script = bootstrap.environment.prepareScript?.trim()
    if (script) {
      const scriptPath = join(deps.paths.flowDir, 'prepare.sh')
      await mkdir(deps.paths.flowDir, { recursive: true, mode: 0o700 })
      await writeFile(scriptPath, script + '\n', { mode: 0o700 })
      log.line('$ prepare script')
      await runner('bash', ['-eo', 'pipefail', scriptPath], {
        cwd: deps.paths.repo,
        env: { ...deps.env, ...secrets },
        onLine: (line) => log.line(line),
        timeoutMs: remaining(deps),
      })
      deps.emit(deps.events.action({ action: 'Ran prepare script' }))
    }
    secrets = {}
    if ((bootstrap.environment.dependencyManagement || 'custom-and-generated') === 'custom-and-generated') {
      await runGeneratedInstallers(deps)
    }
  } catch (error) {
    if (error instanceof SetupError) throw new SetupError(error.message, log.tail())
    const reason =
      error instanceof CommandError
        ? `${error.command.split(' ').slice(0, 2).join(' ')} exited with ${error.result.code}${error.result.timedOut ? ' (setup timed out)' : ''}`
        : errorMessage(error)
    // Name the cause (proxy 407, egress 403/503, missing tool, upstream
    // failure) instead of a generic network error.
    const hint = networkFailureHint(log.tail())
    throw new SetupError(`Environment setup failed: ${reason}${hint ? ` (${hint})` : ''}`, log.tail())
  }
}

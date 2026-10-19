import { chmod, mkdir, readFile, rename, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { errorMessage, silentLogger, type Logger } from './log.js'
import type { CredentialRequest, CredentialResponse } from './protocol.js'
import type { Runner } from './git/git.js'
import { run } from './process.js'

// gh / glab authentication (§14). Neither CLI is configured at image build
// time. The runner asks Flow for a short-lived CLI credential
// (POST /credentials {purpose:"cli"}) and writes the returned env
// (GH_TOKEN/GH_HOST or GITLAB_TOKEN/GITLAB_HOST/GLAB_CONFIG_DIR) to
// ~/.flow/cli-env. The gh and glab wrappers in /opt/flow/bin source that file
// on every call (after `flow-runner cli-env ensure` refreshed it when it is
// close to expiry), so a refresh needs no harness restart.

export const CLI_ENV_FILE = 'cli-env'
export const CLI_ENV_STATE = 'cli-env.json'
export const REFRESH_MARGIN_MS = 5 * 60_000

export interface CliEnvState {
  host: string
  codeHost?: string
  expiresAt: string
  env: Record<string, string>
}

const ENV_NAME = /^[A-Z_][A-Z0-9_]*$/

/** POSIX-shell single-quote escaping. */
export function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

export function renderCliEnv(env: Record<string, string>): string {
  const lines = ['# Written by flow-runner; refreshed before expiry. Do not edit.']
  for (const [name, value] of Object.entries(env).sort(([a], [b]) => a.localeCompare(b))) {
    if (!ENV_NAME.test(name)) continue
    lines.push(`export ${name}=${shellQuote(value)}`)
  }
  return lines.join('\n') + '\n'
}

/** Normalizes the CLI env Flow returned (GHE needs GH_ENTERPRISE_TOKEN; glab needs a config dir). */
export function normalizeCliEnv(env: Record<string, string>, home: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [name, value] of Object.entries(env)) {
    if (ENV_NAME.test(name) && typeof value === 'string') out[name] = value
  }
  if (out.GH_TOKEN && out.GH_HOST && out.GH_HOST !== 'github.com' && !out.GH_ENTERPRISE_TOKEN) {
    out.GH_ENTERPRISE_TOKEN = out.GH_TOKEN
  }
  if (out.GITLAB_TOKEN) {
    // The glab env track J defines: token, host, protocol, config dir, no prompts.
    if (!out.GLAB_CONFIG_DIR) out.GLAB_CONFIG_DIR = join(home, '.config', 'glab-cli')
    if (!out.GITLAB_API_PROTOCOL) out.GITLAB_API_PROTOCOL = 'https'
    if (!out.NO_PROMPT) out.NO_PROMPT = '1'
  }
  return out
}

export interface CliEnvDeps {
  credentials: (request: CredentialRequest) => Promise<CredentialResponse>
  flowDir: string
  home: string
  /** The repository host (github.com, a GHE host, gitlab.com, a self-managed GitLab). */
  host: string
  codeHost: string
  run?: Runner
  glabPath?: string
  now?: () => number
  logger?: Logger
}

async function writeAtomic(path: string, content: string): Promise<void> {
  const tmp = `${path}.tmp-${process.pid}`
  await writeFile(tmp, content, { mode: 0o600 })
  await chmod(tmp, 0o600)
  await rename(tmp, path)
}

export async function readCliEnvState(flowDir: string): Promise<CliEnvState | null> {
  try {
    return JSON.parse(await readFile(join(flowDir, CLI_ENV_STATE), 'utf8')) as CliEnvState
  } catch {
    return null
  }
}

/** Fetches a fresh CLI credential and rewrites ~/.flow/cli-env. */
export async function refreshCliEnv(deps: CliEnvDeps): Promise<CliEnvState> {
  const response = await deps.credentials({ protocol: 'https', host: deps.host, purpose: 'cli' })
  const env = normalizeCliEnv(response.cliEnv ?? {}, deps.home)
  await mkdir(deps.flowDir, { recursive: true, mode: 0o700 })
  const state: CliEnvState = { host: deps.host, codeHost: deps.codeHost, expiresAt: response.expiresAt, env }
  await writeAtomic(join(deps.flowDir, CLI_ENV_FILE), renderCliEnv(env))
  await writeAtomic(join(deps.flowDir, CLI_ENV_STATE), JSON.stringify({ host: state.host, codeHost: state.codeHost, expiresAt: state.expiresAt, env: {} }))
  if (deps.codeHost === 'gitlab' && env.GITLAB_TOKEN && deps.host && deps.host !== 'gitlab.com') {
    // Self-managed GitLab: make it glab's default host (the token itself stays in the env).
    await mkdir(env.GLAB_CONFIG_DIR, { recursive: true, mode: 0o700 })
    const runner = deps.run ?? run
    for (const args of glabConfigCommands(deps.host, env.GITLAB_API_PROTOCOL)) {
      const result = await runner(deps.glabPath ?? '/usr/bin/glab', args, {
        env: { ...process.env, ...env, GITLAB_TOKEN: '' },
        check: false,
        timeoutMs: 30_000,
      })
      if (result.code !== 0) deps.logger?.warn('glab config set failed', { args: args.join(' '), stderr: result.stderr.slice(-500) })
    }
  }
  return state
}

/** `glab config set` commands for a self-managed GitLab host (the token stays in the env). */
export function glabConfigCommands(host: string, protocol = 'https'): string[][] {
  return [
    ['config', 'set', '-g', 'host', host],
    ['config', 'set', '-h', host, 'api_protocol', protocol],
    ['config', 'set', '-h', host, 'git_protocol', 'https'],
  ]
}

/** Refreshes the CLI env when it is missing or expires within the margin. */
export async function ensureCliEnv(deps: CliEnvDeps): Promise<CliEnvState> {
  const now = (deps.now ?? Date.now)()
  const state = await readCliEnvState(deps.flowDir)
  if (state && state.host === deps.host && Date.parse(state.expiresAt) - now > REFRESH_MARGIN_MS) return state
  return refreshCliEnv(deps)
}

/** Next refresh delay for an expiry: margin before it, at least 30s, at most 50 min. */
export function refreshDelay(expiresAt: string, now: number): number {
  const expiry = Date.parse(expiresAt)
  if (!Number.isFinite(expiry)) return 10 * 60_000
  return Math.min(50 * 60_000, Math.max(30_000, expiry - now - REFRESH_MARGIN_MS))
}

/** Keeps ~/.flow/cli-env fresh in the background. */
export class CliEnvRefresher {
  private timer: NodeJS.Timeout | undefined
  private stopped = false
  private readonly logger: Logger

  constructor(private readonly deps: CliEnvDeps) {
    this.logger = deps.logger ?? silentLogger
  }

  async start(): Promise<void> {
    await this.tick()
  }

  stop(): void {
    this.stopped = true
    if (this.timer) clearTimeout(this.timer)
  }

  private async tick(): Promise<void> {
    if (this.stopped) return
    let delay = 60_000
    try {
      const state = await refreshCliEnv(this.deps)
      delay = refreshDelay(state.expiresAt, (this.deps.now ?? Date.now)())
    } catch (error) {
      this.logger.warn('refreshing gh/glab credentials failed', { error: errorMessage(error) })
    }
    if (this.stopped) return
    this.timer = setTimeout(() => void this.tick(), delay)
    this.timer.unref?.()
  }
}

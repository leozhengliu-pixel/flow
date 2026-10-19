import { readFileSync } from 'node:fs'
import { homedir } from 'node:os'
import { join } from 'node:path'
import { RUNNER_MODES, SANDBOX_TOKEN_PREFIX, type RunnerMode } from './protocol.js'

/** Fixed locations inside the sandbox image (§9, §16). */
export interface RunnerPaths {
  workspace: string
  repo: string
  home: string
  /** Runner-private state: cli-env, runtime.json, mcp.json, guidance. */
  flowDir: string
  /** Shims shipped in the image: flow-runner, flow-git-credential, flow-sign, gh, glab. */
  binDir: string
}

export interface RunnerConfig {
  runtimeUrl: string
  tokenFile: string
  sandboxId: string
  mode: RunnerMode
  paths: RunnerPaths
  /** Fallback when bootstrap does not carry repo.branchPrefix. */
  branchPrefix: string
  /** ulimit -u for harness processes (SandboxSize.PIDs), 0 = unchanged. */
  pidsLimit: number
}

export class ConfigError extends Error {}

export function defaultPaths(env: NodeJS.ProcessEnv = process.env): RunnerPaths {
  const home = env.HOME || homedir()
  const workspace = env.FLOW_WORKSPACE_DIR || '/workspace'
  return {
    workspace,
    repo: env.FLOW_REPO_DIR || join(workspace, 'repo'),
    home,
    flowDir: join(home, '.flow'),
    binDir: env.FLOW_BIN_DIR || '/opt/flow/bin',
  }
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env, modeArg?: string): RunnerConfig {
  const runtimeUrl = (env.FLOW_RUNTIME_URL || '').replace(/\/+$/, '')
  if (!runtimeUrl) throw new ConfigError('FLOW_RUNTIME_URL is required')
  const tokenFile = env.FLOW_TOKEN_FILE || '/var/run/flow/token'
  const mode = (modeArg || env.FLOW_RUNNER_MODE || 'session') as RunnerMode
  if (!RUNNER_MODES.includes(mode)) throw new ConfigError(`unknown runner mode ${JSON.stringify(mode)}`)
  const pids = Number.parseInt(env.FLOW_PIDS_LIMIT || '0', 10)
  return {
    runtimeUrl,
    tokenFile,
    sandboxId: env.FLOW_SANDBOX_ID || '',
    mode,
    paths: defaultPaths(env),
    branchPrefix: env.FLOW_CODING_BRANCH_PREFIX || 'flow/',
    pidsLimit: Number.isFinite(pids) && pids > 0 ? pids : 0,
  }
}

/**
 * Reads the sandbox token. It is re-read on every call because Flow may
 * rotate the Secret (the kubelet updates the mounted file in place).
 */
export function readToken(tokenFile: string): string {
  let token: string
  try {
    token = readFileSync(tokenFile, 'utf8').trim()
  } catch (error) {
    throw new ConfigError(`cannot read sandbox token from ${tokenFile}: ${(error as Error).message}`)
  }
  if (!token.startsWith(SANDBOX_TOKEN_PREFIX)) throw new ConfigError(`sandbox token in ${tokenFile} has an unexpected format`)
  return token
}

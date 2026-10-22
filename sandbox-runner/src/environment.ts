import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import type { RunnerConfig } from './config.js'
import type { McpServerCommand } from './harness/types.js'
import type { BootstrapResponse } from './protocol.js'
import { runnerProxyEnv } from './proxy-env.js'
import { envVars } from './setup.js'
import { publishes, scopeGuidance, type TurnScope } from './turn-scope.js'

// The environment every child of the runner gets (setup commands, the
// harness and the tools it runs), the runtime.json the shims fall back to,
// and the runner's part of the agent guidance.

export interface RuntimeInfo {
  runtimeUrl: string
  tokenFile: string
  sandboxId: string
}

export const RUNTIME_INFO_FILE = 'runtime.json'

/** Writes ~/.flow/runtime.json (no secrets: the token stays in its file). */
export async function writeRuntimeInfo(config: RunnerConfig): Promise<void> {
  await mkdir(config.paths.flowDir, { recursive: true, mode: 0o700 })
  const info: RuntimeInfo = { runtimeUrl: config.runtimeUrl, tokenFile: config.tokenFile, sandboxId: config.sandboxId }
  await writeFile(join(config.paths.flowDir, RUNTIME_INFO_FILE), JSON.stringify(info), { mode: 0o600 })
}

export interface BaseEnvOptions {
  snapshot?: boolean
}

/** Variables the runner never passes on (they belong to the runner itself). */
const RUNNER_ONLY = ['FLOW_RUNNER_MODE', 'NODE_OPTIONS']

export function baseEnv(config: RunnerConfig, bootstrap: BootstrapResponse | undefined, options: BaseEnvOptions = {}, parent: NodeJS.ProcessEnv = process.env): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...parent }
  for (const name of RUNNER_ONLY) delete env[name]
  const miseShims = join(config.paths.home, '.local', 'share', 'mise', 'shims')
  const path = (parent.PATH || '/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin').split(':').filter((entry) => entry && entry !== config.paths.binDir && entry !== miseShims)
  Object.assign(env, envVars(bootstrap?.environment.mise?.environmentVariables))
  Object.assign(env, {
    HOME: config.paths.home,
    PATH: [config.paths.binDir, miseShims, ...path].join(':'),
    FLOW_SANDBOX: '1',
    LINEAR_SANDBOX: '1',
    FLOW_RUNTIME_URL: config.runtimeUrl,
    FLOW_TOKEN_FILE: config.tokenFile,
    FLOW_SANDBOX_ID: config.sandboxId,
    FLOW_REPO_DIR: config.paths.repo,
    GIT_TERMINAL_PROMPT: '0',
  })
  if (options.snapshot) {
    env.FLOW_SNAPSHOT = '1'
    env.LINEAR_SNAPSHOT = '1'
  } else {
    delete env.FLOW_SNAPSHOT
    delete env.LINEAR_SNAPSHOT
  }
  if (bootstrap?.git.committerName) env.GIT_COMMITTER_NAME = bootstrap.git.committerName
  if (bootstrap?.git.committerEmail) env.GIT_COMMITTER_EMAIL = bootstrap.git.committerEmail
  // The egress proxy needs this sandbox's identity; the token is read now
  // (it may have been rewritten since the last spawn).
  Object.assign(env, runnerProxyEnv(env, config.sandboxId, config.tokenFile))
  return env
}

/** How harnesses start the Flow MCP server for a turn. */
export function mcpCommand(config: RunnerConfig, bootstrap: BootstrapResponse, turnId: string): McpServerCommand {
  return {
    command: join(config.paths.binDir, 'flow-runner'),
    args: ['mcp'],
    env: {
      HOME: config.paths.home,
      PATH: [config.paths.binDir, '/usr/local/bin', '/usr/bin', '/bin'].join(':'),
      FLOW_RUNTIME_URL: config.runtimeUrl,
      FLOW_TOKEN_FILE: config.tokenFile,
      FLOW_SANDBOX_ID: config.sandboxId,
      FLOW_TURN_ID: turnId,
      FLOW_REPO_DIR: config.paths.repo,
      FLOW_BROWSER: bootstrap.browser.enabled ? '1' : '0',
      FLOW_SANDBOX: '1',
    },
  }
}

/** The runner's rules for the agent, followed by Flow's bootstrap guidance. */
export function runnerGuidance(bootstrap: BootstrapResponse, repoDir: string, turn?: { scope: TurnScope; leftovers: string[] }): string {
  const repo = bootstrap.repo.fullName || bootstrap.repo.cloneUrl
  const cli = bootstrap.repo.codeHost === 'gitlab' ? '`glab` (e.g. `glab ci status`, `glab mr view`)' : '`gh` (e.g. `gh pr checks`, `gh pr view`)'
  const rules = [
    '## Flow coding sandbox',
    `- You are working in an isolated Flow sandbox on branch \`${bootstrap.repo.branchName}\` of ${repo}, in ${repoDir}. FLOW_SANDBOX=1 is set.`,
    '- When a turn that may change the repository ends, Flow commits the changes made in that turn, pushes the branch and opens or updates the draft pull/merge request. A turn whose request asks to keep the repository or the pull request unchanged, or not to commit or push, is not committed or pushed. Do not create pull or merge requests yourself, do not push other branches and do not rewrite pushed history.',
    ...(bootstrap.git.coAuthorTrailer ? ['- Every commit in this sandbox automatically gets a `Co-Authored-By:` trailer (added by a git hook). Do not add a co-author trailer or a "Generated with" line to commit messages yourself.'] : []),
    `- ${cli} is authenticated for reading CI status, checks and review comments.`,
    '- If you cannot continue without a decision from the user, call the `ask_user` tool (Flow MCP server) and then end your turn.',
    turn && !publishes(turn.scope)
      ? '- Finish with a short report of what you checked or did and what you found.'
      : '- Finish with a short summary of what you changed. Its first line becomes the commit message, so make it a concise imperative sentence.',
  ].join('\n')
  // The current turn comes last: it sets this turn's goal and write scope.
  const parts = [rules, bootstrap.guidance?.trim(), turn ? scopeGuidance(turn.scope, turn.leftovers) : ''].filter(Boolean)
  return `${parts.join('\n\n')}\n`
}

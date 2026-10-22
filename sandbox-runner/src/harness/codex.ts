import { join } from 'node:path'
import { CodexMapper } from '../mapping/codex.js'
import type { MapperOptions, StreamMapper } from '../mapping/common.js'
import type { Logger } from '../log.js'
import { gatewayMcpServers, turnPrompt, withoutProviderKeys, type HarnessAdapter, type HarnessContext, type HarnessInvocation } from './types.js'

// Codex CLI, pinned in the image (deploy/sandbox-image/Dockerfile ARGs).
// First turn:  codex exec --json --model <m> -c model_reasoning_effort="<e>"
//              --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check
//              -C /workspace/repo -      (prompt on stdin)
// Follow-up:   codex exec resume <thread_id> --json … -   (cwd /workspace/repo)
// ~/.codex/config.toml selects the "flow" model provider: Flow's LLM proxy
// (Responses API) with the sandbox token read from FLOW_LLM_TOKEN. Guidance
// goes to ~/.codex/AGENTS.override.md (global instructions).

/** A TOML basic string (JSON escapes are valid TOML escapes). */
export function tomlString(value: string): string {
  return JSON.stringify(value)
}

function tomlInlineTable(values: Record<string, string>): string {
  const entries = Object.entries(values).map(([key, value]) => `${/^[A-Za-z0-9_-]+$/.test(key) ? key : tomlString(key)} = ${tomlString(value)}`)
  return `{ ${entries.join(', ')} }`
}

// Bounds of harness.contextWindow, mirroring the API's MinContextWindow /
// MaxContextWindow (api/internal/config/coding.go).
export const MIN_CONTEXT_WINDOW = 1024
export const MAX_CONTEXT_WINDOW = 10_000_000

/**
 * The bootstrap's context window when usable: an integer number within the
 * bounds. Anything else (string, negative, fractional, out of range) is
 * ignored with a warning (defence in depth; the API validates first), so
 * Codex keeps its default.
 */
export function codexContextWindow(value: unknown, logger?: Logger): number | undefined {
  if (value === undefined || value === null) return undefined
  if (typeof value === 'number' && Number.isSafeInteger(value) && value >= MIN_CONTEXT_WINDOW && value <= MAX_CONTEXT_WINDOW) return value
  logger?.warn('ignoring invalid harness.contextWindow', { value: String(value).slice(0, 40), type: typeof value })
  return undefined
}

export function codexConfig(ctx: HarnessContext): string {
  const { bootstrap } = ctx
  const lines = [
    '# Written by flow-runner for every turn. Model traffic goes to Flow only.',
    `model = ${tomlString(bootstrap.harness.model)}`,
    'model_provider = "flow"',
  ]
  if (bootstrap.harness.reasoningEffort) lines.push(`model_reasoning_effort = ${tomlString(bootstrap.harness.reasoningEffort)}`)
  // The model's context window, an integer TOML value. Flow never writes
  // model_auto_compact_token_limit: Codex keeps its own threshold.
  const contextWindow = codexContextWindow(bootstrap.harness.contextWindow, ctx.logger)
  if (contextWindow !== undefined) lines.push(`model_context_window = ${contextWindow}`)
  lines.push(
    'approval_policy = "never"',
    'sandbox_mode = "danger-full-access"',
    'check_for_update_on_startup = false',
    '',
    '[analytics]',
    'enabled = false',
    '',
    '[model_providers.flow]',
    'name = "Flow"',
    `base_url = ${tomlString(bootstrap.llm.openaiBaseUrl)}`,
    'env_key = "FLOW_LLM_TOKEN"',
    'wire_api = "responses"',
    'requires_openai_auth = false',
    '',
    '[mcp_servers.flow]',
    `command = ${tomlString(ctx.mcp.command)}`,
    `args = [${ctx.mcp.args.map(tomlString).join(', ')}]`,
    `env = ${tomlInlineTable(ctx.mcp.env)}`,
    'startup_timeout_sec = 30',
    '',
  )
  // Workspace MCP servers behind Flow's gateway (streamable HTTP); the
  // bearer is the sandbox token, read from the env like the model key.
  for (const server of gatewayMcpServers(bootstrap)) {
    lines.push(`[mcp_servers.${server.name}]`, `url = ${tomlString(server.url)}`, 'bearer_token_env_var = "FLOW_LLM_TOKEN"', 'startup_timeout_sec = 30', 'tool_timeout_sec = 360', '')
  }
  return lines.join('\n')
}

export const codexHarness: HarnessAdapter = {
  name: 'codex',

  invocation(ctx: HarnessContext): HarnessInvocation {
    const { bootstrap } = ctx
    const codexHome = join(ctx.paths.home, '.codex')
    const common = ['--json', '--dangerously-bypass-approvals-and-sandbox', '--skip-git-repo-check']
    if (bootstrap.harness.model) common.push('--model', bootstrap.harness.model)
    if (bootstrap.harness.reasoningEffort) common.push('-c', `model_reasoning_effort=${tomlString(bootstrap.harness.reasoningEffort)}`)
    const args = ctx.resumeId
      ? ['exec', 'resume', ctx.resumeId, ...common, '-']
      : ['exec', ...common, '-C', ctx.paths.repo, '-']
    const env: NodeJS.ProcessEnv = {
      ...withoutProviderKeys(ctx.env),
      CODEX_HOME: codexHome,
      FLOW_LLM_TOKEN: ctx.token,
    }
    return {
      command: 'codex',
      args,
      env,
      stdin: turnPrompt(ctx.turn),
      cwd: ctx.paths.repo,
      files: [
        { path: join(codexHome, 'config.toml'), content: codexConfig(ctx), mode: 0o600 },
        { path: join(codexHome, 'AGENTS.override.md'), content: ctx.guidance, mode: 0o600 },
      ],
    }
  },

  createMapper(options: MapperOptions): StreamMapper {
    return new CodexMapper(options)
  },

  stateDirs: () => ['.codex/sessions', '.codex/session_index.jsonl', '.codex/state'],
}

import { join } from 'node:path'
import { CodexMapper } from '../mapping/codex.js'
import type { MapperOptions, StreamMapper } from '../mapping/common.js'
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

export function codexConfig(ctx: HarnessContext): string {
  const { bootstrap } = ctx
  const lines = [
    '# Written by flow-runner for every turn. Model traffic goes to Flow only.',
    `model = ${tomlString(bootstrap.harness.model)}`,
    'model_provider = "flow"',
  ]
  if (bootstrap.harness.reasoningEffort) lines.push(`model_reasoning_effort = ${tomlString(bootstrap.harness.reasoningEffort)}`)
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

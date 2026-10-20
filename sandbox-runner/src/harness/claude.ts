import { join } from 'node:path'
import { ClaudeMapper } from '../mapping/claude.js'
import type { MapperOptions, StreamMapper } from '../mapping/common.js'
import { gatewayMcpServers, turnPrompt, withoutProviderKeys, type HarnessAdapter, type HarnessContext, type HarnessInvocation } from './types.js'

// Claude Code, pinned in the image (deploy/sandbox-image/Dockerfile ARGs).
// First turn:  claude -p --output-format stream-json --verbose --model <m>
//              --permission-mode bypassPermissions --append-system-prompt <g>
//              --mcp-config ~/.flow/mcp.json   (prompt on stdin)
// Follow-up:   the same plus --resume <session_id from system/init>.
// Model traffic goes to Flow's LLM proxy: ANTHROPIC_BASE_URL plus the
// sandbox token as ANTHROPIC_AUTH_TOKEN; no provider key enters the sandbox.

/** MAX_THINKING_TOKENS per reasoning effort (§12). */
export const CLAUDE_THINKING_TOKENS: Record<string, string> = { low: '4000', medium: '16000', high: '32000' }

/** Linux limits a single argv string to 128 KiB. */
const MAX_ARG_BYTES = 120 * 1024

/**
 * The runner's own "flow" server plus the workspace MCP servers behind
 * Flow's gateway. The gateway takes the sandbox token (already on disk at
 * the runner's token file); the servers' own credentials stay in Flow.
 */
export function claudeMcpConfig(ctx: HarnessContext): string {
  const mcpServers: Record<string, unknown> = { flow: { type: 'stdio', command: ctx.mcp.command, args: ctx.mcp.args, env: ctx.mcp.env } }
  for (const server of gatewayMcpServers(ctx.bootstrap)) {
    mcpServers[server.name] = { type: 'http', url: server.url, headers: { Authorization: `Bearer ${ctx.token}` } }
  }
  return JSON.stringify({ mcpServers }, null, 2)
}

export const claudeHarness: HarnessAdapter = {
  name: 'claudeCode',

  invocation(ctx: HarnessContext): HarnessInvocation {
    const { bootstrap } = ctx
    const mcpPath = join(ctx.paths.flowDir, 'mcp.json')
    const args = ['-p', '--output-format', 'stream-json', '--verbose', '--permission-mode', 'bypassPermissions', '--mcp-config', mcpPath]
    if (bootstrap.harness.model) args.push('--model', bootstrap.harness.model)
    let guidance = ctx.guidance
    if (Buffer.byteLength(guidance) > MAX_ARG_BYTES) guidance = Buffer.from(guidance).subarray(0, MAX_ARG_BYTES).toString('utf8')
    if (guidance.trim()) args.push('--append-system-prompt', guidance)
    if (ctx.resumeId) args.push('--resume', ctx.resumeId)
    const env: NodeJS.ProcessEnv = {
      ...withoutProviderKeys(ctx.env),
      ANTHROPIC_BASE_URL: bootstrap.llm.anthropicBaseUrl,
      ANTHROPIC_AUTH_TOKEN: ctx.token,
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
      DISABLE_AUTOUPDATER: '1',
      DISABLE_TELEMETRY: '1',
      DISABLE_ERROR_REPORTING: '1',
      IS_SANDBOX: '1',
    }
    if (bootstrap.harness.model) env.ANTHROPIC_MODEL = bootstrap.harness.model
    const thinking = CLAUDE_THINKING_TOKENS[bootstrap.harness.reasoningEffort ?? '']
    if (thinking) env.MAX_THINKING_TOKENS = thinking
    return {
      command: 'claude',
      args,
      env,
      stdin: turnPrompt(ctx.turn),
      cwd: ctx.paths.repo,
      files: [{ path: mcpPath, content: claudeMcpConfig(ctx), mode: 0o600 }],
    }
  },

  createMapper(options: MapperOptions): StreamMapper {
    return new ClaudeMapper(options)
  },

  stateDirs: () => ['.claude', '.claude.json'],
}

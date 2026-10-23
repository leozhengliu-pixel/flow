import { join } from 'node:path'
import type { MapperOptions, StreamMapper } from '../mapping/common.js'
import { OpenCodeMapper } from '../mapping/opencode.js'
import { gatewayMcpServers, turnPrompt, withoutProviderKeys, type HarnessAdapter, type HarnessContext, type HarnessInvocation } from './types.js'

// OpenCode for the "Open source" harness option, pinned in the image.
// First turn:  opencode run --format json --auto --thinking -m flow/<m> [--variant <e>]
//              (prompt on stdin; opencode reads stdin when it is not a TTY)
// Follow-up:   the same plus --session <sessionID>.
// ~/.flow/opencode.json (OPENCODE_CONFIG) declares the provider "flow": an
// OpenAI-compatible endpoint at Flow's LLM proxy, keyed by the sandbox token.

export function opencodeConfig(ctx: HarnessContext): string {
  const { bootstrap } = ctx
  const model = bootstrap.harness.model
  const mcp: Record<string, unknown> = {
    flow: { type: 'local', command: [ctx.mcp.command, ...ctx.mcp.args], environment: ctx.mcp.env, enabled: true },
  }
  // Workspace MCP servers behind Flow's gateway, keyed by the sandbox token.
  for (const server of gatewayMcpServers(bootstrap)) {
    mcp[server.name] = { type: 'remote', url: server.url, headers: { Authorization: 'Bearer {env:FLOW_LLM_TOKEN}' }, enabled: true }
  }
  const config = {
    $schema: 'https://opencode.ai/config.json',
    model: `flow/${model}`,
    small_model: `flow/${model}`,
    autoupdate: false,
    share: 'disabled',
    instructions: [join(ctx.paths.flowDir, 'guidance.md')],
    permission: { edit: 'allow', bash: 'allow', webfetch: 'allow' },
    provider: {
      flow: {
        npm: '@ai-sdk/openai-compatible',
        name: 'Flow',
        options: { baseURL: bootstrap.llm.openSourceBaseUrl, apiKey: '{env:FLOW_LLM_TOKEN}' },
        models: { [model]: { name: model } },
      },
    },
    mcp,
  }
  return JSON.stringify(config, null, 2)
}

export const opencodeHarness: HarnessAdapter = {
  name: 'openSource',

  invocation(ctx: HarnessContext): HarnessInvocation {
    const { bootstrap } = ctx
    const configPath = join(ctx.paths.flowDir, 'opencode.json')
    const args = ['run', '--format', 'json', '--auto', '--thinking', '-m', `flow/${bootstrap.harness.model}`]
    if (bootstrap.harness.reasoningEffort) args.push('--variant', bootstrap.harness.reasoningEffort)
    if (ctx.resumeId) args.push('--session', ctx.resumeId)
    const env: NodeJS.ProcessEnv = {
      ...withoutProviderKeys(ctx.env),
      OPENCODE_CONFIG: configPath,
      OPENCODE_DISABLE_AUTOUPDATE: '1',
      OPENCODE_DISABLE_LSP_DOWNLOAD: '1',
      FLOW_LLM_TOKEN: ctx.token,
    }
    return {
      command: 'opencode',
      args,
      env,
      stdin: turnPrompt(ctx.turn),
      cwd: ctx.paths.repo,
      files: [
        { path: configPath, content: opencodeConfig(ctx), mode: 0o600 },
        { path: join(ctx.paths.flowDir, 'guidance.md'), content: ctx.guidance, mode: 0o600 },
      ],
    }
  },

  createMapper(options: MapperOptions): StreamMapper {
    return new OpenCodeMapper(options)
  },

  stateDirs: () => ['.local/share/opencode'],
}

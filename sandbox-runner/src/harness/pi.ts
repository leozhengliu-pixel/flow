import { join } from 'node:path'
import type { MapperOptions, StreamMapper } from '../mapping/common.js'
import { PiMapper } from '../mapping/pi.js'
import { codexContextWindow } from './codex.js'
import { gatewayMcpServers, turnPrompt, withoutProviderKeys, type HarnessAdapter, type HarnessContext, type HarnessInvocation } from './types.js'

// Pi (@earendil-works/pi-coding-agent), pinned in the image
// (deploy/sandbox-image/Dockerfile ARGs).
// First turn:  pi --mode json --no-approve --provider flow --model <m>
//              [--thinking <effort>]          (prompt on stdin)
// Follow-up:   the same plus --session <id of the session header>.
// PI_CODING_AGENT_DIR=~/.pi/agent holds the config the runner writes before
// every turn: models.json declares the provider "flow", an OpenAI-compatible
// endpoint at Flow's LLM proxy keyed by the sandbox token ($FLOW_LLM_TOKEN);
// mcp.json the MCP servers; settings.json turns project trust off;
// APPEND_SYSTEM.md carries the guidance. --no-approve (plus
// defaultProjectTrust "never") keeps a repository's .pi/ directory from
// adding settings, MCP servers, extensions or system prompts.

/** Pi's agent directory under the sandbox home. */
export function piAgentDir(home: string): string {
  return join(home, '.pi', 'agent')
}

/**
 * models.json: one custom provider. The compat fields keep the request shape
 * plain OpenAI Chat Completions (system role, max_tokens, no store), which
 * self-hosted servers behind the open-source upstream accept; Pi would
 * otherwise send OpenAI-only fields to an unknown URL. Reasoning is declared
 * only when a reasoning effort is set, so Pi sends reasoning_effort only then.
 */
export function piModelsConfig(ctx: HarnessContext): string {
  const { bootstrap } = ctx
  const model: Record<string, unknown> = { id: bootstrap.harness.model, name: bootstrap.harness.model, reasoning: Boolean(bootstrap.harness.reasoningEffort) }
  const contextWindow = codexContextWindow(bootstrap.harness.contextWindow, ctx.logger)
  if (contextWindow !== undefined) model.contextWindow = contextWindow
  const config = {
    providers: {
      flow: {
        name: 'Flow',
        baseUrl: bootstrap.llm.openSourceBaseUrl,
        api: 'openai-completions',
        apiKey: '$FLOW_LLM_TOKEN',
        compat: { supportsStore: false, supportsDeveloperRole: false, maxTokensField: 'max_tokens' },
        models: [model],
      },
    },
  }
  return JSON.stringify(config, null, 2)
}

/**
 * mcp.json (user level; project .pi/mcp.json is never read). Flow's own
 * server is "direct": its tools (ask_user, browser…) are declared to the
 * model like built-in tools. The workspace servers behind Flow's gateway are
 * "deferred": they are listed in the system prompt with their title and the
 * model loads their tools with tool_search when it needs them. Connector
 * servers can carry dozens of tools, so declaring them all on every request
 * would bloat each prompt and make the first prompt wait for every gateway
 * connection; deferred keeps the prompt small and its cache stable.
 */
export function piMcpConfig(ctx: HarnessContext): string {
  const mcpServers: Record<string, unknown> = {
    flow: { command: ctx.mcp.command, args: ctx.mcp.args, env: ctx.mcp.env, exposure: 'direct' },
  }
  for (const server of gatewayMcpServers(ctx.bootstrap)) {
    mcpServers[server.name] = {
      url: server.url,
      headers: { Authorization: 'Bearer ${FLOW_LLM_TOKEN}' },
      exposure: 'deferred',
      description: server.title || server.name,
      timeout: 360,
    }
  }
  return JSON.stringify({ mcpServers }, null, 2)
}

export function piSettings(): string {
  return JSON.stringify(
    {
      defaultProjectTrust: 'never',
      enableInstallTelemetry: false,
      quietStartup: true,
      extensions: ['-builtin:llama.cpp'],
    },
    null,
    2,
  )
}

export const piHarness: HarnessAdapter = {
  name: 'pi',

  invocation(ctx: HarnessContext): HarnessInvocation {
    const { bootstrap } = ctx
    const agentDir = piAgentDir(ctx.paths.home)
    const args = ['--mode', 'json', '--no-approve', '--provider', 'flow', '--model', bootstrap.harness.model]
    if (bootstrap.harness.reasoningEffort) args.push('--thinking', bootstrap.harness.reasoningEffort)
    if (ctx.resumeId) args.push('--session', ctx.resumeId)
    const env: NodeJS.ProcessEnv = {
      ...withoutProviderKeys(ctx.env),
      PI_CODING_AGENT_DIR: agentDir,
      PI_OFFLINE: '1',
      PI_SKIP_VERSION_CHECK: '1',
      PI_TELEMETRY: '0',
      FLOW_LLM_TOKEN: ctx.token,
    }
    delete env.PI_CODING_AGENT_SESSION_DIR
    return {
      command: 'pi',
      args,
      env,
      stdin: turnPrompt(ctx.turn),
      cwd: ctx.paths.repo,
      files: [
        { path: join(agentDir, 'models.json'), content: piModelsConfig(ctx), mode: 0o600 },
        { path: join(agentDir, 'mcp.json'), content: piMcpConfig(ctx), mode: 0o600 },
        { path: join(agentDir, 'settings.json'), content: piSettings(), mode: 0o600 },
        { path: join(agentDir, 'APPEND_SYSTEM.md'), content: ctx.guidance, mode: 0o600 },
      ],
    }
  },

  createMapper(options: MapperOptions): StreamMapper {
    return new PiMapper(options)
  },

  stateDirs: () => ['.pi/agent/sessions'],
}

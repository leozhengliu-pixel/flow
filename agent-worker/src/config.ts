import { readFileSync } from 'node:fs'

/**
 * Worker configuration. Secrets come from env or `<NAME>_FILE` and are never logged.
 *
 * FLOW_WORKER_PROTOCOL          openai-responses | openai-chat | anthropic
 * FLOW_WORKER_MODEL             provider model id (gpt-6.1-sol, claude-sonnet-5, deepseek-chat, …)
 * FLOW_WORKER_BASE_URL          provider base URL (OpenAI-compatible root, or Anthropic root ending in /v1); a
 *                               per-protocol FLOW_WORKER_{RESPONSES,CHAT,ANTHROPIC}_BASE_URL wins
 * FLOW_WORKER_API_KEY           provider key (or FLOW_WORKER_API_KEY_FILE)
 * FLOW_WORKER_REASONING_EFFORT  none | minimal | low | medium | high (unset: the provider's default)
 * FLOW_WORKER_FLOW_URL          Flow API base, e.g. http://127.0.0.1:8080
 * FLOW_WORKER_FLOW_TOKEN        run token for Flow /mcp (or FLOW_WORKER_FLOW_TOKEN_FILE)
 * FLOW_WORKER_STATE_DIR         session + run-state store root (default ./.runtime)
 */
export type Protocol = 'openai-responses' | 'openai-chat' | 'anthropic'

export type ModelConfig = {
  protocol: Protocol
  model: string
  baseURL?: string
  apiKey: string
  reasoningEffort?: 'none' | 'minimal' | 'low' | 'medium' | 'high'
  /** Context window in tokens, for history trimming. */
  contextWindow: number
}

export type WorkerConfig = ModelConfig & {
  flowUrl: string
  flowToken: string
  stateDir: string
  maxTurns: number
}

export function secret(name: string, fallbacks: string[] = []): string {
  for (const key of [name, ...fallbacks]) {
    const direct = process.env[key]
    if (direct) return direct.trim()
    const file = process.env[`${key}_FILE`]
    if (file) return readFileSync(file, 'utf8').trim()
  }
  throw new Error(`${name} or ${name}_FILE is required`)
}

const protocols: Protocol[] = ['openai-responses', 'openai-chat', 'anthropic']

export function parseProtocol(value: string | undefined): Protocol {
  const protocol = (value ?? 'openai-responses') as Protocol
  if (!protocols.includes(protocol)) throw new Error(`unknown protocol ${value}; expected ${protocols.join(' | ')}`)
  return protocol
}

const baseURLEnv: Record<Protocol, string> = { 'openai-responses': 'FLOW_WORKER_RESPONSES_BASE_URL', 'openai-chat': 'FLOW_WORKER_CHAT_BASE_URL', anthropic: 'FLOW_WORKER_ANTHROPIC_BASE_URL' }

export function baseURLFor(protocol: Protocol): string | undefined {
  return process.env[baseURLEnv[protocol]] ?? process.env.FLOW_WORKER_BASE_URL ?? (protocol === 'anthropic' ? process.env.ANTHROPIC_BASE_URL : process.env.OPENAI_BASE_URL)
}

export function loadConfig(overrides: Partial<WorkerConfig> = {}): WorkerConfig {
  const protocol = overrides.protocol ?? parseProtocol(process.env.FLOW_WORKER_PROTOCOL)
  const anthropic = protocol === 'anthropic'
  return {
    protocol,
    model: process.env.FLOW_WORKER_MODEL ?? (anthropic ? 'claude-sonnet-5' : 'gpt-6.1-sol'),
    baseURL: baseURLFor(protocol),
    apiKey: overrides.apiKey ?? secret('FLOW_WORKER_API_KEY', [anthropic ? 'ANTHROPIC_API_KEY' : 'OPENAI_API_KEY']),
    reasoningEffort: (process.env.FLOW_WORKER_REASONING_EFFORT || undefined) as ModelConfig['reasoningEffort'],
    contextWindow: Number(process.env.FLOW_WORKER_CONTEXT_WINDOW ?? 200_000),
    flowUrl: process.env.FLOW_WORKER_FLOW_URL ?? 'http://127.0.0.1:8080',
    flowToken: overrides.flowToken ?? secret('FLOW_WORKER_FLOW_TOKEN'),
    stateDir: process.env.FLOW_WORKER_STATE_DIR ?? new URL('../.runtime', import.meta.url).pathname,
    maxTurns: Number(process.env.FLOW_WORKER_MAX_TURNS ?? 24),
    ...overrides,
  }
}

import { createAnthropic } from '@ai-sdk/anthropic'
import type { AgentOutputItem, Model, ModelRequest, ModelResponse, ModelSettings, StreamEvent } from '@openai/agents'
import { OpenAIChatCompletionsModel, OpenAIResponsesModel, retryPolicies } from '@openai/agents'
import { aisdk } from '@openai/agents-extensions/ai-sdk'
import OpenAI from 'openai'
import type { ModelConfig } from './config.js'

/**
 * Model factory: one Agents SDK `Model` per protocol, talking to the provider directly (no gateway).
 *
 *  - openai-responses  OpenAIResponsesModel on any Responses-compatible base URL
 *  - openai-chat       OpenAIChatCompletionsModel on any Chat-Completions-compatible base URL
 *                      (OpenAI, DeepSeek, Qwen/DashScope, vLLM, Ollama, OpenRouter, …)
 *  - anthropic         Anthropic Messages through the AI SDK adapter (`aisdk(@ai-sdk/anthropic)`)
 *
 * `fetch` is injectable so tests can run every protocol against a mocked provider.
 */
export type ModelFactoryOptions = { fetch?: typeof fetch }

export function createModel(config: ModelConfig, options: ModelFactoryOptions = {}): Model {
  switch (config.protocol) {
    case 'openai-responses':
      return new OpenAIResponsesModel(openAIClient(config, options), config.model)
    case 'openai-chat':
      return new OpenAIChatCompletionsModel(openAIClient(config, options), config.model)
    case 'anthropic': {
      const provider = createAnthropic({ apiKey: config.apiKey, ...(config.baseURL ? { baseURL: config.baseURL } : {}), ...(options.fetch ? { fetch: options.fetch } : {}) })
      return aisdk(provider(config.model))
    }
  }
}

function openAIClient(config: ModelConfig, options: ModelFactoryOptions) {
  // Retries belong to the runner's policy (below), so they are visible to the worker and not doubled.
  return new OpenAI({ apiKey: config.apiKey, baseURL: config.baseURL, maxRetries: 0, timeout: 300_000, ...(options.fetch ? { fetch: options.fetch } : {}) })
}

/**
 * Per-protocol request settings: reasoning effort, parallel tool calls on, and a prompt-cache key per session
 * (OpenAI `prompt_cache_key` routes a session's calls to the same cache; Anthropic gets automatic cache_control).
 */
export function modelSettings(config: ModelConfig, sessionId: string): ModelSettings {
  // timeoutMs bounds one model call (a stalled stream otherwise hangs the run for as long as the provider does).
  return { retry, timeoutMs: 240_000, ...protocolSettings(config, sessionId) }
}

/**
 * Runner-managed retries for failures before the first streamed event (network errors, 429/5xx, provider
 * advice). The SDK never replays a stream that already emitted events, so a mid-stream disconnect fails the
 * turn; the worker retries such runs itself when no write happened (design doc §11).
 */
const retry: ModelSettings['retry'] = {
  maxRetries: 2,
  backoff: { initialDelayMs: 500, maxDelayMs: 5_000, multiplier: 2, jitter: true },
  policy: retryPolicies.any(retryPolicies.providerSuggested(), retryPolicies.retryAfter(), retryPolicies.networkError(), retryPolicies.httpStatus([408, 429, 500, 502, 503, 504])),
}

function protocolSettings(config: ModelConfig, sessionId: string): ModelSettings {
  const effort = config.reasoningEffort
  switch (config.protocol) {
    case 'openai-responses':
      return {
        parallelToolCalls: true,
        reasoning: { ...(effort ? { effort } : {}), summary: 'auto' },
        // Stateless: the worker owns history (session store), so nothing is kept at the provider.
        store: false,
        providerData: { prompt_cache_key: sessionId, include: ['reasoning.encrypted_content'] },
      }
    case 'openai-chat':
      return { parallelToolCalls: true, ...(effort ? { reasoning: { effort } } : {}), providerData: { prompt_cache_key: sessionId } }
    case 'anthropic':
      return {
        parallelToolCalls: true,
        maxTokens: 16_000,
        providerData: {
          providerOptions: {
            anthropic: {
              ...(effort ? { thinking: { type: 'adaptive' }, effort: effort === 'none' || effort === 'minimal' ? 'low' : effort } : {}),
              cacheControl: { type: 'ephemeral' },
              disableParallelToolUse: false,
            },
          },
        },
      }
  }
}

/** What one model call looked like; the worker uses it for approvals (calls per turn) and metrics. */
export type ModelCallRecord = {
  startedAt: number
  firstEventAt?: number
  endedAt: number
  toolsOffered: number
  inputTokens: number
  cachedTokens: number
  outputTokens: number
  functionCalls: { callId: string; name: string; arguments: string }[]
  error?: string
}

export type ModelObserver = (record: ModelCallRecord) => void

/**
 * Wraps a Model so the host sees every model call as it completes, before the runner evaluates the tool calls
 * of that turn. `needsApproval` sees one call at a time; the bulk-delete rule needs the whole turn.
 */
export class ObservedModel implements Model {
  observer?: ModelObserver
  constructor(private readonly inner: Model) {}

  async getResponse(request: ModelRequest): Promise<ModelResponse> {
    const startedAt = Date.now()
    try {
      const response = await this.inner.getResponse(request)
      this.emit(startedAt, request, response.output, response.usage, startedAt)
      return response
    } catch (error) {
      this.observer?.({ startedAt, endedAt: Date.now(), toolsOffered: request.tools.length, inputTokens: 0, cachedTokens: 0, outputTokens: 0, functionCalls: [], error: String(error) })
      throw error
    }
  }

  async *getStreamedResponse(request: ModelRequest): AsyncIterable<StreamEvent> {
    const startedAt = Date.now()
    let firstEventAt: number | undefined
    try {
      for await (const event of this.inner.getStreamedResponse(request)) {
        firstEventAt ??= Date.now()
        if (event.type === 'response_done') this.emit(startedAt, request, event.response.output, event.response.usage, firstEventAt)
        yield event
      }
    } catch (error) {
      this.observer?.({ startedAt, firstEventAt, endedAt: Date.now(), toolsOffered: request.tools.length, inputTokens: 0, cachedTokens: 0, outputTokens: 0, functionCalls: [], error: String(error) })
      throw error
    }
  }

  getRetryAdvice(args: Parameters<NonNullable<Model['getRetryAdvice']>>[0]) {
    return this.inner.getRetryAdvice?.(args)
  }

  private emit(startedAt: number, request: ModelRequest, output: AgentOutputItem[], usage: any, firstEventAt: number) {
    const functionCalls = output.flatMap(item => (item.type === 'function_call' ? [{ callId: item.callId, name: item.name, arguments: item.arguments }] : []))
    this.observer?.({
      startedAt,
      firstEventAt,
      endedAt: Date.now(),
      toolsOffered: request.tools.length,
      inputTokens: Number(usage?.inputTokens ?? usage?.input_tokens ?? 0),
      cachedTokens: cachedTokens(usage),
      outputTokens: Number(usage?.outputTokens ?? usage?.output_tokens ?? 0),
      functionCalls,
    })
  }
}

function cachedTokens(usage: any): number {
  const details = usage?.inputTokensDetails ?? usage?.input_tokens_details
  const list = Array.isArray(details) ? details : details ? [details] : []
  return list.reduce((sum: number, d: Record<string, number>) => sum + Number(d?.cached_tokens ?? d?.cachedTokens ?? d?.cache_read_input_tokens ?? 0), 0)
}

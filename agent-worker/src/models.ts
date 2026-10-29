import { createAnthropic } from '@ai-sdk/anthropic'
import type { AgentOutputItem, Model, ModelRequest, ModelResponse, ModelSettings, StreamEvent } from '@openai/agents'
import { OpenAIChatCompletionsModel, OpenAIResponsesModel, retryPolicies } from '@openai/agents'
import { aisdk } from '@openai/agents-extensions/ai-sdk'
import OpenAI from 'openai'
import { AsyncLocalStorage } from 'node:async_hooks'
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
  const fetchModel = options.fetch ?? fetch
  options = { ...options, fetch: (input, init) => diagnosticResponse(fetchModel, input, init) }
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
  beforeModel?: (request: ModelRequest) => Promise<void>
  afterModel?: (output: ModelResponse['output'], usage: any, diagnostics: ModelDiagnostics) => Promise<ModelResponse['output']>
  modelError?: (diagnostics: ModelDiagnostics) => Promise<void>
  /** Rewrites a finished model turn's output before the runner acts on it (the run guard's forced decision). */
  rewrite?: <T extends { type?: string }>(output: T[]) => T[]
  constructor(private readonly inner: Model) {}

  async getResponse(request: ModelRequest): Promise<ModelResponse> {
    const startedAt = Date.now()
    await this.beforeModel?.(request)
    try {
      const response = await this.inner.getResponse(request)
      if (this.afterModel) response.output = await this.afterModel(response.output, response.usage, { startedAt, events: 1, bytes: Buffer.byteLength(JSON.stringify(response)), finished: true, stopReason: '', stopDetail: '', reasoning: '' })
      if (this.rewrite) response.output = this.rewrite(response.output)
      this.emit(startedAt, request, response.output, response.usage, startedAt)
      return response
    } catch (error) {
      this.observer?.({ startedAt, endedAt: Date.now(), toolsOffered: request.tools.length, inputTokens: 0, cachedTokens: 0, outputTokens: 0, functionCalls: [], error: String(error) })
      throw error
    }
  }

  async *getStreamedResponse(request: ModelRequest): AsyncIterable<StreamEvent> {
    const startedAt = Date.now()
    await this.beforeModel?.(request)
    let firstEventAt: number | undefined
    const diagnostics: ModelDiagnostics = { startedAt, events: 0, bytes: 0, finished: false, stopReason: '', stopDetail: '', reasoning: '' }
    let observed = false
    const stream = this.inner.getStreamedResponse(request)[Symbol.asyncIterator]()
    try {
      for (;;) {
        const next = await modelDiagnostics.run(diagnostics, () => stream.next())
        if (next.done) break
        const event = next.value
        firstEventAt ??= Date.now()
        if (!diagnostics.wire) {
          diagnostics.events++
          diagnostics.bytes += Buffer.byteLength(JSON.stringify(event))
        }
        if (event.type === 'model') {
          const raw = event.event as any
          const stop = raw?.choices?.[0]?.finish_reason ?? raw?.delta?.stop_reason ?? raw?.finishReason ?? raw?.response?.status
          if (typeof stop === 'string') diagnostics.stopReason = stop
          else if (stop && typeof stop === 'object') diagnostics.stopReason = stop.raw ?? stop.unified ?? diagnostics.stopReason
          diagnostics.stopDetail = raw?.response?.incomplete_details?.reason ?? diagnostics.stopDetail
          const reasoning = raw?.choices?.[0]?.delta?.reasoning_content ?? raw?.choices?.[0]?.delta?.reasoning ?? (raw?.type === 'reasoning-delta' ? raw.delta : raw?.type === 'response.reasoning_summary_text.delta' ? raw.delta : '')
          if (typeof reasoning === 'string') diagnostics.reasoning += reasoning
        }
        if (event.type === 'response_done') {
          if (!diagnostics.wire) diagnostics.finished = true
          observed = true
          if (this.afterModel) event.response.output = await this.afterModel(event.response.output, event.response.usage, diagnostics) as typeof event.response.output
        }
        if (event.type === 'response_done' && this.rewrite) event.response.output = this.rewrite(event.response.output)
        if (event.type === 'response_done') this.emit(startedAt, request, event.response.output, event.response.usage, firstEventAt)
        yield event
      }
    } catch (error) {
      if (!observed && this.afterModel && diagnostics.stopReason === 'length') {
        // The Chat adapter throws for a token-limited empty turn; Flow still gives it the normal recovery.
        const usage = { inputTokens: 0, outputTokens: 0, totalTokens: 0 }
        const output = await this.afterModel([], usage, diagnostics)
        this.emit(startedAt, request, output, usage, firstEventAt ?? startedAt)
        yield { type: 'response_done', response: { id: `recovery_${startedAt}`, output, usage } } as StreamEvent
        return
      }
      if (!observed) await this.modelError?.(diagnostics)
      this.observer?.({ startedAt, firstEventAt, endedAt: Date.now(), toolsOffered: request.tools.length, inputTokens: 0, cachedTokens: 0, outputTokens: 0, functionCalls: [], error: String(error) })
      throw error
    } finally {
      await stream.return?.()
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

export type ModelDiagnostics = { startedAt: number; events: number; bytes: number; finished: boolean; stopReason: string; stopDetail: string; reasoning: string; wire?: boolean; unreadable?: number; unknown?: number }

const modelDiagnostics = new AsyncLocalStorage<ModelDiagnostics>()

/** Observe the response without consuming it ahead of the SDK or changing its provider parser. */
async function diagnosticResponse(fetchModel: typeof fetch, input: Parameters<typeof fetch>[0], init?: Parameters<typeof fetch>[1]) {
  const response = await fetchModel(input, init)
  const diagnostics = modelDiagnostics.getStore()
  if (!diagnostics || !response.body || !response.headers.get('content-type')?.includes('text/event-stream')) return response
  diagnostics.wire = true
  const decoder = new TextDecoder()
  let pending = ''
  let data: string[] = []
  const frame = () => {
    if (!data.length) return
    const value = data.join('\n')
    data = []
    diagnostics.events++
    if (value === '[DONE]') { diagnostics.finished = true; return }
    try {
      const raw = JSON.parse(value)
      if (!raw?.type && !Array.isArray(raw?.choices)) diagnostics.unknown = (diagnostics.unknown ?? 0) + 1
      if (raw?.choices?.some((choice: any) => choice.finish_reason) || ['response.completed', 'response.incomplete', 'message_stop'].includes(raw?.type)) diagnostics.finished = true
    } catch { diagnostics.unreadable = (diagnostics.unreadable ?? 0) + 1 }
  }
  const line = (value: string) => {
    if (value === '') frame()
    else if (value.startsWith('data:')) data.push(value.slice(5).replace(/^ /, ''))
  }
  const body = response.body.pipeThrough(new TransformStream<Uint8Array, Uint8Array>({
    transform(chunk, controller) {
      diagnostics.bytes += chunk.byteLength
      pending += decoder.decode(chunk, { stream: true })
      let end: number
      while ((end = pending.indexOf('\n')) >= 0) {
        line(pending.slice(0, end).replace(/\r$/, ''))
        pending = pending.slice(end + 1)
      }
      controller.enqueue(chunk)
    },
    flush() { pending += decoder.decode(); if (pending) line(pending.replace(/\r$/, '')); frame() },
  }))
  return new Response(body, { status: response.status, statusText: response.statusText, headers: response.headers })
}

function cachedTokens(usage: any): number {
  const details = usage?.inputTokensDetails ?? usage?.input_tokens_details
  const list = Array.isArray(details) ? details : details ? [details] : []
  return list.reduce((sum: number, d: Record<string, number>) => sum + Number(d?.cached_tokens ?? d?.cachedTokens ?? d?.cache_read_input_tokens ?? 0), 0)
}

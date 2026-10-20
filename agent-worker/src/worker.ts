import {
  Agent,
  RunState,
  run,
  setTracingDisabled,
  type AgentInputItem,
  type Model,
  type RunContext,
  type RunStreamEvent,
  type RunToolApprovalItem,
  type Tool,
} from '@openai/agents'
import { formatActivity, summarizeUsage, type Elicitation, type FlowActivity, type TurnUsage } from './activities.js'
import type { WorkerConfig } from './config.js'
import { newRunContext, type FlowRunContext } from './context.js'
import { flowMcpServer } from './flow-mcp.js'
import { askUserTool, loadToolsTool, parseAskUser } from './host-tools.js'
import { compactHistory, defaultCompaction } from './history.js'
import { createModel, modelSettings, ObservedModel, type ModelCallRecord, type ModelFactoryOptions } from './models.js'
import { approvalKind, approvalPreview, isApprovalWord, type ApprovalKind } from './policy.js'
import { FileSessionStore } from './session-store.js'
import { QuestionStreamer } from './question-stream.js'
import { initialGroups } from './tool-groups.js'

// Traces would go to api.openai.com with the provider key; the worker exports its own OTel spans instead.
setTracingDisabled(true)

/**
 * Flow tools come from /mcp; the SDK has no approval hook for MCP tools, so the agent sets `needsApproval` on the
 * converted MCP function tools from the ported policy. It runs per call, after the whole model turn is known
 * (ObservedModel records the turn's calls first), and again after a RunState is restored.
 */
class FlowAgent extends Agent<FlowRunContext> {
  override async getMcpTools(runContext: RunContext<FlowRunContext>, tracingParent?: any): Promise<Tool<FlowRunContext>[]> {
    const tools = await super.getMcpTools(runContext, tracingParent)
    for (const tool of tools) {
      if (tool.type !== 'function') continue
      const name = tool.name
      tool.needsApproval = async context => approvalKind(context.context as FlowRunContext, name) !== null
    }
    return tools
  }
}

export type TurnStatus = 'response' | 'awaiting' | 'canceled' | 'error'

export type TurnOutcome = {
  status: TurnStatus
  body: string
  elicitation?: Elicitation
  startedAt: number
  firstActivityAt?: number
  firstTextAt?: number
  endedAt: number
  usage: TurnUsage
  modelCalls: ModelCallRecord[]
  toolCalls: { callId: string; name: string; isError: boolean }[]
}

export type SessionOptions = {
  sessionId: string
  systemPrompt: string
  /** Run token for /mcp (defaults to the worker's configured token). */
  flowToken?: string
  /** Run-token tool allowlist. */
  allowlist?: string[]
  maxTurns?: number
}

export type ActivitySink = (activity: FlowActivity) => void

/**
 * One worker process: shared model client and session store; many concurrent sessions, each an async task
 * (no child process). A session object is cheap and can be dropped between turns: history and suspended runs
 * live in the store, so a later turn can run in any process.
 */
export class FlowAgentWorker {
  readonly store: FileSessionStore
  private readonly baseModel: Model

  constructor(readonly config: WorkerConfig, options: ModelFactoryOptions = {}) {
    this.store = new FileSessionStore(`${config.stateDir}/sessions`)
    this.baseModel = createModel(config, options)
  }

  session(options: SessionOptions): FlowAgentSession {
    return new FlowAgentSession(this, options, this.baseModel)
  }
}

export class FlowAgentSession {
  private readonly model: ObservedModel
  private readonly agent: FlowAgent
  private readonly mcp: ReturnType<typeof flowMcpServer>
  private connected?: Promise<void>
  private controller?: AbortController

  constructor(private readonly worker: FlowAgentWorker, readonly options: SessionOptions, baseModel: Model) {
    const config = worker.config
    this.model = new ObservedModel(baseModel)
    this.mcp = flowMcpServer(config.flowUrl, options.flowToken ?? config.flowToken)
    this.agent = new FlowAgent({
      name: 'Flow Agent',
      instructions: options.systemPrompt,
      model: this.model,
      modelSettings: modelSettings(config, options.sessionId),
      tools: [askUserTool, loadToolsTool],
      mcpServers: [this.mcp],
      mcpConfig: { convertSchemasToStrict: false },
    })
  }

  get sessionId() {
    return this.options.sessionId
  }

  /** A new user prompt. */
  async send(message: string, sink: ActivitySink = () => {}): Promise<TurnOutcome> {
    const history = await this.worker.store.session(this.sessionId).getItems()
    const context = newRunContext(this.sessionId, message, initialGroups(message, previousTools(history)), this.options.allowlist)
    return this.execute(message, context, sink)
  }

  /**
   * Answers the suspended run's elicitation and continues it. `answer` is an option value, "approve"/"reject",
   * or free text (an approval word approves, like agentApprovalAnswer). Works in a different process than the
   * one that suspended the run.
   */
  async resume(answer: string, sink: ActivitySink = () => {}): Promise<TurnOutcome> {
    const suspended = this.worker.store.loadSuspended(this.sessionId)
    if (!suspended) throw new Error(`session ${this.sessionId} has no suspended run`)
    await this.connect() // restoring a RunState re-resolves the agent's tools, MCP ones included
    const state = await RunState.fromString<FlowRunContext, FlowAgent>(this.agent, suspended.state)
    const elicitation = suspended.elicitation as Elicitation | undefined
    // A clicked option arrives as its label or value; the model gets the value.
    const picked = elicitation?.options.find(option => option.value === answer || option.label.toLowerCase() === answer.trim().toLowerCase())
    const context = (state as unknown as { _context: RunContext<FlowRunContext> })._context.context
    const approve = (picked?.value ?? answer) === 'approve' || isApprovalWord(answer)
    for (const item of state.getInterruptions()) {
      if (item.name === 'ask_user') {
        context.answers[item.rawItem.type === 'function_call' ? item.rawItem.callId : ''] = picked?.value ?? answer
        state.approve(item)
      } else if (approve) {
        state.approve(item)
      } else {
        state.reject(item, { message: 'The user rejected this action; nothing was changed. Do not retry it unless the user asks again.' })
      }
    }
    this.worker.store.clearSuspended(this.sessionId)
    return this.execute(state, context, sink)
  }

  /** Stop signal: aborts the model stream and any running tool call. */
  stop() {
    this.controller?.abort()
  }

  private connect() {
    this.connected ??= this.mcp.connect()
    return this.connected
  }

  async close() {
    if (this.connected) await this.mcp.close().catch(() => {})
  }

  private async execute(input: string | RunState<FlowRunContext, FlowAgent>, context: FlowRunContext, sink: ActivitySink, retries = 1, previous?: TurnOutcome): Promise<TurnOutcome> {
    const session = this.worker.store.session(this.sessionId)
    const outcome: TurnOutcome = { status: 'error', body: '', startedAt: previous?.startedAt ?? Date.now(), endedAt: 0, usage: summarizeUsage([]), modelCalls: [...(previous?.modelCalls ?? [])], toolCalls: [] }
    const emit = (activity: FlowActivity) => {
      outcome.firstActivityAt ??= activity.at
      sink(activity)
    }
    // Ack before any network work (AIG: first activity within 10 s; UI shows "Working…"). Not counted as the
    // first model-driven activity in the metrics.
    if (!previous) sink({ type: 'thought', at: Date.now(), body: typeof input === 'string' ? 'Working…' : 'Continuing…', ephemeral: false })
    this.model.observer = record => {
      outcome.modelCalls.push(record)
      if (record.functionCalls.length) context.turns.push(record.functionCalls)
    }
    const controller = new AbortController()
    this.controller = controller
    let text = ''
    // Items this run produced, for history repair after a stop (the SDK does not persist an aborted run's items).
    const produced: AgentInputItem[] = []
    const questions = new QuestionStreamer()
    try {
      await this.connect()
      const result = await run(this.agent, input, {
        stream: true,
        context,
        session,
        signal: controller.signal,
        maxTurns: this.options.maxTurns ?? this.worker.config.maxTurns,
        callModelInputFilter: ({ modelData }) => ({ ...modelData, input: compactHistory(modelData.input, defaultCompaction(this.worker.config.contextWindow)).input }),
      })
      for await (const event of result) {
        if (event.type === 'run_item_stream_event' && ['tool_called', 'tool_output', 'message_output_created'].includes(event.name)) produced.push((event.item as any).rawItem)
        for (const activity of toActivities(event, context, outcome, questions)) {
          if (activity.type === 'text.delta') {
            outcome.firstTextAt ??= activity.at
            text += activity.delta
          }
          emit(activity)
        }
      }
      await result.completed
      if (controller.signal.aborted) return await this.canceled(outcome, emit, produced)
      if (result.error) throw result.error
      const interruptions = result.interruptions ?? []
      if (interruptions.length) {
        const elicitation = toElicitation(interruptions)
        // Long waits must not hold the process: the run is serialized and dropped; resume() can run anywhere.
        this.worker.store.saveSuspended(this.sessionId, result.state.toString(), { elicitation })
        emit({ type: 'elicitation', at: Date.now(), elicitation })
        return this.finish(outcome, 'awaiting', elicitation.body, elicitation)
      }
      const body = typeof result.finalOutput === 'string' ? result.finalOutput : text
      outcome.usage = summarizeUsage(outcome.modelCalls)
      emit({ type: 'response', at: Date.now(), body, usage: outcome.usage })
      return this.finish(outcome, 'response', body)
    } catch (error) {
      if (controller.signal.aborted) return await this.canceled(outcome, emit, produced)
      const wrote = outcome.toolCalls.some(call => !call.isError && isWriteTool(call.name))
      if (typeof input === 'string' && retries > 0 && isTransient(error) && !wrote) {
        // A dropped stream (the SDK never replays one that already emitted events): nothing was written, so the
        // request runs again from the stored history, which holds only the user's message of this attempt.
        const history = await session.getItems()
        const last = history.at(-1) as any
        if (last?.type === 'message' && last.role === 'user') await session.popItem()
        context.turns = []
        sink({ type: 'thought', at: Date.now(), body: 'Connection to the model was lost; retrying…', ephemeral: true })
        return this.execute(input, context, sink, retries - 1, outcome)
      }
      // Keep what already happened in history (a follow-up must not deny writes that ran), then report the error.
      await session.addMissing(produced)
      await session.closeDanglingCalls('The run failed before a result came back; the call may or may not have completed.')
      const body = error instanceof Error ? `${error.name}: ${error.message}` : String(error)
      emit({ type: 'error', at: Date.now(), body, usage: summarizeUsage(outcome.modelCalls) })
      return this.finish(outcome, 'error', body)
    } finally {
      this.model.observer = undefined
      if (this.controller === controller) this.controller = undefined
    }
  }

  private async canceled(outcome: TurnOutcome, emit: ActivitySink, produced: AgentInputItem[]) {
    const session = this.worker.store.session(this.sessionId)
    await session.addMissing(produced)
    const closed = await session.closeDanglingCalls()
    const body = closed ? `Stopped. ${closed} pending tool call(s) were cancelled.` : 'Stopped.'
    emit({ type: 'response', at: Date.now(), body, usage: summarizeUsage(outcome.modelCalls) })
    return this.finish(outcome, 'canceled', body)
  }

  private finish(outcome: TurnOutcome, status: TurnStatus, body: string, elicitation?: Elicitation): TurnOutcome {
    outcome.status = status
    outcome.body = body
    outcome.elicitation = elicitation
    outcome.endedAt = Date.now()
    outcome.usage = summarizeUsage(outcome.modelCalls)
    return outcome
  }
}

/** SDK stream events → Flow activities (§5). */
function toActivities(event: RunStreamEvent, context: FlowRunContext, outcome: TurnOutcome, questions: QuestionStreamer): FlowActivity[] {
  const at = Date.now()
  if (event.type === 'raw_model_stream_event') {
    const data = event.data
    if (data.type === 'output_text_delta') return [{ type: 'text.delta', at, delta: data.delta, ephemeral: true }]
    if (data.type === 'model') {
      const raw = data.event as any
      const question = questions.feed(raw)
      if (question) return [{ type: 'question.delta', at, activityId: question.callId, body: question.text, status: 'running', ephemeral: true }]
      const reasoning =
        raw?.type === 'response.reasoning_summary_text.delta' ? raw.delta
          : raw?.choices?.[0]?.delta?.reasoning_content ?? raw?.choices?.[0]?.delta?.reasoning
            ?? (raw?.type === 'reasoning-delta' ? raw.delta : undefined)
      if (typeof reasoning === 'string' && reasoning) return [{ type: 'thought', at, body: reasoning, ephemeral: true }]
    }
    return []
  }
  if (event.type !== 'run_item_stream_event') return []
  const item = event.item as any
  switch (event.name) {
    case 'tool_called': {
      const raw = item.rawItem
      if (raw?.type !== 'function_call') return []
      outcome.toolCalls.push({ callId: raw.callId, name: raw.name, isError: false })
      return [{ type: 'action', at, activityId: raw.callId, action: raw.name, parameter: safeJson(raw.arguments) }]
    }
    case 'tool_output': {
      const raw = item.rawItem
      const custom = item.customData as { isError?: boolean; change?: unknown } | undefined
      const result = outputText(item.output)
      const isError = Boolean(custom?.isError) || /^(An error occurred while running the tool|Error: )/.test(result)
      const call = outcome.toolCalls.findLast(c => c.callId === raw?.callId)
      if (call) call.isError = isError
      // Held calls approved on resume run without a new tool_called event.
      else outcome.toolCalls.push({ callId: raw?.callId, name: raw?.name, isError })
      return [{ type: 'action.result', at, activityId: raw?.callId, action: raw?.name, result, isError, ...(custom?.change ? { change: custom.change } : {}) }]
    }
    default:
      return []
  }
}

/** The held calls of one interruption become one elicitation: ask_user first, else one approval for the turn. */
function toElicitation(items: RunToolApprovalItem[]): Elicitation {
  const calls = items.map(item => ({ callId: item.rawItem.type === 'function_call' ? item.rawItem.callId : '', name: item.name ?? '', arguments: item.arguments ?? '{}' }))
  const ask = calls.find(call => call.name === 'ask_user')
  if (ask) {
    const request = parseAskUser(ask.arguments)
    return { kind: 'select', callId: ask.callId, body: request.question, options: request.options, allowFreeText: request.allowFreeText }
  }
  // Only connector tools set to always ask are held (see policy.ts).
  const kind: ApprovalKind = 'connector'
  return {
    kind: 'approval',
    approval: kind,
    callIds: calls.map(call => call.callId),
    body: approvalPreview(kind, calls),
    options: [{ label: 'Approve', value: 'approve' }, { label: 'Reject', value: 'reject' }],
  }
}

const writePrefixes = ['save_', 'delete_', 'create_', 'triage_', 'update_', 'merge_', 'submit_', 'resolve_', 'restore_', 'external_']
function isWriteTool(name: string) {
  return writePrefixes.some(prefix => name.startsWith(prefix))
}

/** Network-level failures worth one transparent retry (the provider never finished the response). */
function isTransient(error: unknown) {
  const text = `${(error as Error)?.name ?? ''} ${(error as Error)?.message ?? error} ${String((error as any)?.cause ?? '')}`
  return /terminated|ECONNRESET|ECONNREFUSED|ETIMEDOUT|socket hang up|fetch failed|other side closed|premature close|ModelTimeoutError|timed out/i.test(text)
}

/** MCP results arrive as a content item ({type:'text', text}) or a list of them. */
function outputText(output: unknown): string {
  if (typeof output === 'string') return output
  const items = Array.isArray(output) ? output : [output]
  if (items.every(item => (item as any)?.type === 'text')) return items.map(item => (item as any).text).join('\n')
  return JSON.stringify(output)
}

function previousTools(history: AgentInputItem[]): string[] {
  return history.flatMap(item => (item.type === 'function_call' ? [item.name] : []))
}

function safeJson(value: unknown) {
  if (typeof value !== 'string') return value
  try {
    return JSON.parse(value)
  } catch {
    return value
  }
}

export { formatActivity }

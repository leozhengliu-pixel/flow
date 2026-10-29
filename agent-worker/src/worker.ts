import {
  Agent,
  MaxTurnsExceededError,
  RunState,
  run,
  setTracingDisabled,
  type AgentInputItem,
  type MCPServer,
  type Model,
  type RunContext,
  type RunStreamEvent,
  type RunToolApprovalItem,
  type Tool,
} from '@openai/agents'
import { formatActivity, summarizeUsage, type Elicitation, type FlowActivity, type TurnUsage } from './activities.js'
import type { WorkerConfig } from './config.js'
import { newRunContext, type FlowRunContext } from './context.js'
import { ConnectorElicitationWatcher, type ElicitationApi } from './connector-elicitation.js'
import { flowMcpServer } from './flow-mcp.js'
import { askUserTool, loadToolsTool, parseAskUser } from './host-tools.js'
import { compactHistory, defaultCompaction } from './history.js'
import { createModel, modelSettings, ObservedModel, type ModelCallRecord, type ModelFactoryOptions } from './models.js'
import { approvalKind, approvalPreview, isApprovalWord, type ApprovalKind } from './policy.js'
import { FileSessionStore, type RepairableSession, type SuspendedStore } from './session-store.js'
import { QuestionStreamer } from './question-stream.js'
import { outputText, RunGuard, ToolErrorFlags, type GuardApi } from './run-guard.js'
import { initialGroups } from './tool-groups.js'
import { hostedSearchQuery, hostedSearchRefused, hostedWebSearchTool } from './hosted-search.js'
import type { HeldCall } from './runtime-client.js'

// Traces would go to api.openai.com with the provider key; the worker exports its own OTel spans instead.
setTracingDisabled(true)

/**
 * Flow tools come from /mcp; the SDK has no approval hook for MCP tools, so the agent sets `needsApproval` on the
 * converted MCP function tools from the ported policy. It runs per call, after the whole model turn is known
 * (ObservedModel records the turn's calls first), and again after a RunState is restored.
 */
class FlowAgent extends Agent<FlowRunContext> {
  /** The delegated-run guard of the run in progress (run-guard.ts), if the run has one. */
  guard?: RunGuard

  override async getAllTools(runContext: RunContext<FlowRunContext>, tracingParent?: any): Promise<Tool<FlowRunContext>[]> {
    const tools = await super.getAllTools(runContext, tracingParent)
    return this.guard ? ((await this.guard.tools(tools)) as Tool<FlowRunContext>[]) : tools
  }

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
  heldCalls?: HeldCall[]
  sessionId: string
  systemPrompt: string
  /** Run token for /mcp (defaults to the worker's configured token). */
  flowToken?: string
  /** Run-token tool allowlist. */
  allowlist?: string[]
  maxTurns?: number
  /**
   * Flow's run guard for this session's run (POST /internal/agent-runtime/runs/{id}/guard); a thread run gets the
   * in-process agent's preparation budgets and Coding transfer through it (run-guard.ts).
   */
  guard?: GuardApi
  /** Tools served by the worker besides ask_user and load_tools. */
  tools?: Tool<FlowRunContext>[]
  /** MCP servers (default: Flow's /mcp with the run token). */
  mcpServers?: MCPServer[]
  /** The structured isError flags of connector calls, read by the run guard (default: one per session). */
  toolErrors?: ToolErrorFlags
  /**
   * The run's connector questions (GET /internal/agent-runtime/runs/{id}/elicitations): watched while a connector
   * call is in flight and sent to the sink as `connector.elicitation` activities (a chat run's person answers them
   * on Flow; other runs have none).
   */
  elicitations?: ElicitationApi
  /**
   * The session's history for this run (default: the worker's session store). A chat reply's conversation comes
   * from Flow with the run (MemorySession seeded from the payload), so any worker can run any reply.
   */
  history?: RepairableSession
  /** Where a suspended run is kept (default: the worker's session store); a chat run keeps it in Flow. */
  suspended?: SuspendedStore
  /** Tool groups the run starts with besides the ones its request picks (Flow's, from the session's surface). */
  groups?: string[]
  /** Offer every tool Flow lists, run the calls one at a time, and end the run after these tools (a loop run). */
  allTools?: boolean
  serialCalls?: boolean
  stopAtTools?: string[]
  /** Search the web with the provider's hosted search (hosted-search.ts), when the protocol has one. */
  hostedWebSearch?: boolean
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

  constructor(readonly config: WorkerConfig, options: ModelFactoryOptions & { model?: Model } = {}) {
    this.store = new FileSessionStore(`${config.stateDir}/sessions`)
    this.baseModel = options.model ?? createModel(config, options)
  }

  session(options: SessionOptions): FlowAgentSession {
    return new FlowAgentSession(this, options, this.baseModel)
  }
}

export class FlowAgentSession {
  private readonly model: ObservedModel
  private readonly agent: FlowAgent
  private readonly mcp: MCPServer[]
  private readonly toolErrors: ToolErrorFlags
  private readonly elicitations?: ConnectorElicitationWatcher
  private connected?: Promise<void>
  private controller?: AbortController
  /** The sink of the turn in progress (connector questions arrive while a tool call runs). */
  private sink?: ActivitySink
  /** The provider's hosted search tool the agent offers, until the provider refuses it. */
  private hostedSearch?: Tool<FlowRunContext>
  private heldCallsDone = false

  constructor(private readonly worker: FlowAgentWorker, readonly options: SessionOptions, baseModel: Model) {
    const config = worker.config
    this.model = new ObservedModel(baseModel)
    this.toolErrors = options.toolErrors ?? new ToolErrorFlags()
    if (options.elicitations) {
      this.elicitations = new ConnectorElicitationWatcher(options.elicitations, elicitation => this.sink?.({ type: 'connector.elicitation', at: Date.now(), elicitation }))
    }
    const watcher = this.elicitations
    this.mcp = options.mcpServers ?? [flowMcpServer(config.flowUrl, options.flowToken ?? config.flowToken, { toolErrors: this.toolErrors, onConnectorCall: watcher ? () => watcher.begin() : undefined, serial: options.serialCalls })]
    if (options.hostedWebSearch) this.hostedSearch = hostedWebSearchTool(config.protocol)
    this.agent = new FlowAgent({
      name: 'Flow Agent',
      instructions: options.systemPrompt,
      model: this.model,
      modelSettings: modelSettings(config, options.sessionId),
      // A loop run has only the tools Flow lists for it (no question to ask, no groups to load).
      tools: this.hostTools(),
      mcpServers: this.mcp,
      mcpConfig: { convertSchemasToStrict: false },
      ...(options.stopAtTools?.length ? { toolUseBehavior: { stopAtToolNames: options.stopAtTools } } : {}),
    })
  }

  get sessionId() {
    return this.options.sessionId
  }

  /** The tools the agent offers besides /mcp's, the hosted search included while the provider takes it. */
  private hostTools(): Tool<FlowRunContext>[] {
    return [...(this.options.allTools ? [] : [askUserTool, loadToolsTool]), ...(this.options.tools ?? []), ...(this.hostedSearch ? [this.hostedSearch] : [])]
  }

  private history(): RepairableSession {
    return this.options.history ?? this.worker.store.session(this.sessionId)
  }

  private suspendedStore(): SuspendedStore {
    return this.options.suspended ?? this.worker.store.suspendedFor(this.sessionId)
  }

  /**
   * A new user prompt. `input` is what runs when the prompt is more than its text (a message with images); the text
   * still picks the request's tool groups.
   */
  async send(message: string, sink: ActivitySink = () => {}, input?: AgentInputItem[]): Promise<TurnOutcome> {
    const history = await this.history().getItems()
    const context = newRunContext(this.sessionId, message, [...new Set([...initialGroups(message, [...previousTools(history), ...(this.options.heldCalls ?? []).map(call => call.name)]), ...(this.options.groups ?? [])])], this.options.allowlist)
    if (this.options.allTools) context.allTools = true
    return this.execute(input ?? message, context, sink)
  }

  private async replayHeldCalls(outcome: TurnOutcome, sink: ActivitySink, signal: AbortSignal) {
    if (!this.heldCallsDone && this.options.heldCalls?.length) {
      this.heldCallsDone = true
      for (const call of this.options.heldCalls) {
        signal.throwIfAborted()
        let text = 'The user rejected this action; nothing was changed. Do not retry it unless the user asks again.'
        let isError = !call.approved
        let change: unknown
        if (call.approved) {
          const args = JSON.parse(call.arguments || '{}')
          sink({ type: 'action', at: Date.now(), activityId: call.id, action: call.name, parameter: args })
          outcome.toolCalls.push({ callId: call.id, name: call.name, isError: false })
          try {
            const server = this.mcp[0]
            const result = server.callToolResult ? await server.callToolResult(call.name, args, { 'flow/approved': true }, { signal }) : { content: await server.callTool(call.name, args, { 'flow/approved': true }, { signal }) }
            text = result.content.flatMap(part => part.type === 'text' ? [part.text] : []).join('\n')
            isError = Boolean(result.isError)
            change = (result.structuredContent as Record<string, unknown> | undefined)?.change
          } catch (error) { text = String(error); isError = true }
          outcome.toolCalls[outcome.toolCalls.length - 1]!.isError = isError
          sink({ type: 'action.result', at: Date.now(), activityId: call.id, action: call.name, result: text, isError, ...(change ? { change } : {}) })
        }
        await this.history().addItems([
          { type: 'function_call', callId: call.id, name: call.name, arguments: call.arguments || '{}', status: 'completed' } as AgentInputItem,
          { type: 'function_call_result', callId: call.id, name: call.name, output: { type: 'text', text }, status: 'completed' } as AgentInputItem,
        ])
      }
    }
  }

  /**
   * Answers the suspended run's elicitation and continues it. `answer` is an option value, "approve"/"reject",
   * or free text (an approval word approves, like agentApprovalAnswer). Works in a different process than the
   * one that suspended the run.
   */
  async resume(answer: string, sink: ActivitySink = () => {}): Promise<TurnOutcome> {
    return this.resumeSuspended(sink, (item, elicitation, context) => {
      // A clicked option arrives as its label or value; the model gets the value.
      const picked = elicitation?.options.find(option => option.value === answer || option.label.toLowerCase() === answer.trim().toLowerCase())
      if (item.name === 'ask_user') {
        context.answers[item.rawItem.type === 'function_call' ? item.rawItem.callId : ''] = picked?.value ?? answer
        return true
      }
      return (picked?.value ?? answer) === 'approve' || isApprovalWord(answer)
    })
  }

  /**
   * Continues a suspended run whose held calls the person answered one by one (a chat reply's approvals on Flow):
   * `decisions` maps each call id to approved or rejected; a call without a decision is rejected.
   */
  async resumeWith(decisions: Record<string, string>, sink: ActivitySink = () => {}): Promise<TurnOutcome> {
    return this.resumeSuspended(sink, item => decisions[item.rawItem.type === 'function_call' ? item.rawItem.callId : ''] === 'approved')
  }

  private async resumeSuspended(sink: ActivitySink, approves: (item: RunToolApprovalItem, elicitation: Elicitation | undefined, context: FlowRunContext) => boolean): Promise<TurnOutcome> {
    const store = this.suspendedStore()
    const suspended = await store.load()
    if (!suspended) throw new Error(`session ${this.sessionId} has no suspended run`)
    await this.connect() // restoring a RunState re-resolves the agent's tools, MCP ones included
    const state = await RunState.fromString<FlowRunContext, FlowAgent>(this.agent, suspended.state)
    const elicitation = suspended.elicitation as Elicitation | undefined
    const context = (state as unknown as { _context: RunContext<FlowRunContext> })._context.context
    for (const item of state.getInterruptions()) {
      if (approves(item, elicitation, context)) state.approve(item)
      else state.reject(item, { message: 'The user rejected this action; nothing was changed. Do not retry it unless the user asks again.' })
    }
    await store.clear()
    return this.execute(state, context, sink)
  }

  /** Stop signal: aborts the model stream and any running tool call. */
  stop() {
    this.controller?.abort()
  }

  private connect() {
    this.connected ??= Promise.all(this.mcp.map(server => server.connect())).then(() => undefined)
    return this.connected
  }

  async close() {
    if (this.connected) await Promise.all(this.mcp.map(server => server.close().catch(() => {})))
  }

  private async execute(input: string | AgentInputItem[] | RunState<FlowRunContext, FlowAgent>, context: FlowRunContext, sink: ActivitySink, retries = 1, previous?: TurnOutcome, previousGuard?: RunGuard): Promise<TurnOutcome> {
    const session = this.history()
    const maxTurns = this.options.maxTurns ?? this.worker.config.maxTurns
    const outcome: TurnOutcome = { status: 'error', body: '', startedAt: previous?.startedAt ?? Date.now(), endedAt: 0, usage: summarizeUsage([]), modelCalls: [...(previous?.modelCalls ?? [])], toolCalls: [...(previous?.toolCalls ?? [])] }
    const emit = (activity: FlowActivity) => {
      outcome.firstActivityAt ??= activity.at
      sink(activity)
    }
    this.sink = emit
    // Ack before any network work (AIG: first activity within 10 s; UI shows "Working…"). Not counted as the
    // first model-driven activity in the metrics.
    if (!previous) sink({ type: 'thought', at: Date.now(), body: input instanceof RunState ? 'Continuing…' : 'Working…', ephemeral: false })
    let guard = previousGuard
    if (!guard && this.options.guard) {
      guard = new RunGuard(this.options.guard, maxTurns, this.toolErrors)
      // A loop's completion check may send the reply back more than once (Flow bounds it).
      guard.repeatNudges = Boolean(this.options.allTools)
      await guard.start(context)
    }
    this.model.observer = record => {
      outcome.modelCalls.push(record)
      if (record.functionCalls.length) context.turns.push(record.functionCalls)
      if (guard) guard.round++
    }
    this.model.rewrite = guard?.enabled ? <T extends { type?: string }>(items: T[]) => guard.output(items) : undefined
    this.agent.guard = guard?.enabled ? guard : undefined
    if (this.options.allTools && this.options.guard) {
      const call = this.options.guard
      this.model.beforeModel = async request => {
        const answer = await call({ op: 'input', contextBytes: Buffer.byteLength(JSON.stringify(request.input)) + Buffer.byteLength(this.options.systemPrompt) })
        if (answer.error) throw new Error(answer.error)
      }
      this.model.afterModel = async (output, usage, diagnostics) => {
        const text = output.flatMap(item => item.type === 'message' && Array.isArray(item.content) ? item.content.flatMap(part => part.type === 'output_text' ? [part.text] : []) : []).join('')
        const calls = output.flatMap(item => item.type === 'function_call' ? [{ id: item.callId, name: item.name, arguments: item.arguments }] : [])
        const answer = await call({ op: 'model', round: guard?.round ?? 0, modelTurn: {
          text, reasoning: diagnostics.reasoning, calls, stopReason: diagnostics.stopReason, stopDetail: diagnostics.stopDetail,
          usage: { inputTokens: usage.inputTokens ?? 0, outputTokens: usage.outputTokens ?? 0, cachedTokens: usage.inputTokensDetails?.cached_tokens ?? 0, reasoningTokens: usage.outputTokensDetails?.reasoning_tokens ?? 0 },
          durationMs: Date.now()-diagnostics.startedAt, events: diagnostics.events, bytes: diagnostics.bytes, finished: diagnostics.finished,
          unreadable: diagnostics.unreadable, unknown: diagnostics.unknown,
          model: this.worker.config.model, protocol: this.worker.config.protocol,
        } })
        if (answer.error) throw new Error(answer.error)
        return answer.handled ? [{ type: 'message', role: 'assistant', status: 'completed', content: [{ type: 'output_text', text: '' }] }] : output
      }
      this.model.modelError = async diagnostics => {
        await call({ op: 'model', round: guard?.round ?? 0, modelTurn: {
          text: '', reasoning: diagnostics.reasoning, calls: [], stopReason: diagnostics.stopReason, stopDetail: diagnostics.stopDetail,
          usage: { inputTokens: 0, outputTokens: 0, cachedTokens: 0, reasoningTokens: 0 },
          durationMs: Date.now()-diagnostics.startedAt, events: diagnostics.events, bytes: diagnostics.bytes, finished: false,
          unreadable: diagnostics.unreadable, unknown: diagnostics.unknown,
          model: this.worker.config.model, protocol: this.worker.config.protocol, error: 'provider_error',
        } })
      }
    }
    const controller = new AbortController()
    this.controller = controller
    let text = ''
    // Items this run produced, for history repair after a stop (the SDK does not persist an aborted run's items).
    const produced: AgentInputItem[] = []
    const questions = new QuestionStreamer()
    try {
      await this.connect()
      await this.replayHeldCalls(outcome, emit, controller.signal)
      controller.signal.throwIfAborted()
      const result = await run(this.agent, input, {
        stream: true,
        context,
        session,
        signal: controller.signal,
        maxTurns,
        callModelInputFilter: ({ modelData }) => {
          const input = this.options.allTools ? modelData.input : compactHistory(modelData.input, defaultCompaction(this.worker.config.contextWindow)).input
          return { ...modelData, input: guard?.enabled ? guard.input(input) : input }
        },
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
        await this.suspendedStore().save(result.state.toString(), { elicitation })
        emit({ type: 'elicitation', at: Date.now(), elicitation })
        return this.finish(outcome, 'awaiting', elicitation.body, elicitation)
      }
      let body = typeof result.finalOutput === 'string' ? result.finalOutput : text
      if (guard?.enabled) {
        // The run's last word goes through Flow: Coding when the run must enter it, a "done" claim replaced by the
        // session's real state.
        const last = await guard.finish(guard.decisionCut ? 'decision' : 'reply', guard.decisionCut ? '' : body)
        if (last.nudge) {
          // The reply reported change outcomes (failures, permission errors, successes) no write of the run produced:
          // the model is told so once, as the person's next message, and its new answer is the run's last word.
          return await this.execute(last.nudge, context, sink, retries, outcome, guard)
        }
        if (last.error) {
          // Flow ends the run with its own error (a reply that stayed empty, or kept asking for approval in text).
          emit({ type: 'error', at: Date.now(), body: last.error, usage: summarizeUsage(outcome.modelCalls), flow: true })
          return this.finish(outcome, 'error', last.error)
        }
        body = last.reply
      }
      outcome.usage = summarizeUsage(outcome.modelCalls)
      emit({ type: 'response', at: Date.now(), body, usage: outcome.usage })
      return this.finish(outcome, 'response', body)
    } catch (error) {
      if (controller.signal.aborted) return await this.canceled(outcome, emit, produced)
      const wrote = outcome.toolCalls.some(call => !call.isError && isWriteTool(call.name))
      if (this.hostedSearch && !wrote && !(input instanceof RunState) && hostedSearchRefused(error)) {
        // As in-process: a provider that refuses the hosted search tool is asked again without it.
        this.hostedSearch = undefined
        this.agent.tools = this.hostTools()
        const history = await session.getItems()
        const last = history.at(-1) as any
        if (last?.type === 'message' && last.role === 'user') await session.popItem()
        context.turns = []
        if (guard) guard.round = 0
        return this.execute(input, context, sink, retries, outcome, guard)
      }
      if (!(input instanceof RunState) && retries > 0 && isTransient(error) && !wrote) {
        // A dropped stream (the SDK never replays one that already emitted events): nothing was written, so the
        // request runs again from the stored history, which holds only the user's message of this attempt.
        const history = await session.getItems()
        const last = history.at(-1) as any
        if (last?.type === 'message' && last.role === 'user') await session.popItem()
        context.turns = []
        sink({ type: 'thought', at: Date.now(), body: 'Connection to the model was lost; retrying…', ephemeral: true })
        if (guard) guard.round = 0
        return this.execute(input, context, sink, retries - 1, outcome, guard)
      }
      // Keep what already happened in history (a follow-up must not deny writes that ran), then report the error.
      await session.addMissing(produced)
      await session.closeDanglingCalls('The run failed before a result came back; the call may or may not have completed.')
      if (guard?.enabled && error instanceof MaxTurnsExceededError) {
        // At the turn limit a run that must enter Coding is moved into it, and one that started Coding reports the
        // session's state rather than a failed run.
        const last = await guard.finish('turnLimit', '')
        if (last.reply) {
          emit({ type: 'response', at: Date.now(), body: last.reply, usage: summarizeUsage(outcome.modelCalls) })
          return this.finish(outcome, 'response', last.reply)
        }
        if (last.error) {
          emit({ type: 'error', at: Date.now(), body: last.error, usage: summarizeUsage(outcome.modelCalls), flow: true })
          return this.finish(outcome, 'error', last.error)
        }
      }
      const body = error instanceof Error ? `${error.name}: ${error.message}` : String(error)
      emit({ type: 'error', at: Date.now(), body, usage: summarizeUsage(outcome.modelCalls) })
      return this.finish(outcome, 'error', body)
    } finally {
      this.model.observer = undefined
      this.model.rewrite = undefined
      this.model.beforeModel = undefined
      this.model.afterModel = undefined
      this.model.modelError = undefined
      this.agent.guard = undefined
      if (this.controller === controller) this.controller = undefined
      if (this.sink === emit) this.sink = undefined
    }
  }

  private async canceled(outcome: TurnOutcome, emit: ActivitySink, produced: AgentInputItem[]) {
    const session = this.history()
    await session.addMissing(produced)
    const closed = await session.closeDanglingCalls()
    const body = closed ? `Stopped. ${closed} pending tool call(s) were cancelled.` : 'Stopped.'
    emit({ type: 'response', at: Date.now(), body, usage: summarizeUsage(outcome.modelCalls), canceled: true })
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
      if (raw?.type === 'hosted_tool_call') {
        // The provider's hosted search runs on its side: a step of the work, as in-process (never a call to run).
        const callId = `hosted_${raw.id ?? raw.callId ?? Math.random().toString(36).slice(2)}`
        return [
          { type: 'action', at, activityId: callId, action: 'web_search', parameter: { query: hostedSearchQuery(raw) } },
          { type: 'action.result', at, activityId: callId, action: 'web_search', result: '', isError: raw.status === 'failed' },
        ]
      }
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
    return { kind: 'select', callId: ask.callId, arguments: ask.arguments, body: request.question, options: request.options, allowFreeText: request.allowFreeText }
  }
  // Only connector tools set to always ask are held (see policy.ts).
  const kind: ApprovalKind = 'connector'
  return {
    kind: 'approval',
    approval: kind,
    callIds: calls.map(call => call.callId),
    calls,
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

import {
  defineToolInputGuardrail,
  defineToolOutputGuardrail,
  ToolGuardrailFunctionOutputFactory,
  type AgentInputItem,
  type Tool,
} from '@openai/agents'

/**
 * The delegated-run guard of a thread run (a delegated issue, a mention), as the in-process agent applies it
 * (api/cmd/server/builtin_run_guard.go, builtin_coding_transfer.go). Flow keeps the rules and the texts: the worker
 * asks POST /internal/agent-runtime/runs/{id}/guard at the points the in-process loop decides and enforces the
 * answer, so both runtimes share one implementation (api/cmd/server/agent_runtime_guard.go):
 *
 *  - before each model turn (`turn`): connector tools are withdrawn once the preparation budget is spent or only
 *    the last two turns of the worker's own cap are left; the first time, a decision nudge is appended to the last
 *    tool result the model reads;
 *  - around each connector call (`before` / `after`, as tool guardrails): an identical repeat is answered from the
 *    earlier result with the nudge, a call over budget is refused, a failed or empty result counts as no progress;
 *  - a start_coding_session result (`coding`) is annotated with the delivery state;
 *  - the run's last word (`finish`): a reply without Coding, a forced-decision turn that kept searching (its calls
 *    are not run), or the turn limit — Flow starts Coding through the same tool path, or replaces a "done" claim
 *    with the session's real state; a reply that reports change outcomes the run's write calls did not produce is
 *    sent back once (`nudge`), then replaced by Flow's statement, and Flow's count of the run's writes follows a
 *    reply they did not all succeed for.
 *
 * Shared test vectors: api/cmd/server/testdata/worker_run_guard/*.json (Go replays the requests against Flow, the
 * worker test drives this loop and checks it sends exactly them).
 */
export type GuardRequest =
  | { op: 'start' }
  | { op: 'turn'; round: number; maxTurns: number; connectors: boolean }
  | { op: 'before'; tool: string; arguments: string }
  | { op: 'after'; tool: string; arguments: string; content: string; failed: boolean }
  | { op: 'coding'; content: string; failed: boolean }
  | { op: 'finish'; phase: FinishPhase; text: string }

export type FinishPhase = 'reply' | 'decision' | 'turnLimit'

export type GuardResponse = {
  enabled: boolean
  codingDue: boolean
  decisionRound: number
  mustDecide?: boolean
  nudge?: string
  /** A chat reply whose tool budget is spent: the turn is offered no tools (its last word). */
  withhold?: boolean
  handled?: boolean
  content?: string
  failed?: boolean
  reply?: string
  transferred?: boolean
  error?: string
}

export type GuardApi = (request: GuardRequest) => Promise<GuardResponse>

export const startCodingSessionTool = 'start_coding_session'

/** Connector (MCP) tools are offered as external_<hash>; only they count as read-only preparation. */
export function isConnectorTool(name: string) {
  return name.startsWith('external_')
}

/** A tool result that reports a failure (the SDK's and Flow's error shapes). */
export function isErrorOutput(text: string) {
  if (/^(An error occurred while running the tool|Error: )/.test(text)) return true
  try {
    const parsed = JSON.parse(text)
    return parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed) && typeof parsed.error === 'string' && Object.keys(parsed).length === 1
  } catch {
    return false
  }
}

/**
 * The structured `isError` flag of the run's tool results. The SDK hands tool guardrails only the result content,
 * so the MCP client records each call's flag here (flow-mcp.ts) and the guard's `after` reads it back for the same
 * tool and arguments: an `isError` result with plain text counts as failed.
 */
export class ToolErrorFlags {
  private readonly flags = new Map<string, boolean[]>()

  record(tool: string, args: unknown, isError: boolean) {
    const key = flagKey(tool, args)
    const list = this.flags.get(key) ?? []
    list.push(isError)
    this.flags.set(key, list.slice(-8))
  }

  /** The flag of the earliest unread call of tool with these arguments (a JSON string or an object). */
  take(tool: string, args: unknown): boolean | undefined {
    const key = flagKey(tool, args)
    const list = this.flags.get(key)
    const flag = list?.shift()
    if (list && list.length === 0) this.flags.delete(key)
    return flag
  }
}

function flagKey(tool: string, args: unknown) {
  let value = args
  if (typeof value === 'string') {
    try {
      value = JSON.parse(value || '{}')
    } catch {
      return `${tool}\0${value}`
    }
  }
  return `${tool}\0${stableJson(value ?? {})}`
}

function stableJson(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableJson).join(',')}]`
  if (value && typeof value === 'object') {
    return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${stableJson((value as Record<string, unknown>)[key])}`).join(',')}}`
  }
  return JSON.stringify(value)
}

/** MCP results arrive as a content item ({type:'text', text}) or a list of them. */
export function outputText(output: unknown): string {
  if (typeof output === 'string') return output
  const items = Array.isArray(output) ? output : [output]
  if (items.every(item => (item as any)?.type === 'text')) return items.map(item => (item as any).text).join('\n')
  return JSON.stringify(output)
}

/** The guard of the run whose context the SDK hands a guardrail. */
const guards = new WeakMap<object, RunGuard>()

const beforeGuardrail = defineToolInputGuardrail({
  name: 'flow_run_guard_before',
  run: async ({ context, toolCall }) => {
    const guard = guards.get(context.context as object)
    const answer = guard?.enabled ? await guard.call({ op: 'before', tool: toolCall.name, arguments: toolCall.arguments ?? '' }).catch(() => undefined) : undefined
    return answer?.handled ? ToolGuardrailFunctionOutputFactory.rejectContent(answer.content ?? '') : ToolGuardrailFunctionOutputFactory.allow()
  },
})

const afterGuardrail = defineToolOutputGuardrail({
  name: 'flow_run_guard_after',
  run: async ({ context, toolCall, output }) => {
    const guard = guards.get(context.context as object)
    if (guard?.enabled) {
      const content = outputText(output)
      // The structured isError flag first; error text (the SDK's and Flow's error shapes) otherwise.
      const flagged = guard.toolErrors?.take(toolCall.name, toolCall.arguments ?? '')
      const failed = flagged === true || isErrorOutput(content)
      await guard.call({ op: 'after', tool: toolCall.name, arguments: toolCall.arguments ?? '', content, failed }).catch(() => undefined)
    }
    return ToolGuardrailFunctionOutputFactory.allow()
  },
})

const codingGuardrail = defineToolOutputGuardrail({
  name: 'flow_run_guard_coding',
  run: async ({ context, output }) => {
    const guard = guards.get(context.context as object)
    if (!guard?.enabled) return ToolGuardrailFunctionOutputFactory.allow()
    const content = outputText(output)
    const answer = await guard.call({ op: 'coding', content, failed: isErrorOutput(content) }).catch(() => undefined)
    // The result the model reads carries the delivery state and how to report it.
    return answer?.content && answer.content !== content ? ToolGuardrailFunctionOutputFactory.rejectContent(answer.content) : ToolGuardrailFunctionOutputFactory.allow()
  },
})

function attach<T extends { name: string }>(list: T[] | undefined, guardrail: T): T[] {
  const items = list ?? []
  if (!items.some(item => item.name === guardrail.name)) items.push(guardrail)
  return items
}

export class RunGuard {
  /** Model calls made so far in this run (the next turn's 0-based index). */
  round = 0
  /** Set when the forced-decision turn's calls were dropped: the run ends with finish('decision'). */
  decisionCut = false
  /** Set once Flow sent the model back with the outcome nudge (it is sent at most once per run). */
  outcomeNudged = false
  private view: GuardResponse = { enabled: false, codingDue: false, decisionRound: -1 }
  private queue: Promise<unknown> = Promise.resolve()
  private turnAsked = -1
  private withdraw = false
  private withhold = false
  private nudge?: string

  constructor(private readonly api: GuardApi, readonly maxTurns: number, readonly toolErrors?: ToolErrorFlags) {}

  get enabled() {
    return this.view.enabled
  }

  /** Binds the guard to a run's context and reads the run's coding context. A failing call leaves it off. */
  async start(context: object) {
    guards.set(context, this)
    await this.call({ op: 'start' }).catch(() => undefined)
  }

  /** One guard operation; operations of a run are sent one at a time (parallel tool calls of a turn). */
  call(request: GuardRequest): Promise<GuardResponse> {
    const next = this.queue.then(() => this.api(request)).then(answer => {
      if (request.op === 'start' || this.view.enabled) this.view = { enabled: answer.enabled, codingDue: answer.codingDue, decisionRound: answer.decisionRound }
      return answer
    })
    this.queue = next.catch(() => undefined)
    return next
  }

  /** The tools of the next model turn: connector tools withdrawn when Flow says so, guardrails attached. */
  async tools(tools: Tool<any>[]): Promise<Tool<any>[]> {
    if (!this.enabled) return tools
    if (this.turnAsked !== this.round) {
      this.turnAsked = this.round
      const connectors = tools.some(tool => tool.type === 'function' && isConnectorTool(tool.name))
      const answer = await this.call({ op: 'turn', round: this.round, maxTurns: this.maxTurns, connectors }).catch(() => undefined)
      this.withdraw = Boolean(answer?.mustDecide)
      this.withhold = Boolean(answer?.withhold)
      if (answer?.nudge) this.nudge = answer.nudge
    }
    const offered: Tool<any>[] = []
    for (const tool of tools) {
      if (this.withhold && tool.type === 'function') continue
      if (tool.type !== 'function') {
        offered.push(tool)
        continue
      }
      if (isConnectorTool(tool.name)) {
        if (this.withdraw) continue
        tool.inputGuardrails = attach(tool.inputGuardrails, beforeGuardrail)
        tool.outputGuardrails = attach(tool.outputGuardrails, afterGuardrail)
      } else if (tool.name === startCodingSessionTool) {
        tool.outputGuardrails = attach(tool.outputGuardrails, codingGuardrail)
      }
      offered.push(tool)
    }
    return offered
  }

  /** The decision nudge goes on the last tool result the model reads before its first turn without connectors. */
  input(items: AgentInputItem[]): AgentInputItem[] {
    const nudge = this.nudge
    this.nudge = undefined
    const last = items.at(-1) as any
    if (!nudge || last?.type !== 'function_call_result') return items
    const output = last.output
    let next: unknown
    if (typeof output === 'string') next = output + nudge
    else if (output?.type === 'text') next = { ...output, text: output.text + nudge }
    else if (Array.isArray(output)) next = [...output, { type: 'input_text', text: nudge }]
    else return items
    return [...items.slice(0, -1), { ...last, output: next } as AgentInputItem]
  }

  /**
   * A model turn's output. On the forced-decision turn of a run that must enter Coding, calls other than
   * start_coding_session or ask_user are not run: the turn ends the run and Flow starts Coding instead.
   */
  output<T extends { type?: string }>(items: T[]): T[] {
    const round = this.round
    const calls = items.filter(item => item.type === 'function_call') as unknown as { name: string }[]
    if (!this.enabled || round !== this.view.decisionRound || !this.view.codingDue || calls.length === 0) return items
    if (calls.some(call => call.name === startCodingSessionTool || call.name === 'ask_user')) return items
    this.decisionCut = true
    const kept = items.filter(item => item.type !== 'function_call')
    if (!kept.some(item => item.type === 'message')) kept.push({ type: 'message', role: 'assistant', status: 'completed', content: [{ type: 'output_text', text: '' }] } as unknown as T)
    return kept
  }

  /**
   * The run's last word: the reply to post (Flow's when it answers one), or the error the run ends with. When the
   * reply reports change outcomes no write of the run produced (Flow keeps the run's write calls from /mcp,
   * api/cmd/server/agent_run_outcomes.go), Flow answers once with `nudge` instead: the loop sends it to the model
   * and asks again with the new answer.
   */
  async finish(phase: FinishPhase, text: string): Promise<{ reply: string; error?: string; transferred: boolean; nudge?: string }> {
    if (!this.enabled) return phase === 'turnLimit' ? { reply: '', error: undefined, transferred: false } : { reply: text, transferred: false }
    const answer = await this.call({ op: 'finish', phase, text }).catch(() => undefined)
    if (!answer) return { reply: text, transferred: false }
    if (answer.error) return { reply: '', error: answer.error, transferred: false }
    if (phase === 'reply' && answer.nudge && !answer.reply && !this.outcomeNudged) {
      this.outcomeNudged = true
      return { reply: '', transferred: false, nudge: answer.nudge }
    }
    return { reply: answer.reply || text, transferred: Boolean(answer.transferred) }
  }
}

/**
 * Chat replies on the worker (api/cmd/server/agent_worker_chat.go, design doc §6, §7.1). Flow renders the reply's
 * prompt when the message is sent and serves it with the run: the system prompt and the conversation so far, the
 * latest replies with their tool calls and results, ending with the person's message. The worker runs it from that
 * conversation (no history of its own), and posts what happens to Flow, which writes the chat reply the person
 * sees (the same streaming message the in-process agent saves):
 *
 *  - text deltas, coalesced (every `intervalMs`), and ask_user's question while it is written;
 *  - each tool call and its result (with the change-set entry /mcp recorded), in order;
 *  - the elicitation a run suspends on: ask_user's question ends the reply, held connector calls wait for the
 *    person's decisions on Flow (the RunState is kept in Flow, so any worker resumes it);
 *  - the response or the error that ends the reply; a stopped run ends it as stopped.
 *
 * Connector questions (MCP elicitation) are shown by Flow itself, which holds them; the worker does not post them.
 */
import type { AgentInputItem } from '@openai/agents'
import type { FlowActivity } from './activities.js'
import type { PromptMessage } from './runtime-client.js'

/** The run's conversation as SDK input items, and the person's message the reply answers (the last one). */
export function chatConversation(messages: PromptMessage[]): { history: AgentInputItem[]; request: string } {
  const items: AgentInputItem[] = []
  const names = new Map<string, string>()
  for (const message of messages) {
    switch (message.role) {
      case 'user':
        items.push({ role: 'user', content: message.content } as AgentInputItem)
        break
      case 'assistant':
        if (message.content.trim()) items.push({ role: 'assistant', status: 'completed', content: [{ type: 'output_text', text: message.content }] } as AgentInputItem)
        for (const call of message.toolCalls ?? []) {
          names.set(call.id, call.name)
          items.push({ type: 'function_call', callId: call.id, name: call.name, arguments: call.arguments || '{}', status: 'completed' } as AgentInputItem)
        }
        break
      case 'tool':
        if (!message.toolCallId || !names.has(message.toolCallId)) break
        items.push({ type: 'function_call_result', callId: message.toolCallId, name: names.get(message.toolCallId), status: 'completed', output: { type: 'text', text: message.content } } as AgentInputItem)
        break
    }
  }
  const last = items.at(-1) as { role?: string; content?: unknown } | undefined
  if (last?.role === 'user' && typeof last.content === 'string') {
    items.pop()
    return { history: items, request: last.content }
  }
  return { history: items, request: '' }
}

/** One activity as Flow takes it for a chat run (POST /internal/agent-runtime/runs/{id}/activities). */
export type ChatActivity = {
  activityId?: string
  type: 'thought' | 'text.delta' | 'question.delta' | 'action' | 'action.result' | 'elicitation' | 'response' | 'error'
  body?: string
  callId?: string
  content?: { action: string; parameter?: unknown; result?: string }
  isError?: boolean
  change?: unknown
  elicitation?: unknown
}

export type ChatPost = (activity: ChatActivity) => Promise<{ settled?: boolean } | undefined>

/**
 * Turns a run's activities into Flow's chat activities and posts them one at a time, in order. Text and the
 * question being written are coalesced; anything else first sends what is pending. When Flow answers that the reply
 * is settled before the run ended it (stopped, or edited away), `onSettled` is called once.
 */
export class ChatActivityPoster {
  private queue: Promise<unknown> = Promise.resolve()
  private text = ''
  private question?: { callId: string; body: string }
  private timer?: ReturnType<typeof setTimeout>
  private ended = false
  private settledCalled = false

  constructor(
    private readonly post: ChatPost,
    private readonly options: { intervalMs?: number; onError?: (error: unknown) => void; onSettled?: () => void } = {},
  ) {}

  push(activity: FlowActivity) {
    switch (activity.type) {
      case 'text.delta':
        this.text += activity.delta
        this.schedule()
        return
      case 'question.delta':
        this.question = { callId: activity.activityId, body: activity.body }
        this.schedule()
        return
      case 'thought':
        // The ack only; reasoning stays out of the reply (as in-process).
        if (!activity.ephemeral) this.send({ type: 'thought', body: activity.body })
        return
      case 'connector.elicitation':
        return // Flow holds the question and shows it on the reply itself.
      case 'action':
        this.send({ type: 'action', activityId: `${activity.activityId}:call`, callId: activity.activityId, content: { action: activity.action, parameter: activity.parameter } })
        return
      case 'action.result':
        this.send({ type: 'action.result', activityId: `${activity.activityId}:result`, callId: activity.activityId, content: { action: activity.action, result: activity.result }, isError: activity.isError, ...(activity.change ? { change: activity.change } : {}) })
        return
      case 'elicitation':
        // The run suspends on it (ask_user's question ends the reply).
        this.ended = true
        this.send({ type: 'elicitation', activityId: 'elicitation', elicitation: activity.elicitation })
        return
      case 'response':
        this.ended = true
        this.send(activity.canceled ? { type: 'error', activityId: 'end', body: 'Generation stopped' } : { type: 'response', activityId: 'end', body: activity.body })
        return
      case 'error':
        this.ended = true
        this.send({ type: 'error', activityId: 'end', body: activity.body })
        return
    }
  }

  /** Sends what is pending and waits until every activity was posted. */
  async drain() {
    this.flush()
    await this.queue
  }

  private schedule() {
    this.timer ??= setTimeout(() => this.flush(), this.options.intervalMs ?? 100)
  }

  private flush() {
    if (this.timer) clearTimeout(this.timer)
    this.timer = undefined
    if (this.text) {
      const body = this.text
      this.text = ''
      this.enqueue({ type: 'text.delta', body })
    }
    if (this.question) {
      const { callId, body } = this.question
      this.question = undefined
      this.enqueue({ type: 'question.delta', callId, body })
    }
  }

  private send(activity: ChatActivity) {
    this.flush()
    this.enqueue(activity)
  }

  private enqueue(activity: ChatActivity) {
    const final = this.ended
    this.queue = this.queue
      .then(() => this.post(activity))
      .then(answer => {
        if (answer?.settled && !final && !this.settledCalled) {
          this.settledCalled = true
          this.options.onSettled?.()
        }
      })
      .catch(error => this.options.onError?.(error))
  }
}

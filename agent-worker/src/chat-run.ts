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
 * Each post carries its number in the run (`seq`): any of Flow's API servers may take it, a retry is stored once,
 * and Flow applies the posts in number order.
 *
 * Connector questions (MCP elicitation) are shown by Flow itself, which holds them; the worker does not post them.
 */
import type { AgentInputItem } from '@openai/agents'
import type { FlowActivity } from './activities.js'
import type { PromptMessage } from './runtime-client.js'

/** A user message as an SDK item: its text, then its images (data URLs), as the in-process reply sends them. */
function userItem(message: PromptMessage): AgentInputItem {
  if (!message.images?.length) return { role: 'user', content: message.content } as AgentInputItem
  return {
    role: 'user',
    content: [
      { type: 'input_text', text: message.content },
      ...message.images.map(image => ({ type: 'input_image', image: `data:${image.mediaType};base64,${image.data}` })),
    ],
  } as AgentInputItem
}

/**
 * The run's conversation as SDK input items, and the person's message the reply answers (the last one): its text
 * (`request`) and, when it carries images, the item to run (`input`).
 */
export function chatConversation(messages: PromptMessage[]): { history: AgentInputItem[]; request: string; input?: AgentInputItem[] } {
  const items: AgentInputItem[] = []
  const names = new Map<string, string>()
  for (const message of messages) {
    switch (message.role) {
      case 'user':
        items.push(userItem(message))
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
  if (last?.role === 'user' && Array.isArray(last.content)) {
    const item = items.pop()!
    const request = messages.findLast(message => message.role === 'user')?.content ?? ''
    return { history: items, request, input: [item] }
  }
  return { history: items, request: '' }
}

/** One activity as Flow takes it for a chat run (POST /internal/agent-runtime/runs/{id}/activities). */
export type ChatActivity = {
  /**
   * The post's number in the run (1, 2, …, in the order the poster sends them). Flow stores each number once and
   * applies the posts in number order, so a retried post (on any API server) is a no-op and posts that arrive out of
   * order still make the reply they make in order.
   */
  seq?: number
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

/** Waits before the retries of a post that failed in transit or with a server error (429, 5xx). */
export const chatPostRetryDelaysMs = [250, 1_000, 3_000]

/** Whether a failed post may succeed when sent again: no answer, or a server error (not a refusal such as 409). */
export function retryablePostError(error: unknown) {
  const status = (error as { status?: unknown })?.status
  return typeof status !== 'number' || status === 429 || status >= 500
}

/**
 * Turns a run's activities into Flow's chat activities and posts them one at a time, in order, each with its number
 * (`seq`). Text and the question being written are coalesced; anything else first sends what is pending. A post that
 * fails in transit or with a server error is sent again with the same number (Flow stores it once). When Flow
 * answers that the reply is settled before the run ended it (stopped, or edited away), `onSettled` is called once.
 */
export class ChatActivityPoster {
  private queue: Promise<unknown> = Promise.resolve()
  private text = ''
  private question?: { callId: string; body: string }
  private timer?: ReturnType<typeof setTimeout>
  private ended = false
  private settledCalled = false
  private seq = 0
  /** The arguments of each call, repeated with its result (Flow labels a finished call from them). */
  private readonly parameters = new Map<string, unknown>()

  constructor(
    private readonly post: ChatPost,
    private readonly options: { intervalMs?: number; retryDelaysMs?: number[]; onError?: (error: unknown) => void; onSettled?: () => void } = {},
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
        this.parameters.set(activity.activityId, activity.parameter)
        this.send({ type: 'action', activityId: `${activity.activityId}:call`, callId: activity.activityId, content: { action: activity.action, parameter: activity.parameter } })
        return
      case 'action.result': {
        const parameter = this.parameters.get(activity.activityId)
        this.send({ type: 'action.result', activityId: `${activity.activityId}:result`, callId: activity.activityId, content: { action: activity.action, ...(parameter === undefined ? {} : { parameter }), result: activity.result }, isError: activity.isError, ...(activity.change ? { change: activity.change } : {}) })
        return
      }
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
    const numbered: ChatActivity = { seq: ++this.seq, ...activity }
    this.queue = this.queue
      .then(() => this.deliver(numbered))
      .then(answer => {
        if (answer?.settled && !final && !this.settledCalled) {
          this.settledCalled = true
          this.options.onSettled?.()
        }
      })
      .catch(error => this.options.onError?.(error))
  }

  /** Posts one numbered activity, again with the same number while it fails in a way a retry may fix. */
  private async deliver(activity: ChatActivity) {
    const delays = this.options.retryDelaysMs ?? chatPostRetryDelaysMs
    for (let attempt = 0; ; attempt++) {
      try {
        return await this.post(activity)
      } catch (error) {
        if (attempt >= delays.length || !retryablePostError(error)) throw error
        await new Promise(resolve => setTimeout(resolve, delays[attempt]))
      }
    }
  }
}

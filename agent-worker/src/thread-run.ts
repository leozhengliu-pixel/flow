/**
 * Built-in agent sessions on the worker (comment-thread mentions and delegated issues;
 * api/cmd/server/agent_worker_thread.go, design doc §6.7, §7.2). Flow delivers the session, posts its first
 * activity and queues the run; the worker runs it from the thread Flow renders (the payload's context) and posts what
 * the in-process run appends to the session:
 *
 *  - an `action` per Flow tool call (its name), when the call starts; connector calls are posted by Flow's /mcp,
 *    host tools (ask_user, load_tools) not at all;
 *  - streamed text as `output`, at most once a second and after 100 bytes, as in-process; text written with tool calls
 *    is narration and the part not yet posted is dropped;
 *  - the elicitation the run stops for, with the turn's text: Flow writes the session's question (ask_user's options,
 *    or held connector calls as one approval) from it;
 *  - the `response`, or the `error`: Flow's own error words as they are, any other failure as the in-process run
 *    reports it. A stopped run posts nothing (the session is already stopped).
 *
 * Each post carries an activity id, so a retry (after a failure in transit or a server error) is stored once.
 */
import type { FlowActivity } from './activities.js'
import { chatPostRetryDelaysMs, retryablePostError } from './chat-run.js'

/** One activity as Flow takes it for a built-in session run (POST /internal/agent-runtime/runs/{id}/activities). */
export type ThreadActivity = {
  activityId: string
  type: 'action' | 'output' | 'elicitation' | 'response' | 'error'
  body: string
  elicitation?: unknown
}

export type ThreadPost = (activity: ThreadActivity) => Promise<unknown>

/** What an in-process run reports when its model call fails. */
export const threadProviderError = 'Agent execution failed. Check the provider configuration and retry.'

const hostTools = new Set(['ask_user', 'load_tools', 'ask_question'])

export class ThreadActivityPoster {
  private queue: Promise<unknown> = Promise.resolve()
  private pending = ''
  private turnText = ''
  private afterCall = false
  private sawResult = false
  private last = 0
  private seq = 0

  constructor(
    private readonly post: ThreadPost,
    /** `attempt`: the run attempt the posts belong to (a requeued run posts its own). */
    private readonly options: { attempt?: number; now?: () => number; retryDelaysMs?: number[]; onError?: (error: unknown) => void; onRejected?: () => void } = {},
  ) {}

  private now() {
    return this.options.now?.() ?? Date.now()
  }

  push(activity: FlowActivity) {
    switch (activity.type) {
      case 'text.delta': {
        if (this.afterCall) {
          // A new model turn after tool calls: its text is the turn's own.
          this.afterCall = false
          this.sawResult = false
          this.turnText = ''
        }
        this.turnText += activity.delta
        this.pending += activity.delta
        const now = this.now()
        if (this.pending.length > 32_000 || (now - this.last > 1_000 && this.pending.length > 100)) {
          this.enqueue('output', this.pending)
          this.pending = ''
          this.last = now
        }
        return
      }
      case 'action':
        // Narration written with tool calls is not the answer: what was not posted yet is dropped.
        this.pending = ''
        if (this.sawResult) {
          // A call after results: a new model turn that wrote no text.
          this.sawResult = false
          this.turnText = ''
        }
        this.afterCall = true
        if (hostTools.has(activity.action) || activity.action.startsWith('external_')) return
        this.enqueue('action', activity.action)
        return
      case 'action.result':
        this.sawResult = true
        this.afterCall = true
        return
      case 'elicitation':
        // The question (or the held calls) comes with the text of the turn that asked.
        this.pending = ''
        this.enqueue('elicitation', this.sawResult ? '' : this.turnText, activity.elicitation)
        return
      case 'response':
        this.pending = ''
        if (!activity.canceled) this.enqueue('response', activity.body)
        return
      case 'error':
        this.pending = ''
        this.enqueue('error', activity.flow ? activity.body : threadProviderError)
        return
      default:
        // thought (Flow posted the first activity), action.result, question.delta, connector.elicitation: nothing.
        return
    }
  }

  /** Waits until every activity was posted. */
  async drain() {
    await this.queue
  }

  private enqueue(type: ThreadActivity['type'], body: string, elicitation?: unknown) {
    const activity: ThreadActivity = { activityId: `${this.options.attempt ?? 1}:${++this.seq}:${type}`, type, body, ...(elicitation === undefined ? {} : { elicitation }) }
    this.queue = this.queue.then(() => this.deliver(activity)).catch(error => {
      if ((error as { status?: number })?.status === 409) this.options.onRejected?.()
      this.options.onError?.(error)
    })
  }

  private async deliver(activity: ThreadActivity) {
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

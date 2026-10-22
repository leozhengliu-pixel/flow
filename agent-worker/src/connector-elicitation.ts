import type { ConnectorElicitation } from './activities.js'

/** GET /internal/agent-runtime/runs/{id}/elicitations: the run's connector questions once past `after`. */
export type ElicitationApi = (after: number, waitMs: number, signal: AbortSignal) => Promise<{ supported: boolean; version: number; items: ConnectorElicitation[] }>

/**
 * Watches a run's connector questions (MCP elicitation) while a connector call is in flight. Flow is the
 * connector's MCP client and holds the question (the call waits up to 5 minutes for the person); the worker only
 * learns of it here and shows it, each change once (asked, then answered or cancelled). Flow answers that a run
 * without a person to ask (anything but a chat run) is not supported, and the watcher stops asking.
 */
export class ConnectorElicitationWatcher {
  private active = 0
  private abort?: AbortController
  private polling?: Promise<void>
  private version = 0
  private unsupported = false
  private readonly seen = new Map<string, string>()

  constructor(
    private readonly api: ElicitationApi,
    private readonly emit: (elicitation: ConnectorElicitation) => void,
    private readonly options = { waitMs: 20_000, retryMs: 1_000 },
  ) {}

  /** A connector call started; the returned function ends it. The watcher polls while any call is in flight. */
  begin(): () => Promise<void> {
    if (this.unsupported) return async () => {}
    this.active++
    if (!this.abort) {
      const abort = new AbortController()
      this.abort = abort
      this.polling = this.loop(abort.signal)
    }
    let ended = false
    return async () => {
      if (ended) return
      ended = true
      if (--this.active > 0) return
      const abort = this.abort
      const polling = this.polling
      this.abort = undefined
      this.polling = undefined
      abort?.abort()
      await polling
      // The question's resolution is recorded before the call returns: one last look delivers it.
      if (!this.unsupported && [...this.seen.values()].includes('pending')) await this.poll(0, new AbortController().signal).catch(() => {})
    }
  }

  private async loop(signal: AbortSignal) {
    while (!signal.aborted && !this.unsupported) {
      try {
        await this.poll(this.options.waitMs, signal)
      } catch {
        if (signal.aborted) return
        await new Promise(resolve => setTimeout(resolve, this.options.retryMs))
      }
    }
  }

  private async poll(waitMs: number, signal: AbortSignal) {
    const answer = await this.api(this.version, waitMs, signal)
    if (!answer.supported) {
      this.unsupported = true
      return
    }
    this.version = answer.version
    for (const item of answer.items) {
      if (this.seen.get(item.id) === item.status) continue
      this.seen.set(item.id, item.status)
      this.emit(item)
    }
  }
}

/**
 * Client for Flow's internal runtime API (api/cmd/server/agent_runtime_api.go, design doc §8.1): claim runs with
 * row leases, heartbeat, watch for stops (long poll), mint run tokens for /mcp, read the payload, keep suspended
 * state, finish the run. Authenticated with the worker secret; the worker names itself in Flow-Worker-Id.
 */
export type AgentRun = {
  id: string
  workspaceKey: string
  sessionId: string
  kind: string
  priority: number
  status: string
  attempts: number
  wrote: boolean
  cancelRequested: boolean
  leaseUntil?: number
  payload?: unknown
}

export type RunStatus = 'done' | 'failed' | 'canceled' | 'awaiting' | 'interrupted'

export class FlowRuntimeClient {
  constructor(private readonly baseUrl: string, private readonly secret: string, readonly workerId: string) {}

  private async call<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    const response = await fetch(`${this.baseUrl.replace(/\/$/, '')}/internal/agent-runtime${path}`, {
      method,
      headers: { Authorization: `Bearer ${this.secret}`, 'Flow-Worker-Id': this.workerId, ...(body === undefined ? {} : { 'content-type': 'application/json' }) },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    })
    const text = await response.text()
    if (!response.ok) throw new RuntimeApiError(response.status, `${method} ${path}: ${response.status} ${text.slice(0, 200)}`)
    return (text ? JSON.parse(text) : undefined) as T
  }

  /** Long-polls for up to `waitMs` (≤ 20 s) until runs are claimable for the free slots. */
  claim(free: Partial<Record<'chat' | 'thread' | 'loop' | 'background', number>>, waitMs = 0) {
    return this.call<{ runs: AgentRun[]; leaseMs: number; heartbeatMs: number }>('POST', '/claim', { free, waitMs })
  }

  /**
   * Long-polls up to `waitMs` (≤ 25 s) until a run this worker holds should stop (a cancel was requested or the
   * person stopped the session); `ignore` lists runs already stopping. The push path for stops: heartbeats carry
   * `cancel` too, but only every 15 s.
   */
  cancels(waitMs: number, ignore: string[] = [], signal?: AbortSignal) {
    return this.call<{ cancel: string[] }>('POST', '/cancels', { waitMs, ignore }, signal)
  }

  heartbeat(runId: string) {
    return this.call<{ leaseUntil: number; cancel: boolean }>('POST', `/runs/${encodeURIComponent(runId)}/heartbeat`, {})
  }

  token(runId: string) {
    return this.call<{ token: string; expiresAt: number; mcpPath: string }>('POST', `/runs/${encodeURIComponent(runId)}/token`, {})
  }

  payload(runId: string) {
    return this.call<{ run: AgentRun; principal: { mode: string; userId: string; tools?: string[] }; context?: { system: string; latestPrompt: string } }>('GET', `/runs/${encodeURIComponent(runId)}/payload`)
  }

  complete(runId: string, status: RunStatus, extra: { error?: string; usage?: unknown } = {}) {
    return this.call<AgentRun>('POST', `/runs/${encodeURIComponent(runId)}/complete`, { status, ...extra })
  }
}

export class RuntimeApiError extends Error {
  constructor(readonly status: number, message: string) {
    super(message)
  }
}

/**
 * Keeps one cancel long-poll open while the worker holds runs and calls `onCancel` once per run that should stop,
 * as soon as Flow records the stop (the poll is answered at once). Heartbeats remain the fallback when it fails.
 */
export class CancelWatcher {
  private readonly stopping = new Set<string>()
  private readonly abort = new AbortController()
  private wake?: () => void
  private closed = false

  constructor(
    private readonly client: Pick<FlowRuntimeClient, 'cancels'>,
    private readonly held: () => Iterable<string>,
    private readonly onCancel: (runId: string) => void,
    private readonly options = { waitMs: 20_000, retryMs: 1_000 },
  ) {}

  /** A run started: (re)start polling. */
  started() {
    this.wake?.()
  }

  /** A run finished: stop ignoring it. */
  finished(runId: string) {
    this.stopping.delete(runId)
  }

  close() {
    this.closed = true
    this.abort.abort()
    this.wake?.()
  }

  async run() {
    while (!this.closed) {
      if ([...this.held()].length === 0) {
        await new Promise<void>(resolve => { this.wake = resolve })
        this.wake = undefined
        continue
      }
      try {
        const { cancel } = await this.client.cancels(this.options.waitMs, [...this.stopping], this.abort.signal)
        for (const runId of cancel) {
          if (this.stopping.has(runId)) continue
          this.stopping.add(runId)
          this.onCancel(runId)
        }
      } catch {
        if (this.closed) return
        await new Promise(resolve => setTimeout(resolve, this.options.retryMs))
      }
    }
  }
}

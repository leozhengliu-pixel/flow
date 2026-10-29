/**
 * Client for Flow's internal runtime API (api/cmd/server/agent_runtime_api.go, design doc §8.1): claim runs with
 * row leases, heartbeat, watch for stops (long poll), mint run tokens for /mcp, read the payload, keep suspended
 * state, finish the run. Authenticated with the worker secret; the worker names itself in Flow-Worker-Id.
 */
import type { ConnectorElicitation } from './activities.js'
import type { GuardRequest, GuardResponse } from './run-guard.js'

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

/** One message of the conversation Flow renders for a run (a chat run's carries the latest tool calls). */
export type PromptMessage = {
  role: 'user' | 'assistant' | 'tool' | 'system'
  content: string
  toolCalls?: { id: string; name: string; arguments: string }[]
  toolCallId?: string
  isError?: boolean
  /** A user message's image inputs (files sent with it), the bytes base64 as the in-process reply sends them. */
  images?: { name: string; mediaType: string; data: string }[]
}

/** What a chat run's payload says about its reply (api/cmd/server/agent_worker_chat.go workerChatRunInfo). */
export type ChatRunInfo = {
  messageId: string
  maxTurns: number
  maxCalls: number
  request?: string
  resume?: { decisions: Record<string, string> }
  /** Tool groups the reply starts with besides the ones its request picks (the chat's surface, earlier tools). */
  groups?: string[]
}

/** What a built-in session run's payload says (api/cmd/server/agent_worker_thread.go workerThreadRunInfo). */
export type ThreadRunInfo = {
  taskVersion: number
  maxTurns: number
  maxCalls: number
  resume?: { decisions: Record<string, string> }
}

export type RunPayload = {
  run: AgentRun
  principal: { mode: string; userId: string; tools?: string[] }
  context?: { system: string; latestPrompt: string; messages?: PromptMessage[] }
  chat?: ChatRunInfo
  thread?: ThreadRunInfo
  notices?: string[]
}

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
  claim(free: Partial<Record<'chat' | 'thread' | 'loop' | 'background', number>>, waitMs = 0, max = 0) {
    return this.call<{ runs: AgentRun[]; leaseMs: number; heartbeatMs: number }>('POST', '/claim', { free, waitMs, ...(max > 0 ? { max } : {}) })
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

  /**
   * The run's payload. Reading it discovers the run's connector tools; `notices` are what discovery noticed (a
   * selected connector that was skipped), as an in-process loop run records them.
   */
  payload(runId: string) {
    return this.call<RunPayload>('GET', `/runs/${encodeURIComponent(runId)}/payload`)
  }

  /** Posts one activity of the run (a chat run's reply part: chat-run.ts). */
  activity(runId: string, activity: unknown) {
    return this.call<{ messageId?: string; settled?: boolean }>('POST', `/runs/${encodeURIComponent(runId)}/activities`, activity)
  }

  /** The session's suspended harness state (agent_session_states), or undefined when none is stored. */
  async getState(runId: string): Promise<string | undefined> {
    const response = await fetch(`${this.baseUrl.replace(/\/$/, '')}/internal/agent-runtime/runs/${encodeURIComponent(runId)}/state`, { headers: this.headers() })
    if (response.status === 404) return undefined
    const text = await response.text()
    if (!response.ok) throw new RuntimeApiError(response.status, `GET state: ${response.status} ${text.slice(0, 200)}`)
    return text
  }

  async putState(runId: string, state: string) {
    const response = await fetch(`${this.baseUrl.replace(/\/$/, '')}/internal/agent-runtime/runs/${encodeURIComponent(runId)}/state`, { method: 'PUT', headers: { ...this.headers(), 'content-type': 'application/json' }, body: state })
    if (!response.ok) throw new RuntimeApiError(response.status, `PUT state: ${response.status} ${(await response.text()).slice(0, 200)}`)
  }

  async deleteState(runId: string) {
    const response = await fetch(`${this.baseUrl.replace(/\/$/, '')}/internal/agent-runtime/runs/${encodeURIComponent(runId)}/state`, { method: 'DELETE', headers: this.headers() })
    if (!response.ok) throw new RuntimeApiError(response.status, `DELETE state: ${response.status}`)
  }

  private headers() {
    return { Authorization: `Bearer ${this.secret}`, 'Flow-Worker-Id': this.workerId }
  }

  /** Long-polls up to `waitMs` (≤ 20 s) until the run's connector questions move past `after` (connector-elicitation.ts). */
  elicitations(runId: string, after: number, waitMs: number, signal?: AbortSignal) {
    return this.call<{ supported: boolean; version: number; items: ConnectorElicitation[] }>('GET', `/runs/${encodeURIComponent(runId)}/elicitations?after=${after}&waitMs=${waitMs}`, undefined, signal)
  }

  /** One operation of the run's delegated-run guard (run-guard.ts, api/cmd/server/agent_runtime_guard.go). */
  guard(runId: string, request: GuardRequest) {
    return this.call<GuardResponse>('POST', `/runs/${encodeURIComponent(runId)}/guard`, request)
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

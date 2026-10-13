/**
 * Client for Flow's internal runtime API (api/cmd/server/agent_runtime_api.go, design doc §8.1): claim runs with
 * row leases, heartbeat, mint run tokens for /mcp, read the payload, keep suspended state, finish the run.
 * Authenticated with the worker secret; the worker names itself in Flow-Worker-Id.
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

  private async call<T>(method: string, path: string, body?: unknown, auth = this.secret): Promise<T> {
    const response = await fetch(`${this.baseUrl.replace(/\/$/, '')}/internal/agent-runtime${path}`, {
      method,
      headers: { Authorization: `Bearer ${auth}`, 'Flow-Worker-Id': this.workerId, ...(body === undefined ? {} : { 'content-type': 'application/json' }) },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await response.text()
    if (!response.ok) throw new RuntimeApiError(response.status, `${method} ${path}: ${response.status} ${text.slice(0, 200)}`)
    return (text ? JSON.parse(text) : undefined) as T
  }

  /** Long-polls for up to `waitMs` (≤ 20 s) until runs are claimable for the free slots. */
  claim(free: Partial<Record<'chat' | 'thread' | 'loop' | 'background', number>>, waitMs = 0) {
    return this.call<{ runs: AgentRun[]; leaseMs: number; heartbeatMs: number }>('POST', '/claim', { free, waitMs })
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

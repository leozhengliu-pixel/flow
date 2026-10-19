/**
 * Live updates for coding sessions. The server pushes `coding_session.*` realtime events (aggregateId = session id,
 * payload `{agentSessionId?, issueId?, state?, phase?}`); use-workspace-realtime turns them into this window event
 * so open session views refresh at once without a workspace resync. Activities keep arriving as `agent_task.*`
 * pushes (AGENT_TASK_ACTIVITY_EVENT), because a session's activities live on its AgentTask anchor.
 */
export const CODING_SESSION_EVENT = 'flow:coding-session-changed'

export type CodingSessionEventDetail = { sessionId: string; agentSessionId?: string; issueId?: string }

export const isCodingSessionRealtimeEvent = (type: string) => type.startsWith('coding_session.')

/** Dispatches CODING_SESSION_EVENT for one realtime event (no-op for other event types). */
export function dispatchCodingSessionRealtimeEvent(event: { type: string; aggregateId?: string; payload?: unknown }): boolean {
  if (!isCodingSessionRealtimeEvent(event.type)) return false
  const payload = (event.payload && typeof event.payload === 'object' ? event.payload : {}) as Record<string, unknown>
  const text = (value: unknown) => typeof value === 'string' && value ? value : undefined
  const detail: CodingSessionEventDetail = { sessionId: event.aggregateId ?? text(payload.sessionId) ?? '', agentSessionId: text(payload.agentSessionId), issueId: text(payload.issueId) }
  window.dispatchEvent(new CustomEvent<CodingSessionEventDetail>(CODING_SESSION_EVENT, { detail }))
  return true
}

/** Local notification after this client changed a session (start, stop, restart…), so other views of it refresh. */
export function notifyCodingSessionChanged(detail: CodingSessionEventDetail) {
  window.dispatchEvent(new CustomEvent<CodingSessionEventDetail>(CODING_SESSION_EVENT, { detail }))
}

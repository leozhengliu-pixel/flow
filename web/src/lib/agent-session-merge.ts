import type { AgentSession } from '@/types/flow'

/**
 * Agent chats reach the app in two shapes: whole (a chat read with GET /api/agent/sessions/{id}, a stream, or a short
 * chat a list sends whole) and as a summary (`messagesOmitted`: a list or the bootstrap left the messages out). These
 * merges keep a summary from ever replacing messages the app already has, and keep a late, older response from
 * replacing a newer copy. Whatever they keep is never kept silently stale: a newer summary over loaded messages marks
 * the chat `needsDetailRefresh`, so the surface showing it reads it again.
 */
export function mergeAgentSession(previous: AgentSession | undefined, incoming: AgentSession): AgentSession {
  if (!previous || previous.id !== incoming.id) return incoming
  const older = stamp(incoming) < stamp(previous)
  const newer = stamp(incoming) > stamp(previous)
  if (!incoming.messagesOmitted) {
    if (!older) return incoming.needsDetailRefresh ? { ...incoming, needsDetailRefresh: false } : incoming
    // A late read older than what is shown: never replaces a current copy, and only fills in a summary's messages
    // (still to be read again, since the chat changed after it).
    if (!previous.messagesOmitted && !previous.needsDetailRefresh) return previous
    return { ...previous, messages: incoming.messages, messagesOmitted: false, needsDetailRefresh: true }
  }
  if (previous.messagesOmitted) return older ? previous : incoming
  if (older) return previous
  // A summary of a chat whose messages are loaded: take its fields (title, favorite, read state) and keep the messages;
  // a newer one means the conversation changed on the server, so it is read again.
  return { ...incoming, messages: previous.messages, messagesOmitted: false, needsDetailRefresh: Boolean(previous.needsDetailRefresh || newer) }
}

/**
 * The chats of a fresh list (`incoming`, in its order) merged with the copies held so far: a chat the list no longer
 * has (deleted, or no longer the viewer's) is dropped with its messages.
 */
export function mergeAgentSessions(current: readonly AgentSession[] | undefined, incoming: readonly AgentSession[] | undefined): AgentSession[] {
  const next = incoming ?? []
  if (!current?.length) return [...next]
  const previous = new Map(current.map(session => [session.id, session]))
  return next.map(session => mergeAgentSession(previous.get(session.id), session))
}

/** Whether `session` must be read whole before its conversation is shown. */
export function agentSessionNeedsDetail(session: AgentSession | undefined): boolean {
  return Boolean(session && (session.messagesOmitted || session.needsDetailRefresh))
}

/** The summary of a chat kept in persisted caches: no messages and no notes, only what lists show. */
export function agentSessionSummary(session: AgentSession): AgentSession {
  const summary: AgentSession = { ...session, messages: [], messagesOmitted: true }
  delete summary.needsDetailRefresh
  delete summary.feedback
  return summary
}

function stamp(session: AgentSession) {
  const time = Date.parse(session.updatedAt)
  return Number.isFinite(time) ? time : 0
}

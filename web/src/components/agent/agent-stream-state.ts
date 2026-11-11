import type { AgentMessage, AgentMessagePart, AgentSession } from '@/types/flow'
import type { AgentStreamEvent } from '@/lib/agent-stream'

export function applyAgentStreamEvent(session: AgentSession | undefined, event: AgentStreamEvent): AgentSession | undefined {
  if (event.type === 'session.completed' && event.session) return event.session
  if (event.type === 'session.started' && event.session) {
    // The reply is saved as "streaming" on the server from here on; the local copy says the same, so a stream that
    // ends without session.completed (or an error) leaves a reply the surface follows until the server settles it.
    const message: AgentMessage = { id: event.messageId ?? `stream-${Date.now()}`, role: 'assistant', content: '', parts: [], status: 'streaming', createdAt: new Date().toISOString() }
    return { ...event.session, messages: [...event.session.messages, message] }
  }
  if (!session || !event.messageId) return session
  const messages = session.messages.map(message => {
    if (message.id !== event.messageId) return message
    const parts = [...(message.parts ?? [])]
    if (event.part) {
      const index = parts.findIndex(part => part.id === event.part?.id)
      const before = index < 0 && event.beforePartId ? parts.findIndex(part => part.id === event.beforePartId) : -1
      if (index >= 0) parts[index] = event.part
      else if (before >= 0) parts.splice(before, 0, event.part)
      else parts.push(event.part)
    }
    // text.replaced: the server moved mid-task narration into the work group, so the answer restarts.
    const content = event.type === 'text.delta' ? message.content + (event.delta ?? '') : event.type === 'text.replaced' ? event.delta ?? '' : message.content
    const text = parts.findIndex(part => part.type === 'text')
    if (event.type === 'text.replaced' && text >= 0) parts[text] = { ...parts[text], text: content }
    return { ...message, content, parts }
  })
  // updatedAt stays the server's: it orders copies of the chat (agent-session-merge), and this device's clock does not.
  return { ...session, messages }
}

export function markAgentSessionStopped(session: AgentSession): AgentSession {
  return markAgentSessionFailed(session, 'Generation stopped', 'stopped')
}

/**
 * Ends the trailing reply with an error row, the way the server saves a failed turn: running parts stop and the
 * reason is added once. Lets a turn that failed mid-stream show its error (with Retry) without re-reading the chat.
 */
export function markAgentSessionFailed(session: AgentSession, reason: string, suffix = 'error'): AgentSession {
  const last = session.messages.at(-1)
  if (!last || last.role !== 'assistant') return session
  const parts = (last.parts ?? []).map(part => part.status === 'running' || part.status === 'pending' ? { ...part, status: 'error' as const } : part)
  if (!parts.some(part => part.type === 'error')) parts.push({ id: `${last.id}-${suffix}`, type: 'error', status: 'error', text: reason })
  // The turn has ended (failed or stopped): it is no longer "streaming", so nothing follows it as still running.
  return { ...session, messages: session.messages.map(message => message.id === last.id ? { ...message, parts, status: undefined } : message) }
}

/**
 * The trailing reply of a stream that dropped after the server took the message, for views that hold their own
 * message list: it stays "streaming" (with the work so far that was only in the live stream) so the view follows the
 * server's saved copy (useAgentReplyFollow) instead of offering to send the message again.
 */
export function markDroppedAgentReply(messages: AgentMessage[], messageId: string | undefined, work: AgentMessagePart[] = [], now = new Date()): AgentMessage[] {
  const last = messages.at(-1)
  if (last?.role === 'assistant') {
    const known = new Set((last.parts ?? []).map(part => part.id))
    const parts = [...(last.parts ?? []), ...work.filter(part => !known.has(part.id))]
    return messages.map((message, index) => index === messages.length - 1 ? { ...message, parts, status: 'streaming' as const } : message)
  }
  if (!messageId) return messages
  return [...messages, { id: messageId, role: 'assistant', content: '', parts: work, status: 'streaming', createdAt: now.toISOString() }]
}

import type { AgentMessage, AgentSession } from '@/types/flow'
import type { AgentStreamEvent } from '@/lib/agent-stream'

export function applyAgentStreamEvent(session: AgentSession | undefined, event: AgentStreamEvent): AgentSession | undefined {
  if (event.type === 'session.completed' && event.session) return event.session
  if (event.type === 'session.started' && event.session) {
    const message: AgentMessage = { id: event.messageId ?? `stream-${Date.now()}`, role: 'assistant', content: '', parts: [], createdAt: new Date().toISOString() }
    return { ...event.session, messages: [...event.session.messages, message] }
  }
  if (!session || !event.messageId) return session
  const messages = session.messages.map(message => {
    if (message.id !== event.messageId) return message
    const parts = [...(message.parts ?? [])]
    if (event.part) {
      const index = parts.findIndex(part => part.id === event.part?.id)
      if (index >= 0) parts[index] = event.part
      else parts.push(event.part)
    }
    return { ...message, content: event.type === 'text.delta' ? message.content + (event.delta ?? '') : message.content, parts }
  })
  return { ...session, messages, updatedAt: new Date().toISOString() }
}

export function markAgentSessionStopped(session: AgentSession): AgentSession {
  const last = session.messages.at(-1)
  if (!last || last.role !== 'assistant') return session
  const parts = (last.parts ?? []).map(part => part.status === 'running' ? { ...part, status: 'error' as const } : part)
  if (!parts.some(part => part.type === 'error')) parts.push({ id: `${last.id}-stopped`, type: 'error', status: 'error', text: 'Generation stopped' })
  return { ...session, messages: session.messages.map(message => message.id === last.id ? { ...message, parts } : message) }
}

import type { AgentMessagePart, AgentSession } from '@/types/flow'
import { ApiError, apiFetch, jsonRequest } from '@/lib/api-client'

export interface AgentStreamEvent {
  type: 'session.started' | 'text.delta' | 'reasoning.delta' | 'tool.started' | 'tool.delta' | 'tool.completed' | 'tool.approval_required' | 'tool.approval_resolved' | 'session.completed' | 'error'
  session?: AgentSession
  messageId?: string
  delta?: string
  part?: AgentMessagePart
  approvalId?: string
  decision?: 'approved' | 'rejected'
  error?: string
}

export type AgentStreamInput = {
  message: string
  issueIds?: string[]
  skillIds?: string[]
  location?: 'page' | 'toolbar'
}

export function streamNewAgentSession(input: AgentStreamInput, onEvent: (event: AgentStreamEvent) => void, signal?: AbortSignal) {
  return streamAgentRequest('/api/agent/sessions/stream', 'POST', input, onEvent, signal)
}

export function streamAgentSessionMessage(id: string, message: string, onEvent: (event: AgentStreamEvent) => void, signal?: AbortSignal) {
  return streamAgentRequest(`/api/agent/sessions/${encodeURIComponent(id)}/messages/stream`, 'POST', { message }, onEvent, signal)
}

export function streamAgentSessionMessageEdit(id: string, messageId: string, message: string, onEvent: (event: AgentStreamEvent) => void, signal?: AbortSignal) {
  return streamAgentRequest(`/api/agent/sessions/${encodeURIComponent(id)}/messages/${encodeURIComponent(messageId)}/stream`, 'PATCH', { message }, onEvent, signal)
}

async function streamAgentRequest(url: string, method: 'POST' | 'PATCH', input: unknown, onEvent: (event: AgentStreamEvent) => void, signal?: AbortSignal) {
  const response = await apiFetch(url, { ...jsonRequest(method, input), signal })
  if (!response.ok) {
    const payload = await response.json().catch(() => null)
    throw new ApiError(payload?.error || `Request failed: ${response.status}`, response.status, payload?.code, payload?.current)
  }
  if (!response.body) throw new Error('Flow Agent returned an empty stream')
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let completed: AgentSession | undefined
  const consume = (block: string) => {
    const data = block.split(/\r?\n/).filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n')
    if (!data) return
    const event = JSON.parse(data) as AgentStreamEvent
    onEvent(event)
    if (event.type === 'error') throw new Error(event.error || 'Flow Agent stream failed')
    if (event.type === 'session.completed') completed = event.session
  }
  try {
    while (true) {
      const { value, done } = await reader.read()
      buffer += decoder.decode(value, { stream: !done })
      const blocks = buffer.split(/\r?\n\r?\n/)
      buffer = blocks.pop() ?? ''
      blocks.forEach(consume)
      if (done) break
    }
    if (buffer.trim()) consume(buffer)
  } finally {
    reader.releaseLock()
  }
  if (!completed) throw new Error('Flow Agent stream ended before completion')
  return completed
}

import type { AgentMessage, AgentMessagePart } from '@/types/flow'
import { classifyAgentError } from './agent-error-detail'
import { isStaleAgentReply, settleStaleAgentReply } from './agent-reply-status'

/** The reason the server (and the web, when it ends a stream itself) records on a reply the user stopped. */
export const STOPPED_REPLY_TEXT = 'Generation stopped'

/** Whether this reply was ended with the Stop button (the server saves it with a "Generation stopped" error part). */
export function isStoppedAgentReply(message: AgentMessage | undefined) {
  if (message?.role !== 'assistant') return false
  // A reply saved with its outcome says whether its owner stopped it; older replies tell by their error part.
  if (message.outcome) return message.outcome.status === 'canceled'
  return Boolean(message.parts?.some(part => part.type === 'error' && classifyAgentError(part.text ?? '') === 'stopped'))
}

/**
 * How a stopped reply reads: the partial answer is dropped and there is no "stopped" row, Retry or
 * rating — only the work the agent had done stays, folded as "Worked for N seconds". A reply stopped before any work
 * leaves nothing behind (see `isBlankStoppedReply`), so the message you sent stands alone.
 * A reply whose run went away (interrupted, or it never reached a worker) reads like a completed one: only its error
 * row goes (agent-reply-status.ts; the chat's status says it stopped responding or didn't start).
 */
export function settleStoppedAgentReply(message: AgentMessage): AgentMessage {
  if (isStaleAgentReply(message)) return settleStaleAgentReply(message)
  if (!isStoppedAgentReply(message)) return message
  return { ...message, content: '', parts: (message.parts ?? []).filter(part => part.type !== 'text' && part.type !== 'error') }
}

/**
 * A finished reply with nothing to show: a stopped or stale one once it is settled, or (like the reference
 * app) an empty turn that came back with no answer and no work. Only a saved reply's outcome marks the latter, so the
 * placeholder of a reply still being written is never hidden.
 */
export function isBlankStoppedReply(message: AgentMessage) {
  if (message.role === 'assistant' && message.outcome?.status === 'completed' && !message.content.trim() && !message.parts?.length) return true
  if (isStaleAgentReply(message)) {
    const settled = settleStaleAgentReply(message)
    return !settled.content.trim() && !settled.parts?.length
  }
  return isStoppedAgentReply(message) && settleStoppedAgentReply(message).parts?.length === 0
}

/**
 * The trailing reply of a stream the user stopped, for views that hold their own message list: whatever text it
 * streamed is kept on the message (the server saves it the same way) with the "Generation stopped" marker, plus the
 * work parts that were still only in the live stream.
 */
export function markStoppedAgentReply(messages: AgentMessage[], work: AgentMessagePart[] = [], now = new Date()): AgentMessage[] {
  const last = messages.at(-1)
  const settled = work.map(part => part.status === 'running' || part.status === 'pending' ? { ...part, status: 'error' as const } : part)
  const marker: AgentMessagePart = { id: `${last?.id ?? 'stream'}-stopped`, type: 'error', status: 'error', text: STOPPED_REPLY_TEXT }
  if (!last) return messages
  if (last.role === 'assistant') {
    const known = new Set((last.parts ?? []).map(part => part.id))
    const parts = [...(last.parts ?? []), ...settled.filter(part => !known.has(part.id))]
    if (!parts.some(part => part.type === 'error')) parts.push(marker)
    return messages.map((message, index) => index === messages.length - 1 ? { ...message, parts } : message)
  }
  return [...messages, { id: `stopped-${now.getTime()}`, role: 'assistant', content: '', parts: [...settled, marker], createdAt: now.toISOString() }]
}

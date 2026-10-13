import type { AgentMessage, AgentMessagePart } from '@/types/flow'
import { classifyAgentError } from './agent-error-detail'

/** The reason the server (and the web, when it ends a stream itself) records on a reply the user stopped. */
export const STOPPED_REPLY_TEXT = 'Generation stopped'

/** Whether this reply was ended with the Stop button (the server saves it with a "Generation stopped" error part). */
export function isStoppedAgentReply(message: AgentMessage | undefined) {
  return message?.role === 'assistant' && Boolean(message.parts?.some(part => part.type === 'error' && classifyAgentError(part.text ?? '') === 'stopped'))
}

/**
 * How a stopped reply reads: the partial answer is dropped and there is no "stopped" row, Retry or
 * rating — only the work the agent had done stays, folded as "Worked for N seconds". A reply stopped before any work
 * leaves nothing behind (see `isBlankStoppedReply`), so the message you sent stands alone.
 */
export function settleStoppedAgentReply(message: AgentMessage): AgentMessage {
  if (!isStoppedAgentReply(message)) return message
  return { ...message, content: '', parts: (message.parts ?? []).filter(part => part.type !== 'text' && part.type !== 'error') }
}

/** A stopped reply with nothing left to show once it is settled. */
export function isBlankStoppedReply(message: AgentMessage) {
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

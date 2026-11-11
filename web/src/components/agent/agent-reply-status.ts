import type { AgentMessage, AgentSession } from '@/types/flow'

/**
 * How a chat shows a reply whose run went away, as the reference app shows a stale session. The server keeps the
 * structured outcome (api/cmd/server/agent_reply_outcome.go); the chat itself has no copy for it — no error row, no
 * "interrupted" text, the work the reply did stays as on a completed reply — and the chat's status reads:
 * - "Stopped responding" when the request was acknowledged: the reply was interrupted (outcome "interrupted") after
 *   the server accepted it and handed it to the model or an agent worker;
 * - "Didn't start" when it never reached the model or a worker (outcome "failed", reason "worker_unavailable": no
 *   agent worker took the run). The Agent panel says "Agent didn't start" with "The request may not have reached the
 *   agent", and offers Retry only when running the turn again cannot repeat a write.
 */

/** The error row text the server saved on interrupted replies (agentReplyInterrupted in agent_live_reply.go). */
export const AGENT_REPLY_INTERRUPTED_TEXT = 'The reply was interrupted before it finished. Retry to run it again.'

/** Error rows of replies that never reached a worker, saved before outcomes (workerChatNoWorker and the queue failure). */
const NOT_STARTED_TEXTS = ['No agent worker is available; try again or contact your admin', 'The reply could not be queued for the agent worker.']

/** The reference app's stale session labels (shared with coding sessions, coding-session-model.ts). */
export const AGENT_STALE_LABELS = {
  stoppedResponding: 'Stopped responding',
  didntStart: 'Didn’t start',
  panelDidntStart: 'Agent didn’t start',
  panelDidntStartDetail: 'The request may not have reached the agent',
} as const

export type AgentReplyStale = {
  /** The request was acknowledged (it reached the model or a worker) before its run went away. */
  started: boolean
  /** The status label: "Stopped responding" or "Didn't start". */
  label: string
  /** Running the turn again cannot repeat a write: it never reached a worker, or no tool that may write ran. */
  retrySafe: boolean
}

/** The reply's closing error row: the server saves it as "<message id>_error". */
function isClosingErrorPart(message: AgentMessage, part: NonNullable<AgentMessage['parts']>[number]) {
  return part.type === 'error' && (part.id === `${message.id}_error` || part.text === AGENT_REPLY_INTERRUPTED_TEXT || NOT_STARTED_TEXTS.includes(part.text ?? ''))
}

function hasErrorText(message: AgentMessage, texts: string[]) {
  return Boolean(message.parts?.some(part => part.type === 'error' && texts.includes(part.text ?? '')))
}

/** Whether a finished reply was interrupted after it was acknowledged (its outcome, or for older replies its error row). */
export function isInterruptedAgentReply(message: AgentMessage | undefined) {
  if (message?.role !== 'assistant' || message.status === 'streaming') return false
  if (message.outcome) return message.outcome.status === 'interrupted'
  return hasErrorText(message, [AGENT_REPLY_INTERRUPTED_TEXT])
}

/** Whether a finished reply never reached the model or a worker. */
export function isNotStartedAgentReply(message: AgentMessage | undefined) {
  if (message?.role !== 'assistant' || message.status === 'streaming') return false
  if (message.outcome) return message.outcome.status === 'failed' && message.outcome.reason === 'worker_unavailable'
  return hasErrorText(message, NOT_STARTED_TEXTS)
}

/** Whether the chat shows the reply as a stale session instead of an error row. */
export function isStaleAgentReply(message: AgentMessage | undefined) {
  return isInterruptedAgentReply(message) || isNotStartedAgentReply(message)
}

/** A stale reply as the chat shows it: without its error row, the rest as a completed reply. */
export function settleStaleAgentReply(message: AgentMessage): AgentMessage {
  if (!isStaleAgentReply(message)) return message
  return { ...message, parts: (message.parts ?? []).filter(part => !isClosingErrorPart(message, part)) }
}

/** Tools that only read: running them again repeats no change. */
const READ_TOOL = /^(get|list|search|read|fetch|find|load|lookup|ask|report|query|view|transcribe)_/
const READ_TOOL_NAMES = new Set(['web_search', 'load_tools', 'ask_user', 'report_progress'])

/** Whether a tool call may have changed something (a write tool, or a connector tool whose effect is unknown). */
function mayHaveWritten(part: NonNullable<AgentMessage['parts']>[number]) {
  const call = part.toolCall
  if (!call) return false
  if (call.change) return true
  const name = call.name.replace(/^mcp__flow\./, '')
  return !(READ_TOOL.test(name) || READ_TOOL_NAMES.has(name))
}

/** The stale state of a reply whose run went away; undefined for any other reply. */
export function agentReplyStale(message: AgentMessage | undefined): AgentReplyStale | undefined {
  if (!message || !isStaleAgentReply(message)) return undefined
  const started = isInterruptedAgentReply(message)
  const shown = settleStaleAgentReply(message)
  return {
    started,
    label: started ? AGENT_STALE_LABELS.stoppedResponding : AGENT_STALE_LABELS.didntStart,
    retrySafe: !(shown.parts ?? []).some(mayHaveWritten),
  }
}

/** A chat's stale state: its latest reply's, when that reply's run went away. */
export function agentSessionStale(session: Pick<AgentSession, 'messages'> | undefined) {
  const last = session?.messages?.findLast(message => message.role === 'assistant')
  return last && session?.messages?.at(-1)?.id === last.id ? agentReplyStale(last) : undefined
}

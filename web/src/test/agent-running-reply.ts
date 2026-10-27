import type { AgentMessage, AgentSession } from '@/types/flow'

/**
 * A chat whose reply is still running on the server, as a reloaded surface reads it: the question, then the reply
 * saved as "streaming" with its text so far, a tool call waiting for approval and a connector question.
 */
export function runningReplySession(overrides: Partial<AgentSession> = {}): AgentSession {
  const now = new Date().toISOString()
  const asked: AgentMessage = { id: 'q', role: 'user', content: 'Delete the branch', createdAt: now }
  const streaming: AgentMessage = {
    id: 'a', role: 'assistant', content: 'Checking the branch first.', status: 'streaming', createdAt: now, updatedAt: now,
    parts: [
      { id: 'text', type: 'text', text: 'Checking the branch first.', status: 'running' },
      { id: 'tool', type: 'toolCall', status: 'pending', toolCall: { id: 'call', name: 'external_code__delete_branch', title: 'Delete branch', arguments: {}, status: 'pending', approvalId: 'approval-1' } },
      { id: 'elicit-1', type: 'elicitation', status: 'pending', elicitation: { id: 'elicit-1', sessionId: overrides.id ?? 'session-live', connectorName: 'Local tools', connectorUrl: 'http://127.0.0.1:9000/mcp', mode: 'form', message: 'Which branch?', schema: { type: 'object', properties: { branch: { type: 'string', title: 'Branch' } }, required: ['branch'] } } },
    ],
  }
  return {
    id: 'session-live', slugId: 'live', userId: 'user-1', title: 'Branch', favorite: false, location: 'toolbar', issueIds: [], skillIds: [],
    messages: [asked, streaming], createdAt: now, updatedAt: now, ...overrides,
  }
}

/** The same chat once the reply finished: the approval and the question answered (elsewhere), the final text saved. */
export function finishedReplySession(running: AgentSession): AgentSession {
  const [asked, streaming] = running.messages
  const later = new Date(Date.now() + 5000).toISOString()
  return {
    ...running,
    updatedAt: later,
    messages: [asked, {
      id: streaming.id, role: 'assistant', content: 'Deleted the branch.', createdAt: streaming.createdAt, durationMs: 4000,
      parts: [
        { id: 'tool', type: 'toolCall', status: 'completed', toolCall: { ...streaming.parts![1].toolCall!, status: 'completed', result: { ok: true } } },
        { id: 'elicit-1', type: 'elicitation', status: 'completed', elicitation: { ...streaming.parts![2].elicitation!, action: 'accept' } },
        { id: 'text', type: 'text', text: 'Deleted the branch.', status: 'completed' },
      ],
    }],
  }
}

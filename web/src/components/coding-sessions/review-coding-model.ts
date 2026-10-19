import { startReviewCodingSession } from '@/lib/coding-sessions-api'
import type { ReviewEvent } from '@/types/flow'
import { notifyCodingSessionChanged } from './coding-session-events'

/** The prompt for "Send comment to agent": the comment, its file and line, and the review it belongs to. */
export function reviewCommentPrompt(event: Pick<ReviewEvent, 'body' | 'path' | 'line'> & { actor?: Pick<ReviewEvent['actor'], 'displayName'> }): string {
  const where = event.path ? ` on \`${event.path}${event.line ? `:${event.line}` : ''}\`` : ''
  const who = event.actor?.displayName ? ` from ${event.actor.displayName}` : ''
  return `Address this review comment${who}${where}:\n\n${(event.body ?? '').trim()}`
}

/** "Send comment to agent" on a review comment: a follow-up turn on the PR's coding session (or a new one on its branch). */
export async function sendReviewCommentToCodingAgent(reviewId: string, event: Parameters<typeof reviewCommentPrompt>[0]) {
  const session = await startReviewCodingSession(reviewId, { kind: 'followUp', prompt: reviewCommentPrompt(event) })
  notifyCodingSessionChanged({ sessionId: session.id, agentSessionId: session.agentSessionId, issueId: session.issueId })
  return session
}

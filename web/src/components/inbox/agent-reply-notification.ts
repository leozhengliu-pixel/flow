import { projectPath } from '@/lib/app-routes'
import type { Notification, Project } from '@/types/flow'

/**
 * An agent's reply in an issue, document or project thread ("Flow replied: <first line>"). One notification per thread is updated in
 * place, so it shows the latest reply rather than a count.
 */
export function agentReplyLine(notification: Notification, t: (source: string) => string) {
  const name = notification.actor.displayName || notification.actor.name || t('Agent')
  const kind = notification.payload?.agentActivity
  const excerpt = String(notification.payload?.excerpt ?? '').trim()
  const template = kind === 'finished'
    ? (excerpt ? '{name} finished: {text}' : '{name} finished')
    : kind === 'elicitation'
    ? (excerpt ? '{name} is waiting for your input: {text}' : '{name} is waiting for your input')
    : kind === 'error'
      ? (excerpt ? '{name} ran into an error: {text}' : '{name} ran into an error')
      : (excerpt ? '{name} replied: {text}' : '{name} replied')
  return t(template).replace('{name}', name).replace('{text}', excerpt)
}

/**
 * A finished coding session's pull request, when the workspace has its review: the row then opens that review beside the
 * session's agent panel instead of the issue. Replies to comments and sessions without a pull request keep the issue.
 */
export function finishedSessionTarget(data: { reviews: { id: string }[] }, notification: Notification): { sessionId?: string; sessionReviewId?: string } {
  if (notification.type !== 'agentReply' || notification.payload?.agentActivity !== 'finished') return {}
  const sessionId = notification.payload.codingSessionId
  const reviewId = notification.payload.reviewId
  if (!sessionId || !reviewId || !data.reviews.some(review => review.id === reviewId)) return {}
  return { sessionId, sessionReviewId: reviewId }
}

/**
 * An agent reply about a session that no comment started (a delegation, e.g. one that finished without a pull request):
 * the issue opens at `#comment-<session>` with the session's card outlined.
 */
export function pointsAtSessionCard(notification: Notification) {
  return notification.type === 'agentReply' && notification.sourceType === 'agentSession' && !notification.commentId && Boolean(notification.sourceId)
}

/** Agent replies in a project's comment thread link to the comment on the project's Activity tab. */
export function agentReplyProjectPath(workspaceKey: string, project: Pick<Project, 'slugId'>, commentId?: string) {
  return `${projectPath(workspaceKey, project, 'activity')}${commentId ? `#comment-${encodeURIComponent(commentId)}` : ''}`
}

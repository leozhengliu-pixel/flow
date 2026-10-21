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

/** Agent replies in a project's comment thread link to the comment on the project's Activity tab. */
export function agentReplyProjectPath(workspaceKey: string, project: Pick<Project, 'slugId'>, commentId?: string) {
  return `${projectPath(workspaceKey, project, 'activity')}${commentId ? `#comment-${encodeURIComponent(commentId)}` : ''}`
}

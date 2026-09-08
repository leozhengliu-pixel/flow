import type { ActivityEvent, BootstrapData } from '@/types/flow'

export type ActivityContext = Pick<BootstrapData, 'users' | 'projects' | 'labels' | 'cycles' | 'issues' | 'states'>
type Translate = (text: string) => string

export function describeIssueActivity(event: ActivityEvent, context?: ActivityContext, t: Translate = text => text): string | null {
  const m = event.metadata ?? {}
  const phrase = (template: string, values: Record<string, string> = {}) => t(template).replace(/\{(\w+)\}/g, (_, key: string) => values[key] ?? '')
  const person = (id: string) => context?.users.find(user => user.id === id)?.displayName
  const project = (id: string) => context?.projects.find(project => project.id === id)?.name
  if (event.type === 'issue.created') return t('created the issue')
  if (event.type === 'issue.updated') {
    const changes: string[] = []
    if (m.state) changes.push(m.stateBefore ? phrase('moved from {from} to {to}', { from: t(m.stateBefore), to: t(m.state) }) : phrase('moved to {state}', { state: t(m.state) }))
    if (m.title) changes.push(phrase('changed the title to {title}', { title: m.title }))
    if (m.priority) changes.push(phrase('set priority to {priority}', { priority: t(m.priority) }))
    if ('assignee' in m) changes.push(!m.assignee ? t('removed the assignee') : m.assignee === event.actor.id ? t('assigned the issue to themselves') : person(m.assignee) ? phrase('assigned the issue to {name}', { name: person(m.assignee)! }) : t('changed the assignee'))
    if ('delegate' in m) changes.push(!m.delegate ? t('removed the delegate') : person(m.delegate) ? phrase('delegated the issue to {name}', { name: person(m.delegate)! }) : t('changed the delegate'))
    if ('project' in m) changes.push(!m.project ? t('removed the issue from its project') : project(m.project) ? phrase('added to project {name}', { name: project(m.project)! }) : t('changed the project'))
    if ('projectMilestone' in m) {
      const milestone = context?.projects.flatMap(item => item.milestones ?? []).find(item => item.id === m.projectMilestone)
      changes.push(!m.projectMilestone ? t('removed the milestone') : milestone ? phrase('added to milestone {name}', { name: milestone.name }) : t('changed the milestone'))
    }
    if ('cycle' in m) {
      const cycle = context?.cycles.find(item => item.id === m.cycle)
      changes.push(!m.cycle ? t('removed the issue from its cycle') : cycle ? phrase('added to cycle {name}', { name: cycle.name }) : t('changed the cycle'))
    }
    if ('labels' in m) {
      const ids = m.labels.split(',').filter(Boolean)
      const names = ids.map(id => context?.labels.find(label => label.id === id)?.name).filter(Boolean)
      changes.push(!ids.length ? t('removed all labels') : names.length === ids.length ? phrase('changed labels to {labels}', { labels: names.join(', ') }) : t('changed the labels'))
    }
    if ('dueDate' in m) changes.push(m.dueDate ? phrase('set the due date to {date}', { date: m.dueDate }) : t('removed the due date'))
    if ('estimate' in m) changes.push(Number(m.estimate) ? phrase('set the estimate to {estimate}', { estimate: m.estimate }) : t('removed the estimate'))
    if ('parent' in m) {
      const parent = context?.issues.find(issue => issue.id === m.parent)
      changes.push(!m.parent ? t('removed the parent issue') : parent ? phrase('set the parent issue to {issue}', { issue: parent.identifier }) : t('changed the parent issue'))
    }
    if ('archived' in m) changes.push(t(m.archived === 'true' ? 'archived the issue' : 'restored the issue'))
    if ('recurrence' in m || 'nextOccurrenceAt' in m) changes.push(t('changed the recurring schedule'))
    // Document snapshots, subscribers, ordering, and before-values are audit metadata.
    // They do not produce standalone entries in the visible issue activity feed.
    return changes.length ? changes.join(phrase(', ')) : null
  }
  if (event.type.startsWith('comment.') || event.type.includes('reaction') || event.type === 'issue.reacted') return null
  if (event.type === 'issue.releases_updated') {
    const changes = [m.added && phrase('added to release {name}', { name: m.added }), m.removed && phrase('removed from release {name}', { name: m.removed })].filter(Boolean)
    return changes.length ? changes.join(', ') : t('updated releases')
  }
  if (event.type === 'issue.review_linked' || event.type === 'issue.review_unlinked') return phrase(event.type.endsWith('unlinked') ? 'unlinked pull request {review}' : 'linked pull request {review}', { review: [m.repository, m.reviewNumber && `#${m.reviewNumber}`].filter(Boolean).join(' ') })
  if (event.type === 'issue.relation_added') {
    const target = context?.issues.find(issue => issue.id === m.relatedIssueId)
    const relation = ({ blocks: 'marked this as blocking {issue}', blocked_by: 'marked this as blocked by {issue}', duplicate: 'marked this as a duplicate of {issue}', parent_of: 'marked this as parent of {issue}', sub_issue_of: 'marked this as a sub-issue of {issue}' } as Record<string, string>)[m.type] ?? 'related this to {issue}'
    return phrase(relation, { issue: target?.identifier ?? t('another issue') })
  }
  if (event.type === 'issue.relation_removed') return t('removed an issue relation')
  if (event.type === 'attachment.created') return phrase('attached {name}', { name: m.title || t('a file') })
  if (event.type === 'attachment.deleted') return t('removed an attachment')
  return t('updated the issue')
}

export function activityTimeLabel(createdAt: string, now: number, locale: string) {
  const seconds = Math.max(0, (now - new Date(createdAt).getTime()) / 1000)
  if (seconds < 60) return locale === 'zh-CN' ? '刚刚' : 'just now'
  const [size, unit, short] = seconds < 3600 ? [60, 'minute', 'm'] : seconds < 86400 ? [3600, 'hour', 'h'] : seconds < 604800 ? [86400, 'day', 'd'] : seconds < 2592000 ? [604800, 'week', 'w'] : seconds < 31536000 ? [2592000, 'month', 'mo'] : [31536000, 'year', 'y']
  const count = Math.floor(seconds / Number(size))
  return locale === 'zh-CN' ? new Intl.RelativeTimeFormat(locale, { numeric: 'always', style: 'short' }).format(-count, unit as Intl.RelativeTimeFormatUnit) : `${count}${short} ago`
}

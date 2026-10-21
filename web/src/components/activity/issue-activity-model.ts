import type { ActivityEvent, BootstrapData } from '@/types/flow'
import { isAgentBot, sameActivityActor } from '@/lib/actor-attribution'

export type ActivityContext = Pick<BootstrapData, 'users' | 'projects' | 'labels' | 'cycles' | 'issues' | 'states'> & Partial<Pick<BootstrapData, 'releases' | 'reviews'>>
type Translate = (text: string) => string

/** A resource an event names: rendered as the same chip as a mention (hover card, navigation, fetch-by-id for issues / projects the client does not hold). */
export type ActivityRef = { type: 'issue' | 'project' | 'milestone' | 'cycle' | 'label' | 'release' | 'review' | 'initiative' | 'document' | 'customer'; id: string; label: string }
export type ActivityPart = string | ActivityRef
type PhraseValue = string | ActivityRef | ActivityRef[]

/** The plain sentence of an event's parts (what notifications, exports and tests read). */
export function activityPartsText(parts: ActivityPart[]) {
  return parts.map(part => typeof part === 'string' ? part : part.label).join('')
}

/** The word before the last of several changes: " and " ("…, … and …"); zh-CN "，并". */
export function lastChangeSeparator(t: Translate): string {
  return t('{changes} and {last}').match(/^\{changes\}([\s\S]*)\{last\}$/)?.[1] ?? ' and '
}

/** The separator between changes: ", " (zh-CN "，"). */
export function changeSeparator(t: Translate): string {
  return t('{changes}, {next}').match(/^\{changes\}([\s\S]*)\{next\}$/)?.[1] ?? ', '
}

function joinParts(groups: ActivityPart[][], separator: string, last = separator): ActivityPart[] {
  return groups.flatMap((group, index) => index ? [index === groups.length - 1 ? last : separator, ...group] : group)
}

function mergeText(parts: ActivityPart[]): ActivityPart[] {
  const merged: ActivityPart[] = []
  for (const part of parts) {
    if (part === '') continue
    if (typeof part === 'string' && typeof merged.at(-1) === 'string') merged[merged.length - 1] = `${merged.at(-1)}${part}`
    else merged.push(part)
  }
  return merged
}

/** An event's sentence as text and resource references. Returns null for events the visible history leaves out. */
export function describeIssueActivityParts(event: ActivityEvent, context?: ActivityContext, t: Translate = text => text): ActivityPart[] | null {
  const m = event.metadata ?? {}
  const phrase = (template: string, values: Record<string, PhraseValue> = {}): ActivityPart[] => {
    const parts: ActivityPart[] = []
    const source = t(template)
    let last = 0
    for (const match of source.matchAll(/\{(\w+)\}/g)) {
      parts.push(source.slice(last, match.index))
      const value = values[match[1]]
      if (Array.isArray(value)) parts.push(...joinParts(value.map(item => [item]), ', '))
      else if (value !== undefined) parts.push(value)
      last = match.index + match[0].length
    }
    parts.push(source.slice(last))
    return parts
  }
  const text = (value: string): ActivityPart[] => [value]
  const person = (id: string) => context?.users.find(user => user.id === id)?.displayName
  const issueRef = (id: string): ActivityRef => {
    const issue = context?.issues.find(item => item.id === id || item.identifier === id)
    return { type: 'issue', id: issue?.id ?? id, label: issue?.identifier ?? t('another issue') }
  }
  if (event.type === 'issue.created') {
    if (!m.recurringFrom) return text(t('created the issue'))
    return mergeText(phrase('created the issue from the recurring schedule of {issue}', { issue: { type: 'issue', id: m.recurringFromId || m.recurringFrom, label: m.recurringFrom } }))
  }
  if (event.type === 'issue.updated') {
    const changes: ActivityPart[][] = []
    if (m.state) changes.push(m.stateBefore ? phrase('moved from {from} to {to}', { from: t(m.stateBefore), to: t(m.state) }) : phrase('moved to {state}', { state: t(m.state) }))
    if (m.title) changes.push(phrase('changed the title to {title}', { title: m.title }))
    if (m.priority) changes.push(phrase('set priority to {priority}', { priority: t(m.priority) }))
    // Flow's agent acts as the person it works for: "assigned the issue to Dev User", never "to themselves".
    if ('assignee' in m) changes.push(text(!m.assignee ? t('removed the assignee') : m.assignee === event.actor.id && !isAgentBot(event.botActor) ? t('assigned the issue to themselves') : person(m.assignee) ? activityPartsText(phrase('assigned the issue to {name}', { name: person(m.assignee)! })) : t('changed the assignee')))
    // A closed pull request unassigns the agent: "GitHub unassigned Flow".
    if ('delegate' in m) changes.push(text(!m.delegate && m.previousDelegate && person(m.previousDelegate) ? activityPartsText(phrase('unassigned {name}', { name: person(m.previousDelegate)! })) : !m.delegate ? t('removed the delegate') : person(m.delegate) ? activityPartsText(phrase('delegated the issue to {name}', { name: person(m.delegate)! })) : t('changed the delegate')))
    if ('project' in m) {
      const project = context?.projects.find(item => item.id === m.project)
      changes.push(!m.project ? text(t('removed the issue from its project')) : phrase('added to project {name}', { name: { type: 'project', id: m.project, label: project?.name ?? t('another project') } }))
    }
    if ('projectMilestone' in m) {
      const milestone = context?.projects.flatMap(item => item.milestones ?? []).find(item => item.id === m.projectMilestone)
      changes.push(!m.projectMilestone ? text(t('removed the milestone')) : milestone ? phrase('added to milestone {name}', { name: { type: 'milestone', id: milestone.id, label: milestone.name } }) : text(t('changed the milestone')))
    }
    if ('cycle' in m) {
      const cycle = context?.cycles.find(item => item.id === m.cycle)
      changes.push(!m.cycle ? text(t('removed the issue from its cycle')) : cycle ? phrase('added to cycle {name}', { name: { type: 'cycle', id: cycle.id, label: cycle.name || `${t('Cycle')} ${cycle.number}` } }) : text(t('changed the cycle')))
    }
    if ('labels' in m) {
      const ids = m.labels.split(',').filter(Boolean)
      const labels = ids.flatMap(id => { const label = context?.labels.find(item => item.id === id); return label ? [{ type: 'label', id: label.id, label: label.name } as ActivityRef] : [] })
      changes.push(!ids.length ? text(t('removed all labels')) : labels.length === ids.length ? phrase('changed labels to {labels}', { labels }) : text(t('changed the labels')))
    }
    if ('dueDate' in m) changes.push(m.dueDate ? phrase('set the due date to {date}', { date: m.dueDate }) : text(t('removed the due date')))
    if ('estimate' in m) changes.push(Number(m.estimate) ? phrase('set the estimate to {estimate}', { estimate: m.estimate }) : text(t('removed the estimate')))
    if ('parent' in m) changes.push(!m.parent ? text(t('removed the parent issue')) : phrase('set the parent issue to {issue}', { issue: issueRef(m.parent) }))
    if ('archived' in m) changes.push(text(t(m.archived === 'true' ? 'archived the issue' : 'restored the issue')))
    if (m.recurringNext) changes.push(phrase('moved the recurring schedule to {issue}', { issue: { type: 'issue', id: m.recurringNextId || m.recurringNext, label: m.recurringNext } }))
    else if ('recurrence' in m && !m.recurrence) changes.push(text(t('stopped the recurring schedule')))
    else if ('recurrence' in m || 'nextOccurrenceAt' in m) changes.push(text(t('changed the recurring schedule')))
    // Document snapshots, subscribers, ordering, and before-values are audit metadata.
    // They do not produce standalone entries in the visible issue activity feed.
    // Like the reference app: "set priority to Urgent and set the estimate to 3", "…, … and …".
    return changes.length ? mergeText(joinParts(changes, changeSeparator(t), lastChangeSeparator(t))) : null
  }
  if (event.type.startsWith('comment.') || event.type.includes('reaction') || event.type === 'issue.reacted') return null
  if (event.type === 'issue.releases_updated') {
    const releases = (names: string): PhraseValue => {
      const items = names.split(', ').filter(Boolean).map(name => (context?.releases ?? []).find(item => item.name === name))
      return items.length && items.every(Boolean) ? items.map(item => ({ type: 'release', id: item!.id, label: item!.name }) as ActivityRef) : names
    }
    const changes: ActivityPart[][] = []
    if (m.added) changes.push(phrase('added to release {name}', { name: releases(m.added) }))
    if (m.removed) changes.push(phrase('removed from release {name}', { name: releases(m.removed) }))
    return changes.length ? mergeText(joinParts(changes, ', ')) : text(t('updated releases'))
  }
  if (event.type === 'issue.review_linked' || event.type === 'issue.review_unlinked') {
    const review = (context?.reviews ?? []).find(item => item.id === m.reviewId)
    const label = [m.repository, m.reviewNumber && `#${m.reviewNumber}`].filter(Boolean).join(' ')
    return mergeText(phrase(event.type.endsWith('unlinked') ? 'unlinked pull request {review}' : 'linked pull request {review}', { review: review ? { type: 'review', id: review.id, label: review.title } : label }))
  }
  // Without an actor when the code host's closed pull request archived it ("Session with Flow archived"); a member who
  // archived it is named ("<member> archived the session with Flow").
  if (event.type === 'issue.coding_session_archived') return text(activityPartsText(phrase(m.archivedBy ? 'archived the session with {name}' : 'Session with {name} archived', { name: (m.agentId && person(m.agentId)) || 'Flow' })))
  if (event.type === 'issue.preview_ready') return text(activityPartsText(phrase('deployed preview {environment}', { environment: m.environment || 'Preview' })))
  if (event.type === 'issue.relation_added') {
    const relation = ({ blocks: 'marked this as blocking {issue}', blocked_by: 'marked this as blocked by {issue}', duplicate: 'marked this as a duplicate of {issue}', parent_of: 'marked this as parent of {issue}', sub_issue_of: 'marked this as a sub-issue of {issue}' } as Record<string, string>)[m.type] ?? 'related this to {issue}'
    return mergeText(phrase(relation, { issue: m.relatedIssueId ? issueRef(m.relatedIssueId) : t('another issue') }))
  }
  if (event.type === 'issue.relation_removed') return text(t('removed an issue relation'))
  if (event.type === 'attachment.created') return text(activityPartsText(phrase('attached {name}', { name: m.title || t('a file') })))
  if (event.type === 'attachment.deleted') return text(t('removed an attachment'))
  return text(t('updated the issue'))
}

export function describeIssueActivity(event: ActivityEvent, context?: ActivityContext, t: Translate = text => text): string | null {
  const parts = describeIssueActivityParts(event, context, t)
  return parts ? activityPartsText(parts) : null
}

export function activityTimeLabel(createdAt: string, now: number, locale: string) {
  const seconds = Math.max(0, (now - new Date(createdAt).getTime()) / 1000)
  if (seconds < 60) return locale === 'zh-CN' ? '刚刚' : 'just now'
  const [size, unit, short] = seconds < 3600 ? [60, 'minute', 'm'] : seconds < 86400 ? [3600, 'hour', 'h'] : seconds < 604800 ? [86400, 'day', 'd'] : seconds < 2592000 ? [604800, 'week', 'w'] : seconds < 31536000 ? [2592000, 'month', 'mo'] : [31536000, 'year', 'y']
  const count = Math.floor(seconds / Number(size))
  return locale === 'zh-CN' ? new Intl.RelativeTimeFormat(locale, { numeric: 'always', style: 'short' }).format(-count, unit as Intl.RelativeTimeFormatUnit) : `${count}${short} ago`
}

/** Field families an `issue.updated` event can change (a family's before / id / type keys travel with it). */
const ISSUE_FIELD_KEYS = ['state', 'title', 'priority', 'assignee', 'delegate', 'project', 'projectMilestone', 'cycle', 'labels', 'dueDate', 'estimate', 'parent', 'archived', 'recurrence', 'recurringNext', 'nextOccurrenceAt']

/** How many fields an `issue.updated` event changed. */
export function issueEventFieldCount(event: ActivityEvent): number {
  const metadata = event.metadata ?? {}
  return ISSUE_FIELD_KEYS.filter(key => key in metadata).length
}

/** Consecutive changes by the same actor this close together read as one history row (the reference app's coalesced rows). */
export const ACTIVITY_COALESCE_WINDOW_MS = 2 * 60_000

/**
 * Whether `next` can join `previous` in one row: both are field changes by the same actor and attribution
 * (a person, an application, or Flow's agent for the same person), close together, of the same automation,
 * and they change different fields (changing one field twice stays two rows).
 */
export function canCoalesceIssueEvents(previous: ActivityEvent, next: ActivityEvent): boolean {
  if (previous.type !== 'issue.updated' || next.type !== 'issue.updated' || !sameActivityActor(previous, next)) return false
  const gap = new Date(next.createdAt).getTime() - new Date(previous.createdAt).getTime()
  if (!(gap >= 0 && gap <= ACTIVITY_COALESCE_WINDOW_MS)) return false
  const before = previous.metadata ?? {}
  const after = next.metadata ?? {}
  if ((before.automation ?? '') !== (after.automation ?? '')) return false
  const fields = (metadata: Record<string, string>) => ISSUE_FIELD_KEYS.filter(key => key in metadata)
  const changed = fields(before)
  const adding = fields(after)
  return changed.length > 0 && adding.length > 0 && !adding.some(key => changed.includes(key))
}

/** One event for both: the first one's id and position, every field either changed. */
export function mergeIssueEvents(previous: ActivityEvent, next: ActivityEvent): ActivityEvent {
  return { ...previous, metadata: { ...previous.metadata, ...next.metadata } }
}

/**
 * Folds consecutive field changes into single rows; `events` must be in time order. `canJoin` can keep rows apart
 * (the timeline keeps a comment posted between two changes between them).
 */
export function coalesceIssueEvents(events: ActivityEvent[], canJoin: (previous: ActivityEvent, next: ActivityEvent) => boolean = () => true): ActivityEvent[] {
  const result: ActivityEvent[] = []
  for (const event of events) {
    const last = result.at(-1)
    if (last && canCoalesceIssueEvents(last, event) && canJoin(last, event)) result[result.length - 1] = mergeIssueEvents(last, event)
    else result.push(event)
  }
  return result
}

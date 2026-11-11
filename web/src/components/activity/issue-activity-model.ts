import { format, formatDistance } from 'date-fns'
import { zhCN } from 'date-fns/locale'
import type { ActivityEvent, BootstrapData } from '@/types/flow'
import { isAgentBot } from '@/lib/actor-attribution'

export type ActivityContext = Pick<BootstrapData, 'users' | 'projects' | 'labels' | 'cycles' | 'issues' | 'states' | 'workspace'> & Partial<Pick<BootstrapData, 'releases' | 'reviews' | 'teams'>>
type Translate = (text: string) => string
export const ACTIVITY_COALESCE_WINDOW_MS = 2 * 60_000
export type ActivityRef = { type: 'issue' | 'project' | 'milestone' | 'cycle' | 'label' | 'release' | 'review' | 'initiative' | 'document' | 'customer'; id: string; label: string }
export type ActivityValue = { type: 'value'; label: string; title?: string; href?: string }
export type ActivityPart = string | ActivityRef | ActivityValue
export type IssueActivityEntry = ActivityEvent & { recordId: string; entryType: string; description: ActivityPart[]; fields?: string[]; actorless?: boolean; tooltip?: string; members?: IssueActivityEntry[] }

export function activityPartsText(parts: ActivityPart[]) {
  return parts.map(part => typeof part === 'string' ? part : part.label).join('')
}
export function lastChangeSeparator(t: Translate) { return t('{changes} and {last}').match(/^\{changes\}([\s\S]*)\{last\}$/)?.[1] ?? ' and ' }
export function changeSeparator(t: Translate) { return t('{changes}, {next}').match(/^\{changes\}([\s\S]*)\{next\}$/)?.[1] ?? ', ' }
export function joinActivityParts(groups: ActivityPart[][], t: Translate = text => text): ActivityPart[] {
  return groups.flatMap((group, i) => i ? [i === groups.length - 1 ? lastChangeSeparator(t) : changeSeparator(t), ...group] : group)
}
export function activityPhrase(template: string, values: Record<string, ActivityPart | ActivityPart[]> = {}, t: Translate = text => text): ActivityPart[] {
  return t(template).split(/(\{\w+\})/).flatMap(part => part.startsWith('{') ? [values[part.slice(1, -1)] ?? part].flat() : [part]).filter(part => part !== '')
}
const value = (label: string, title?: string): ActivityValue => ({ type: 'value', label, ...(title ? { title } : {}) })
const truncate = (text: string, length: number) => text.length > length ? `${text.slice(0, length)}…` : text

/** Accept the contract's arrays and JSON-encoded arrays from older metadata readers. */
export function activityIds(input: unknown): string[] {
  if (Array.isArray(input)) return input.filter((id): id is string => typeof id === 'string')
  if (typeof input !== 'string' || !input) return []
  if (input.startsWith('[')) { try { return activityIds(JSON.parse(input)) } catch { return [] } }
  return input.split(',').map(id => id.trim()).filter(Boolean)
}

/** Expand one server record. Only the seven small-change families share sentences. */
export function issueActivityEntries(event: ActivityEvent, context?: ActivityContext, t: Translate = text => text, year = new Date().getFullYear()): IssueActivityEntry[] {
  const m = event.metadata ?? {}
  const rows: IssueActivityEntry[] = []
  const phrase = (text: string, values?: Record<string, ActivityPart | ActivityPart[]>) => activityPhrase(text, values, t)
  const emit = (entryType: string, description: ActivityPart[], extra: Partial<IssueActivityEntry> = {}) => rows.push({ ...event, id: rows.length ? `${event.id}-${entryType}${rows.some(row => row.entryType === entryType) ? `-${rows.filter(row => row.entryType === entryType).length + 1}` : ''}` : event.id, recordId: event.id, entryType, description, ...extra })
  const person = (id: string) => context?.users.find(user => user.id === id)?.displayName ?? t('another user')
  const ref = (type: ActivityRef['type'], id: string): ActivityRef => {
    const collection = type === 'project' ? context?.projects : type === 'cycle' ? context?.cycles : type === 'label' ? context?.labels : type === 'milestone' ? context?.projects.flatMap(project => project.milestones ?? []) : undefined
    return { type, id, label: collection?.find(item => item.id === id)?.name || t(type === 'project' ? 'another project' : type === 'cycle' ? 'another cycle' : type === 'label' ? 'another label' : 'another milestone') }
  }
  const issue = (id: string): ActivityRef => { const target = context?.issues.find(item => item.id === id || item.identifier === id); return { type: 'issue', id: target?.id ?? id, label: target?.identifier ?? t('another issue') } }
  const has = (key: string) => key in m && (!(key + 'Before' in m) || m[key] !== m[key + 'Before'])
  const projectWord = (name: string) => /project/i.test(name) ? '' : t('project') + ' '
  const date = (input: string) => { const parsed = new Date(`${input.slice(0, 10)}T12:00:00`); return Number.isFinite(+parsed) ? format(parsed, parsed.getFullYear() === year ? 'MMM d' : 'MMM d, yyyy') : input }
  if (event.type === 'issue.created') {
    const description = m.templateName ? phrase('created the {template}', { template: value(m.templateName) })
      : m.recurringFrom ? phrase('created as part of a {series}', { series: value(t('recurring series'), m.recurrence) })
      : m.importer ? phrase('created in {importer}', { importer: value(m.importer) })
      : m.email ? [t('created via email')]
      : m.sourceReview ? phrase('created from a review comment on {review}', { review: value(m.sourceReview) })
      : m.sourceComment ? [t('created from a comment')]
      : m.sourceThread ? [t('created from a thread')]
      : m.integration ? phrase('created from {integration}', { integration: value(m.integration) })
      : m.addedBy ? phrase('created, added by {name}', { name: value(person(m.addedBy)) }) : [t('created')]
    emit('created', description)
  } else if (event.type === 'issue.updated') {
    const small: { type: string; parts: ActivityPart[] }[] = []
    const add = (type: string, parts: ActivityPart[]) => small.push({ type, parts })
    if (has('delegate')) add('delegate', !m.delegate ? phrase('unassigned {name}', { name: value(person(m.delegateBefore || m.previousDelegate)) }) : m.delegate === event.actor?.id ? [t('self-delegated the issue')] : phrase('delegated to {name}', { name: value(person(m.delegate)) }))
    if (has('assignee')) add('assign', !m.assignee ? [t('removed assignee')] : m.assignee === event.actor?.id && !isAgentBot(event.botActor) ? [t('self-assigned the issue')] : phrase(m.assigneeBefore ? 're-assigned to {name}' : 'assigned to {name}', { name: value(person(m.assignee)) }))
    if (has('project')) {
      const from = ref('project', m.projectBefore), to = ref('project', m.project)
      add('project', !m.project ? m.projectBefore ? phrase('removed from {word}{name}', { word: projectWord(from.label), name: from }) : [t('removed the issue from its project')] : m.projectBefore ? phrase('changed project from {from} to {to}', { from, to }) : phrase('added to {word}{name}', { word: projectWord(to.label), name: to }))
    }
    if (has('priority')) {
      const priorities = ['No priority', 'Urgent', 'High', 'Medium', 'Low']
      const rank = (input: string) => /^\d$/.test(input) ? Number(input) : priorities.indexOf(input)
      const before = rank(m.priorityBefore), after = rank(m.priority)
      add('priority', after === 0 || !m.priority ? [t('removed priority')] : phrase(before > 0 ? after < before ? 'raised priority from {from} to {to}' : 'lowered priority from {from} to {to}' : 'set priority to {to}', { from: value(t(priorities[before] ?? m.priorityBefore)), to: value(t(priorities[after] ?? m.priority)) }))
    }
    if (has('estimate')) {
      const points = (input: string) => value(/^\d+(\.\d+)?$/.test(input) ? t(Number(input) === 1 ? '{count} point' : '{count} points').replace('{count}', input) : input)
      add('estimate', !m.estimate || m.estimate === '0' ? [t('removed estimate')] : phrase(Number(m.estimateBefore) > 0 ? Number(m.estimate) > Number(m.estimateBefore) ? 'increased estimate from {from} to {to}' : 'lowered estimate from {from} to {to}' : 'estimated complexity as {to}', { from: points(m.estimateBefore ?? ''), to: points(m.estimate) }))
    }
    const added = activityIds(m.addedLabelIds), removed = activityIds(m.removedLabelIds)
    for (const [type, ids, limit] of [['label-added', added.filter(id => !removed.includes(id)), 5], ['label-removed', removed.filter(id => !added.includes(id)), 3]] as const) {
      if (!ids.length) continue
      const labels = ids.map(id => ref('label', id))
      const names = labels.length <= limit ? joinActivityParts(labels.map(label => [label]), t) : phrase('{count} labels', { count: String(labels.length) })
      add(type, phrase(type === 'label-added' ? ids.length === 1 ? 'added label {labels}' : 'added labels {labels}' : ids.length === 1 ? 'removed label {labels}' : 'removed labels {labels}', { labels: names }))
    }
    if ('labels' in m && !('addedLabelIds' in m) && !('removedLabelIds' in m)) {
      const labels = activityIds(m.labels).map(id => ref('label', id))
      add('label-added', labels.length ? phrase('changed labels to {labels}', { labels: joinActivityParts(labels.map(label => [label]), t) }) : [t('removed all labels')])
    }
    const emitSmall = () => {
      for (let index = 0; index < small.length; index += index === 4 ? 3 : 2) {
        const clauses = small.slice(index, index + (index === 4 ? 3 : 2))
        emit(clauses.length === 1 ? clauses[0].type : clauses.every(clause => clause.type.startsWith('label-')) ? 'labels' : 'aggregate', joinActivityParts(clauses.map(clause => clause.parts), t), { fields: clauses.map(clause => clause.type) })
      }
    }
    if (has('delegate')) emitSmall()
    if (has('team')) emit('team', phrase(m.teamBeforeName && m.teamName ? 'changed team from {from} to {to}' : 'changed team to {to}', { from: value(m.teamBeforeName || context?.teams?.find(team => team.id === m.teamBefore)?.name || t('another team')), to: value(m.teamName || context?.teams?.find(team => team.id === m.team)?.name || t('another team')) }), { tooltip: m.identifierBefore && m.identifier ? t('Changed identifier from {from} to {to}').replace('{from}', m.identifierBefore).replace('{to}', m.identifier) : undefined })
    if (has('state') && m.stateType !== 'duplicate' && m.viaDuplicate !== 'true') emit('state-change', [...phrase(m.stateBefore ? 'moved from {from} to {to}' : 'moved to {to}', { from: value(t(m.stateBefore)), to: value(t(m.state)) }), ...(['autoClose', 'autoCloseStale'].includes(m.automation) ? [' ', t('due to inactivity')] : [])], { metadata: { ...m, stateBeforeType: m.stateBeforeType || context?.states.find(state => state.name === m.stateBefore)?.type || '' } })
    if (has('cycle')) {
      const from = ref('cycle', m.cycleBefore), to = ref('cycle', m.cycle)
      const cycleName = m.cycle ? to : from
      emit('cycle', phrase(!m.cycle ? 'removed issue from {cycle}' : m.cycleBefore ? 'moved issue to {cycle}' : 'added issue to {cycle}', { cycle: [/cycle/i.test(cycleName.label) ? '' : `${t('cycle')} `, cycleName] }))
    }
    if (!has('delegate')) emitSmall()
    if (has('projectMilestone')) {
      const from = ref('milestone', m.projectMilestoneBefore), to = ref('milestone', m.projectMilestone)
      emit('milestone', phrase(!m.projectMilestone ? 'removed from milestone {name} in project {project}' : m.projectMilestoneBefore ? 'changed milestone from {from} to {to}' : 'added to milestone {name} in project {project}', { from, to, name: m.projectMilestone ? to : from, project: ref('project', m.project || m.projectBefore || context?.projects.find(project => project.milestones?.some(item => item.id === (m.projectMilestone || m.projectMilestoneBefore)))?.id || '') }))
    }
    if (has('title')) emit('title-change', phrase('changed title to {title}', { title: value(truncate(m.title, 70), m.titleBefore ? t('Previously: {title}').replace('{title}', m.titleBefore) : undefined) }))
    if (has('sla')) emit('sla', !m.sla ? [t('removed the SLA')] : phrase(m.slaReset === 'true' ? 'reset SLA to {to}' : m.slaBefore ? 'changed the SLA from {from} to {to}' : 'set the SLA to {to}', { from: value(m.slaBefore), to: value(m.sla) }))
    if (has('parent')) emit('parent', phrase(!m.parent ? m.parentBefore ? 'removed parent {issue}' : 'removed the parent issue' : m.parentBefore ? 'changed parent issue from {from} to {issue}' : 'set parent to {issue}', { from: issue(m.parentBefore), issue: issue(m.parent || m.parentBefore) }))
    if (has('dueDate')) emit('due-date', !m.dueDate ? [t('removed due date')] : phrase(m.dueDateBefore ? 'changed the due date from {from} to {to}' : 'set the due date to {to}', { from: date(m.dueDateBefore ?? ''), to: date(m.dueDate) }))
    if (has('archived')) emit('archive', [t(m.archived === 'true' ? 'archived the issue' : 'restored the issue')])
    if (has('trashed')) emit('trash', [t(m.trashed === 'true' ? 'deleted the issue' : 'restored the issue')])
    if (m.recurringNext) emit('recurrence', phrase('moved the recurring schedule to {issue}', { issue: { type: 'issue', id: m.recurringNextId || m.recurringNext, label: m.recurringNext } }))
    else if ('recurrence' in m || 'nextOccurrenceAt' in m) emit('recurrence', [t(!m.recurrence ? 'stopped the recurring schedule' : 'changed the recurring schedule')])
  } else if (event.type === 'issue.sla_breached') emit('sla-breached', [t('SLA breached')], { actorless: true })
  else if (event.type === 'issue.archived' || event.type === 'issue.restored' || event.type === 'issue.deleted') emit('archive', [t(event.type === 'issue.archived' ? 'archived the issue' : event.type === 'issue.deleted' ? 'deleted the issue' : 'restored the issue')])
  else if (event.type === 'issue.imported') emit('import', m.importer ? phrase('imported issue from {importer}', { importer: value(m.importer) }) : [t('imported issue')])
  else if (event.type === 'issue.loop_run_created') emit('loop', phrase('{loop} is waiting to run…', { loop: value(m.loopName || t('Loop')) }), { actorless: true })
  else if (event.type.startsWith('comment.') || event.type.includes('reaction') || event.type === 'issue.reacted') return []
  else if (event.type.startsWith('issue.relation_')) {
    let type = m.relationType || m.type || 'related'
    if (m.direction === 'inverse') type = ({ blocks: 'blocked_by', blocked_by: 'blocks', duplicate: 'has_duplicate', parent_of: 'sub_issue_of', sub_issue_of: 'parent_of' } as Record<string, string>)[type] || type
    if (type === 'parent_of') return []
    const adding = event.type === 'issue.relation_added'
    const templates: Record<string, [string, string]> = {
      blocks: ['marked this issue as blocking {issue}', 'unmarked this issue as blocking {issue}'],
      blocked_by: ['marked this issue as blocked by {issue}', 'unmarked this issue as being blocked by {issue}'],
      related: ['added related issue {issue}', 'removed related issue {issue}'],
      duplicate: ['marked this issue as a {duplicate} of {issue}', 'unmarked this issue as a {duplicate} of {issue}'],
      has_duplicate: ['marked {issue} as a {duplicate} of this issue', 'unmarked {issue} as a {duplicate} of this issue'],
      sub_issue_of: ['set parent to {issue}', 'removed parent {issue}'],
    }
    emit(type === 'related' ? 'related-issue' : type.includes('duplicate') ? 'duplicate-issue' : type, phrase((templates[type] ?? templates.related)[adding ? 0 : 1], { issue: issue(m.relatedIssueId), duplicate: value(t('duplicate')) }))
  } else if (event.type === 'attachment.created') emit('attachment', phrase('linked {link}', { link: { type: 'value', label: truncate(m.linkTitle || m.title || t('a file'), 77), href: m.linkUrl || m.url } }))
  else if (event.type === 'attachment.deleted') emit('attachment', [t('removed an attachment')])
  else if (event.type === 'issue.releases_updated') {
    const parts: ActivityPart[][] = []
    for (const key of ['added', 'removed']) if (m[key]) parts.push(phrase(key === 'added' ? 'added to release {name}' : 'removed from release {name}', { name: value(m[key]) }))
    emit('release', parts.length ? joinActivityParts(parts, t) : [t('updated releases')])
  } else if (event.type === 'issue.review_linked' || event.type === 'issue.review_unlinked') {
    const review = context?.reviews?.find(item => item.id === m.reviewId)
    emit('review', phrase(event.type.endsWith('unlinked') ? 'unlinked pull request {review}' : 'linked pull request {review}', { review: review ? { type: 'review', id: review.id, label: review.title } : value([m.repository, m.reviewNumber && `#${m.reviewNumber}`].filter(Boolean).join(' ')) }))
  } else if (event.type === 'issue.coding_session_archived') emit('session', phrase(m.archivedBy ? 'archived the session with {name}' : 'Session with {name} archived', { name: value(m.agentId ? person(m.agentId) : 'Flow') }), { actorless: !m.archivedBy })
  else if (event.type === 'issue.preview_ready') emit('preview', phrase('deployed preview {environment}', { environment: m.environment || 'Preview' }))
  else emit('other', [t('updated the issue')])
  return rows
}

export function describeIssueActivityParts(event: ActivityEvent, context?: ActivityContext, t: Translate = text => text): ActivityPart[] | null {
  const rows = issueActivityEntries(event, context, t)
  return rows.length ? joinActivityParts(rows.map(row => row.description), t) : null
}
export function describeIssueActivity(event: ActivityEvent, context?: ActivityContext, t: Translate = text => text): string | null {
  const parts = describeIssueActivityParts(event, context, t)
  return parts ? activityPartsText(parts) : null
}

export function activityTimeLabel(createdAt: string, now: number, locale: string) {
  const seconds = Math.max(0, (now - Date.parse(createdAt)) / 1000)
  if (seconds < 60) return locale === 'zh-CN' ? '刚刚' : 'just now'
  const days = seconds / 86400
  let count: number, unit: Intl.RelativeTimeFormatUnit, short: string
  if (seconds < 3600) { count = Math.round(seconds / 60); unit = 'minute'; short = 'min' }
  else if (seconds < 8 * 3600) { count = Math.floor(seconds / 3600) + Number(seconds % 3600 >= 2400); unit = 'hour'; short = 'h' }
  else if (days < 1) { count = Math.round(seconds / 3600); unit = 'hour'; short = 'h' }
  else if (days < 5) { count = Math.floor(days) + Number(seconds % 86400 >= 57024); unit = 'day'; short = 'd' }
  else if (days < 30.4375 && Math.round(days) % 7 !== 0) { count = Math.round(days); unit = 'day'; short = 'd' }
  else if (days < 70) { count = Math.round(days / 7); unit = 'week'; short = 'w' }
  else if (days < 365.25) { count = Math.round(days / 30.4375); unit = 'month'; short = 'mo' }
  else { count = Math.floor(days / 365.25); unit = 'year'; short = 'y' }
  return locale === 'zh-CN' ? new Intl.RelativeTimeFormat(locale, { numeric: 'always', style: 'short' }).format(-count, unit) : `${count}${short} ago`
}

export function activityExpandedTimeLabel(createdAt: string, now: number, locale: string) {
  const yesterday = new Date(now)
  yesterday.setHours(0, 0, 0, 0)
  yesterday.setDate(yesterday.getDate() - 1)
  return Date.parse(createdAt) >= +yesterday ? formatDistance(new Date(createdAt), new Date(now), { addSuffix: true, locale: locale === 'zh-CN' ? zhCN : undefined }) : format(new Date(createdAt), new Date(createdAt).getFullYear() === new Date(now).getFullYear() ? 'MMM d' : 'MMM d, yyyy')
}

export type ActivitySessionCard = { id: string; createdAt: string; agentId?: string }
export function withoutSessionDelegations(events: ActivityEvent[], sessions: readonly ActivitySessionCard[]): ActivityEvent[] {
  if (!sessions.length) return events
  return events.flatMap(event => {
    const m = event.metadata
    if (event.type !== 'issue.updated' || !m?.delegate) return [event]
    const shown = m.agentSessionId ? sessions.some(session => session.id === m.agentSessionId) : sessions.some(session => session.agentId === m.delegate && Math.abs(Date.parse(session.createdAt) - Date.parse(event.createdAt)) <= 60_000)
    if (!shown) return [event]
    const { delegate: _delegate, agentSessionId: _session, ...rest } = m
    return issueActivityEntries({ ...event, metadata: rest }).length ? [{ ...event, metadata: rest }] : []
  })
}

import { sameActivityActor } from '@/lib/actor-attribution'
import { activityPhrase, changeSeparator, joinActivityParts, type ActivityRef, type IssueActivityEntry } from './issue-activity-model'

const families = new Set(['state-change', 'priority', 'label-added', 'label-removed', 'title-change', 'cycle', 'related-issue', 'duplicate-issue', 'attachment'])
const aggressive = new Set(['title-change', 'cycle', 'related-issue', 'duplicate-issue', 'attachment'])
const family = (row: IssueActivityEntry) => row.entryType.startsWith('label-') ? 'labels' : row.entryType
const sameActorOnly = new Set(['state-change', 'priority', 'labels'])

export function orderIssueActivityEntries(entries: IssueActivityEntry[]) {
  const records: IssueActivityEntry[][] = []
  for (const entry of entries) {
    const last = records.at(-1)
    if (last?.[0].recordId === entry.recordId) last.push(entry)
    else records.push([entry])
  }
  const result: IssueActivityEntry[] = []
  for (const record of records) {
    const type = result.at(-1)?.entryType
    result.push(...record.filter(entry => entry.entryType === type), ...record.filter(entry => entry.entryType !== type))
  }
  return result
}

/** Adjacency determines families. Older completed blocks also collect aggressive families. */
export function groupIssueActivityEntries(entries: IssueActivityEntry[], options: { now: number; last: boolean }, t: (text: string) => string = text => text): IssueActivityEntry[] {
  const yesterday = new Date(options.now)
  yesterday.setHours(0, 0, 0, 0)
  yesterday.setDate(yesterday.getDate() - 1)
  const collect = !options.last && entries.every(row => Date.parse(row.createdAt) < +yesterday)
  const result: IssueActivityEntry[] = []
  for (const entry of orderIssueActivityEntries(entries)) {
    const type = family(entry)
    const previous = collect && aggressive.has(type) ? result.findLast(row => family(row) === type) : result.at(-1)
    if (!previous || !families.has(entry.entryType) || family(previous) !== type || sameActorOnly.has(type) && !sameActivityActor(previous, entry)) { result.push(entry); continue }
    const members = [...(previous.members ?? [previous]), entry]
    if (entry.entryType === 'title-change' && sameActivityActor(previous, entry) && Date.parse(entry.createdAt) - Date.parse(previous.createdAt) <= 60_000 && !previous.members) {
      result[result.indexOf(previous)] = { ...entry, id: previous.id, recordId: previous.recordId, createdAt: previous.createdAt, metadata: { ...entry.metadata, titleBefore: previous.metadata.titleBefore }, description: entry.description.map(part => typeof part !== 'string' && part.type === 'value' && previous.metadata.titleBefore ? { ...part, title: t('Previously: {title}').replace('{title}', previous.metadata.titleBefore) } : part) }
      continue
    }
    const grouped = { ...entry, id: previous.id, recordId: previous.recordId, members, description: summarizeIssueActivity(members, t) }
    result.splice(result.indexOf(previous), 1)
    result.push(grouped)
  }
  return result
}

export function summarizeIssueActivity(rows: IssueActivityEntry[], t: (text: string) => string = text => text) {
  const first = rows[0], last = rows[rows.length - 1], m = last.metadata
  const phrase = (template: string, values: Parameters<typeof activityPhrase>[1] = {}) => activityPhrase(template, values, t)
  const bold = (label: string) => ({ type: 'value' as const, label: t(label) })
  const number = (n: number) => t(['zero', 'one', 'two', 'three', 'four', 'five'][n] ?? String(n))
  if (last.entryType === 'attachment') return phrase('added {links}', { links: bold(t('{count} links').replace('{count}', String(rows.length))) })
  if (last.entryType === 'title-change') return phrase(rows.length === 2 ? 'changed title twice' : 'changed title {count} times', { count: number(rows.length) })
  if (last.entryType.startsWith('label-')) {
    const labels = new Map<string, { part: ActivityRef; first: string; last: string }>()
    for (const row of rows) for (const part of row.description) {
      if (typeof part === 'string' || part.type !== 'label') continue
      const previous = labels.get(part.id)
      labels.set(part.id, { part, first: previous?.first ?? row.entryType, last: row.entryType })
    }
    return joinActivityParts([...labels.values()].map(label => phrase(label.first !== label.last ? label.first === 'label-added' ? 'added and then removed {label}' : 'removed and then added {label}' : label.last === 'label-added' ? 'added {label}' : 'removed {label}', { label: label.part })), t)
  }
  if (last.entryType === 'state-change') {
    const from = first.metadata.stateBefore, to = m.state
    if (rows.length === 2 || from === to) return phrase(from === to ? 'moved to {from}, then back to {to}' : 'moved to {from}, then to {to}', { from: bold(rows[rows.length - 2].metadata.state), to: bold(to) })
    const order: Record<string, number> = { triage: 0, backlog: 1, unstarted: 2, started: 3, completed: 4, canceled: 5 }
    return phrase((order[m.stateType] ?? 0) < (order[first.metadata.stateBeforeType] ?? 0) ? 'moved back from {from} to {to}' : 'moved from {from} to {to}', { from: bold(from || first.metadata.state), to: bold(to) })
  }
  if (last.entryType === 'priority') {
    const removed = !m.priority || m.priority === 'No priority' || m.priority === '0'
    const priority = ['No priority', 'Urgent', 'High', 'Medium', 'Low'][Number(m.priority)] ?? m.priority
    if (rows.length >= 3) return phrase(removed ? 'changed priority a few times before removing it' : 'changed priority a few times before setting it to {to}', { to: bold(priority) })
    return [...first.description, ...phrase(removed ? ', then removed priority' : ', then set to {to}', { to: bold(priority) })]
  }
  if (last.entryType === 'cycle') {
    const cycle = last.description.find(part => typeof part !== 'string' && part.type === 'cycle') ?? ''
    if (rows.length === 2) return [...first.description, ...phrase(m.cycle ? ' and moved to {cycle}' : ' and then removed it from {cycle}', { cycle })]
    return phrase(!m.cycle ? 'moved issue through {count} cycles and then removed it from {cycle}' : 'moved issue through {count} cycles to {cycle}', { count: number(rows.length), cycle })
  }
  if (last.entryType === 'related-issue' || last.entryType === 'duplicate-issue') {
    const added = rows.filter(row => row.type.endsWith('_added')), removed = rows.filter(row => row.type.endsWith('_removed'))
    const targets = rows.flatMap(row => row.description.filter((part): part is ActivityRef => typeof part !== 'string' && part.type === 'issue'))
    if (targets.length === rows.length && rows.length === 2 && first.metadata.relatedIssueId === m.relatedIssueId && added.length && removed.length) return phrase(first.type.endsWith('_added') ? '{issue} added and then removed from related issues' : '{issue} removed and then added to related issues', { issue: targets[0] })
    if (targets.length === rows.length && added.length === rows.length && rows.length <= 2) return phrase('{issues} added to related issues', { issues: joinActivityParts(targets.map(target => [target]), t) })
    return phrase(removed.length && added.length ? 'Added {added} related issues and removed {removed}' : added.length ? 'Added {count} related issues' : 'Removed {count} related issues', { count: number(rows.length), added: number(added.length), removed: number(removed.length) })
  }
  return joinActivityParts(rows.map(row => row.description), t)
}

export type ActivityCollapsePosition = 'only' | 'first' | 'middle' | 'last'
export function partitionIssueActivity(entries: IssueActivityEntry[], position: ActivityCollapsePosition, now: number, nextCommentDate?: string) {
  const comment = nextCommentDate ? Date.parse(nextCommentDate) : 0
  const recentAt = Math.min(now - 4 * 3600_000, comment >= now - 4 * 86400_000 ? comment - 5 * 60_000 : Infinity)
  const recentIndex = entries.findIndex(entry => Date.parse(entry.createdAt) >= recentAt)
  const recent = recentIndex < 0 ? 0 : entries.length - recentIndex
  const minimum = { only: 9, first: 5, last: 4, middle: 2 }[position]
  const before = position === 'first' || position === 'only' ? 3 : 0
  const after = Math.max(recent, position === 'only' ? 3 : position === 'last' ? 2 : 0)
  const end = Math.max(before, entries.length - after)
  return entries.length >= minimum && end - before >= 2 ? { before: entries.slice(0, before), hidden: entries.slice(before, end), after: entries.slice(end) } : { before: entries, hidden: [], after: [] }
}
export function activityCollapseReasons(entries: IssueActivityEntry[], t: (text: string) => string = text => text) {
  const reasons: Record<string, [string, number, string]> = {
    'sla-breached': ['breached', 0, 'SLA'],
    'state-change': ['changed', 0, 'status'], assign: ['changed', 1, 'assignee'], priority: ['changed', 2, 'priority'], project: ['changed', 3, 'project'], delegate: ['changed', 4, 'agent'],
    estimate: ['changed', 5, 'estimate'], team: ['changed', 6, 'team'], 'title-change': ['changed', 7, 'title'], 'due-date': ['changed', 10, 'due date'], sla: ['changed', 9, 'SLA'], milestone: ['changed', 12, 'milestone'], cycle: ['changed', 13, 'cycles'],
    'label-added': ['added', 8, 'labels'], 'label-removed': ['removed', 8, 'labels'], attachment: ['added', 9, 'links'], 'related-issue': ['changed', 14, 'related issues'], 'duplicate-issue': ['changed', 14, 'related issues'],
  }
  const present = [...new Set(entries.flatMap(entry => entry.fields ?? [entry.entryType]))].flatMap(field => reasons[field] ? [reasons[field]] : [])
  const order = ['breached', 'changed', 'added', 'removed']
  present.sort((a, b) => order.indexOf(a[0]) - order.indexOf(b[0]) || a[1] - b[1])
  const groups = new Map<string, string[]>()
  for (const [verb, , noun] of present.slice(0, 5)) groups.set(verb, [...(groups.get(verb) ?? []), t(noun)])
  const summary = [...groups].map(([verb, nouns]) => activityPhrase(`${verb} {fields}`, { fields: joinActivityParts(nouns.map(noun => [noun]), t) }, t)).flatMap((parts, index) => index ? [changeSeparator(t), ...parts] : parts)
  return summary.length ? activityPhrase(': {summary}…', { summary }, t).join('') : '…'
}
const expandedBlocks = new Map<string, number>()
export function activityBlockExpanded(key: string, now: number) { const until = expandedBlocks.get(key); return until !== undefined && until >= now }
export function expandActivityBlock(key: string, now: number) {
  expandedBlocks.delete(key)
  expandedBlocks.set(key, now + 15 * 60_000)
  if (expandedBlocks.size > 2000) expandedBlocks.delete(expandedBlocks.keys().next().value!)
}

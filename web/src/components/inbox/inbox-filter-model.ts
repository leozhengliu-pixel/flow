import type { Notification, User } from '@/types/flow'
import { personSearchText } from '@/lib/people'
import type { InboxFilterCondition, InboxFilterOption } from './inbox-filter-types'

export const UNMATCHED_OPTION_ID = '__inbox_unmatched__'

export function inboxNotificationCategory(notification: Pick<Notification, 'category' | 'type'>, fallback = notification.type) {
  const categories: Record<string, string> = { assignments: 'assignment', statusChanges: 'status', comments: 'comment', mentions: 'mention', reactions: 'reaction', subscriptions: 'subscription', documents: 'document', updates: 'project', reminders: 'reminder', loops: 'loop', integrations: 'apps', customerRequests: 'customerRequest', triage: 'triage' }
  return categories[notification.category] ?? fallback
}

export function partitionInboxOptions(options: InboxFilterOption[], itemCount: number, selectedIds: string[] = []) {
  const split = itemCount > 0 && options.length >= 10 && itemCount * options.length <= 10000 && options.every(option => option.count !== undefined)
  if (!split) return { matching: options, unmatched: [] as InboxFilterOption[] }
  const selected = new Set(selectedIds)
  return { matching: options.filter(option => (option.count ?? 0) > 0 || selected.has(option.id)), unmatched: options.filter(option => !(option.count ?? 0) && !selected.has(option.id)) }
}

// Match the menu's row-boundary clipping: leave 55% of the next row visible.
export function inboxValueMenuHeight(rows: number[], search: boolean, viewportHeight: number, anchorTop: number) {
  const header = search ? 37.5 : 1
  const full = header + 12 + rows.reduce((sum, row) => sum + row, 0)
  const below = viewportHeight - anchorTop - 16
  const available = below > viewportHeight * .5 ? below : viewportHeight - 32
  if (full <= available) return full
  const limit = Math.min(available, Math.floor(viewportHeight * .8), header + 6 + rows.slice(0, 18).reduce((sum, row) => sum + row, 0))
  let cursor = header + 6
  let height = Math.min(full, limit)
  for (const row of rows) {
    const boundary = Math.ceil(cursor + row * .55)
    if (boundary > limit) break
    height = boundary
    cursor += row
  }
  return Math.max(1, height)
}

export interface InboxFilterTarget {
  issueId: string
  notificationType: string
  actorId: string
  projectId?: string
  initiativeIds: string[]
  issuePriority: number
  issueStatusType: string
  reviewStatus?: string
}

export function matchesInboxFilter(notification: InboxFilterTarget, filter: InboxFilterCondition) {
  const values = new Set(filter.values.map(value => value.value))
  if (!values.size) return true
  let match = false
  if (filter.property === 'notificationType') match = values.has(notification.notificationType)
  if (filter.property === 'from') match = values.has(notification.actorId)
  if (filter.property === 'project') match = notification.projectId ? values.has(notification.projectId) : Boolean(notification.issueId) && values.has('__none__')
  if (filter.property === 'initiative') match = notification.initiativeIds.length ? notification.initiativeIds.some(id => values.has(id)) : values.has('__none__')
  if (filter.property === 'issuePriority') match = Boolean(notification.issueId) && values.has(String(notification.issuePriority))
  if (filter.property === 'issueStatusType') match = Boolean(notification.issueId) && values.has(notification.issueStatusType)
  if (filter.property === 'reviewStatus') match = Boolean(notification.reviewStatus) && values.has(notification.reviewStatus!)
  return filter.operator === 'is' ? match : !match
}

export function inboxActorOptions(notifications: Array<{ actorId: string; actor: string; actorAvatarUrl?: string }>, users: User[]): InboxFilterOption[] {
  const userById = new Map(users.map(user => [user.id, user]))
  const actors = new Map<string, InboxFilterOption>()
  for (const notification of notifications) {
    if (!notification.actorId || !notification.actor) continue
    const existing = actors.get(notification.actorId)
    if (existing) { existing.count = (existing.count ?? 0) + 1; continue }
    const person = userById.get(notification.actorId) ?? { id: notification.actorId, displayName: notification.actor, avatarUrl: notification.actorAvatarUrl }
    actors.set(notification.actorId, { id: person.id, label: person.displayName, avatarUrl: person.avatarUrl, person, keywords: personSearchText(person), count: 1, i18nIgnore: true })
  }
  return [...actors.values()].sort((a, b) => a.label.localeCompare(b.label))
}

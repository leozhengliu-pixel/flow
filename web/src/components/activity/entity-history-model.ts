/**
 * Project and initiative history rows from their audit history (GET /projects|initiatives/{id}/history):
 * "Dev User set priority to High", or for Flow's agent "Flow set priority to High on behalf of Dev User".
 * Consecutive property changes by one actor are folded into one row like issue history.
 */
import { sameActivityActor } from '@/lib/actor-attribution'
import type { AuditLogEntry } from '@/types/flow'
import { ACTIVITY_COALESCE_WINDOW_MS, changeSeparator, lastChangeSeparator } from './issue-activity-model'

type Translate = (text: string) => string
export type HistoryResource = 'project' | 'initiative'
export type HistoryChange = { field: string; from: string; to: string }

export function historyChanges(entry: Pick<AuditLogEntry, 'metadata'>): HistoryChange[] {
  const raw = entry.metadata?.changes
  if (!Array.isArray(raw)) return []
  return raw.flatMap(item => {
    if (!item || typeof item !== 'object') return []
    const { field, from, to } = item as Record<string, unknown>
    return typeof field === 'string' ? [{ field, from: typeof from === 'string' ? from : '', to: typeof to === 'string' ? to : '' }] : []
  })
}

/** Folds consecutive "updated" entries by the same actor (and attribution) that change different fields; time order in and out. */
export function coalesceHistoryEntries(entries: AuditLogEntry[]): AuditLogEntry[] {
  const result: AuditLogEntry[] = []
  for (const entry of [...entries].sort((a, b) => +new Date(a.createdAt) - +new Date(b.createdAt))) {
    const last = result.at(-1)
    const gap = last ? +new Date(entry.createdAt) - +new Date(last.createdAt) : Infinity
    if (last && last.action === 'updated' && entry.action === 'updated' && last.resourceId === entry.resourceId && gap <= ACTIVITY_COALESCE_WINDOW_MS && sameActivityActor(last, entry)) {
      const before = historyChanges(last)
      const after = historyChanges(entry)
      if (!after.some(change => before.some(item => item.field === change.field))) {
        result[result.length - 1] = { ...last, metadata: { ...last.metadata, changes: [...before, ...after] } }
        continue
      }
    }
    result.push(entry)
  }
  return result
}

const STATUS_LABELS: Record<string, string> = { proposed: 'Proposed', planned: 'Planned', active: 'Active', completed: 'Completed', canceled: 'Canceled' }

function phrase(t: Translate, template: string, values: Record<string, string>) {
  return t(template).replace(/\{(\w+)\}/g, (match, key: string) => values[key] ?? match)
}

function formatDay(value: string, locale: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return value
  return new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric' }).format(new Date(`${value}T00:00:00`))
}

function describeChange(change: HistoryChange, resource: HistoryResource, t: Translate, locale: string): string | null {
  const { field, to } = change
  switch (field) {
    case 'name': return phrase(t, resource === 'project' ? 'renamed the project to {name}' : 'renamed the initiative to {name}', { name: to })
    case 'status': return to ? phrase(t, 'changed the status to {status}', { status: t(STATUS_LABELS[to] ?? to) }) : null
    case 'priority': return to ? phrase(t, 'set priority to {priority}', { priority: t(to) }) : t('removed the priority')
    case 'lead': return to ? phrase(t, 'set the lead to {name}', { name: to }) : t('removed the lead')
    case 'owner': return to ? phrase(t, 'set the owner to {name}', { name: to }) : t('removed the owner')
    case 'startDate': return to ? phrase(t, 'set the start date to {date}', { date: formatDay(to, locale) }) : t('removed the start date')
    case 'targetDate': return to ? phrase(t, 'set the target date to {date}', { date: formatDay(to, locale) }) : t('removed the target date')
    case 'description': return t('updated the description')
    default: return null
  }
}

/** The action of a history entry, without its actor; null for entries the activity feed leaves out. */
export function describeHistoryEntry(entry: AuditLogEntry, resource: HistoryResource, t: Translate = text => text, locale = 'en'): string | null {
  const metadata = entry.metadata ?? {}
  const name = typeof metadata.milestoneName === 'string' ? metadata.milestoneName : ''
  switch (entry.action) {
    case 'created': return t(resource === 'project' ? 'created the project' : 'created the initiative')
    case 'milestone_created': return phrase(t, 'added milestone {name}', { name })
    case 'milestone_updated': return phrase(t, 'updated milestone {name}', { name })
    case 'milestone_deleted': return phrase(t, 'removed milestone {name}', { name })
    case 'updated': {
      const parts = historyChanges(entry).flatMap(change => describeChange(change, resource, t, locale) ?? [])
      if (!parts.length) return null
      return parts.length === 1 ? parts[0] : `${parts.slice(0, -1).join(changeSeparator(t))}${lastChangeSeparator(t)}${parts.at(-1)}`
    }
    default: return null
  }
}

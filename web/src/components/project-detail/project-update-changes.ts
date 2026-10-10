import type { AuditLogEntry, ProjectUpdate } from '@/types/flow'

export type ProjectPropertyChange = { field: string; from: string; to: string }

function historyChanges(entry: AuditLogEntry): ProjectPropertyChange[] {
  const raw = entry.metadata?.changes
  if (!Array.isArray(raw)) return []
  return raw.filter((item): item is ProjectPropertyChange => Boolean(item) && typeof item === 'object' && typeof (item as ProjectPropertyChange).field === 'string').map(item => ({ field: item.field, from: String(item.from ?? ''), to: String(item.to ?? '') }))
}

/** Folds project history into per-update "changes since last update" lists. */
export function projectUpdateChanges(updates: ProjectUpdate[], history: AuditLogEntry[], projectCreatedAt: string) {
  const result = new Map<string, ProjectPropertyChange[]>()
  const entries = history.filter(entry => entry.action === 'updated').sort((left, right) => +new Date(left.createdAt) - +new Date(right.createdAt))
  const ordered = [...updates].sort((left, right) => +new Date(left.createdAt) - +new Date(right.createdAt))
  let since = Date.parse(projectCreatedAt) || 0
  for (const update of ordered) {
    const until = Date.parse(update.createdAt)
    const merged = new Map<string, ProjectPropertyChange>()
    for (const entry of entries) {
      const at = Date.parse(entry.createdAt)
      if (at <= since || at > until) continue
      for (const change of historyChanges(entry)) {
        const previous = merged.get(change.field)
        merged.set(change.field, { field: change.field, from: previous ? previous.from : change.from, to: change.to })
      }
    }
    result.set(update.id, [...merged.values()].filter(change => change.from !== change.to))
    since = until
  }
  return result
}

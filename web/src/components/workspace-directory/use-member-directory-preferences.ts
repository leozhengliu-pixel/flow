import { useEffect, useState } from 'react'

export type MemberOrdering = 'name' | 'status' | 'joined'
export type MemberColumn = 'status' | 'joined' | 'teams'
export interface MemberDirectoryPreferences { ordering: MemberOrdering; descending: boolean; columns: MemberColumn[] }

export const memberColumnIds: MemberColumn[] = ['status', 'joined', 'teams']
const orderingIds: MemberOrdering[] = ['name', 'status', 'joined']
const fallback = (): MemberDirectoryPreferences => ({ ordering: 'name', descending: false, columns: [...memberColumnIds] })

export function memberDirectoryStorageKey(workspaceId: string, userId: string) {
  return `flow:member-directory:${workspaceId}:${userId}`
}

function readPreferences(storageKey: string): MemberDirectoryPreferences {
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) ?? 'null')
    if (!saved || typeof saved !== 'object') return fallback()
    return {
      ordering: orderingIds.includes(saved.ordering) ? saved.ordering : 'name',
      descending: saved.descending === true,
      columns: Array.isArray(saved.columns) ? memberColumnIds.filter(id => saved.columns.includes(id)) : fallback().columns,
    }
  } catch {
    return fallback()
  }
}

/** Viewer-scoped Members directory display options (ordering + visible columns), persisted like the Teams directory. */
export function useMemberDirectoryPreferences(workspaceId: string, userId: string) {
  const storageKey = memberDirectoryStorageKey(workspaceId, userId)
  const [preferences, setPreferences] = useState(() => readPreferences(storageKey))
  useEffect(() => {
    try { localStorage.setItem(storageKey, JSON.stringify(preferences)) } catch { /* Storage can be unavailable in private sessions. */ }
  }, [preferences, storageKey])
  return [preferences, setPreferences] as const
}

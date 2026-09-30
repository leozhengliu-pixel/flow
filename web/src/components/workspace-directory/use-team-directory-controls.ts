import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { emptyTeamFilters, parseTeamFilters, type TeamDirectoryFilters, type TeamOrdering } from './team-directory-model'

export type TeamColumn = 'id' | 'membership' | 'owners' | 'projects' | 'cycle' | 'created' | 'updated' | 'members'
const columnIds: TeamColumn[] = ['id', 'membership', 'owners', 'projects', 'created', 'updated', 'members', 'cycle']
/** Linear's default team columns: ID, Membership, Members, Active projects (Cycle stays opt-in). */
export const defaultTeamColumns: TeamColumn[] = ['id', 'membership', 'members', 'projects']
const PREFERENCES_VERSION = 2

export function useTeamDirectoryControls(workspaceId: string, userId: string) {
  const [params, setParams] = useSearchParams()
  const raw = params.get('teamFilters')
  const filters = useMemo(() => parseTeamFilters(raw), [raw])
  const setFilters = (update: TeamDirectoryFilters | ((current: TeamDirectoryFilters) => TeamDirectoryFilters)) => setParams(current => {
    const next = new URLSearchParams(current)
    const value = typeof update === 'function' ? update(parseTeamFilters(current.get('teamFilters'))) : update
    if (JSON.stringify(value) === JSON.stringify(emptyTeamFilters())) next.delete('teamFilters')
    else next.set('teamFilters', JSON.stringify(value))
    return next
  }, { replace: true })
  const storageKey = `flow:team-directory:${workspaceId}:${userId}`
  const [preferences, setPreferences] = useState(() => {
    const fallback = { ordering: 'name' as TeamOrdering, descending: false, columns: defaultTeamColumns }
    try {
      const saved = JSON.parse(localStorage.getItem(storageKey) ?? 'null')
      if (!saved) return fallback
      return { ordering: (['name', 'created', 'updated'].includes(saved.ordering) ? saved.ordering : 'name') as TeamOrdering,
        descending: saved.descending === true,
        columns: Array.isArray(saved.columns) ? migrateColumns(saved.columns.filter((id: TeamColumn) => columnIds.includes(id)) as TeamColumn[], saved.version) : fallback.columns }
    } catch { return fallback }
  })
  useEffect(() => { try { localStorage.setItem(storageKey, JSON.stringify({ ...preferences, version: PREFERENCES_VERSION })) } catch { /* Storage can be unavailable in private sessions. */ } }, [preferences, storageKey])
  return { filters, setFilters, preferences, setPreferences }
}

/** v1 always showed the team key and defaulted Cycle on; v2 makes the key the "ID" property and Cycle opt-in. */
function migrateColumns(columns: TeamColumn[], version: unknown): TeamColumn[] {
  if (version === PREFERENCES_VERSION) return columns
  return ['id' as TeamColumn, ...columns.filter(id => id !== 'cycle' && id !== 'id')]
}

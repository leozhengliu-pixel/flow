import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { emptyTeamFilters, parseTeamFilters, type TeamDirectoryFilters, type TeamOrdering } from './team-directory-model'

export type TeamColumn = 'membership' | 'owners' | 'projects' | 'cycle' | 'created' | 'updated' | 'members'
const columnIds: TeamColumn[] = ['membership', 'owners', 'projects', 'created', 'updated', 'members', 'cycle']

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
    const fallback = { ordering: 'name' as TeamOrdering, descending: false, columns: ['membership', 'members', 'cycle', 'projects'] as TeamColumn[] }
    try {
      const saved = JSON.parse(localStorage.getItem(storageKey) ?? 'null')
      if (!saved) return fallback
      return { ordering: (['name', 'created', 'updated'].includes(saved.ordering) ? saved.ordering : 'name') as TeamOrdering,
        descending: saved.descending === true,
        columns: Array.isArray(saved.columns) ? saved.columns.filter((id: TeamColumn) => columnIds.includes(id)) as TeamColumn[] : fallback.columns }
    } catch { return fallback }
  })
  useEffect(() => { try { localStorage.setItem(storageKey, JSON.stringify(preferences)) } catch { /* Storage can be unavailable in private sessions. */ } }, [preferences, storageKey])
  return { filters, setFilters, preferences, setPreferences }
}

import { useMemo, useState } from 'react'
import type { BootstrapData } from '@/types/flow'

export function triageOptionAvailable(data: BootstrapData) {
  return Object.values(data.teamSettings ?? {}).some((settings) => settings.triageEnabled)
}

export function filterLabelItems<T extends { title?: string; name?: string; identifier?: string; triagedAt?: string; state?: { type: string }; team?: { id: string } }>(
  items: T[],
  opts: { search: string; triageOnly: boolean; resourceType: string; teamSettings?: BootstrapData['teamSettings'] },
) {
  const query = opts.search.trim().toLowerCase()
  return items.filter((item) => {
    if (opts.triageOnly && opts.resourceType === 'issue') {
      const teamId = item.team?.id
      const triageEnabled = teamId ? opts.teamSettings?.[teamId]?.triageEnabled : false
      if (!triageEnabled) return false
      if (item.triagedAt || item.state?.type !== 'backlog') return false
    }
    if (!query) return true
    const hay = `${item.identifier ?? ''} ${item.title ?? ''} ${item.name ?? ''}`.toLowerCase()
    return hay.includes(query)
  })
}

export function useLabelPageChrome(initialSearch = '') {
  const [search, setSearch] = useState(initialSearch)
  const [triageOnly, setTriageOnly] = useState(false)
  return useMemo(
    () => ({ search, setSearch, triageOnly, setTriageOnly }),
    [search, triageOnly],
  )
}

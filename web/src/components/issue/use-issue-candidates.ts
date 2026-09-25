import { useEffect, useMemo, useState } from 'react'
import { listIssueRecords } from '@/lib/api'
import type { BootstrapData, Issue } from '@/types/flow'

/**
 * Issues an issue picker can offer. The page's data only holds the issues it has loaded, so recent
 * workspace issues are fetched while the picker is open and merged in.
 */
export function useIssueCandidates(data: BootstrapData, open: boolean): Issue[] {
  const [fetched, setFetched] = useState<Issue[]>([])
  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    listIssueRecords({ archived: 'false', sort: 'updatedAt', direction: 'desc', limit: 250 }, controller.signal, data.workspace.urlKey)
      .then(page => setFetched(page.items))
      .catch(() => undefined)
    return () => controller.abort()
  }, [data.workspace.urlKey, open])
  return useMemo(() => {
    const byId = new Map<string, Issue>()
    for (const item of [...data.issues, ...fetched]) if (!item.archivedAt) byId.set(item.id, item)
    return [...byId.values()]
  }, [data.issues, fetched])
}

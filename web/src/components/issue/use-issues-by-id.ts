import { useEffect, useMemo, useState } from 'react'
import { fetchIssueRecord } from '@/lib/api'
import type { BootstrapData, Issue } from '@/types/flow'

/** Issues by id, fetching the ones the (paged) workspace data has not loaded. */
export function useIssuesById(ids: (string | undefined)[], data: BootstrapData) {
  const [fetched, setFetched] = useState<Map<string, Issue>>(() => new Map())
  const idsKey = [...new Set(ids.filter(Boolean))].join(',')
  const missing = idsKey ? idsKey.split(',').filter(id => !data.issues.some(item => item.id === id) && !fetched.has(id)).join(',') : ''
  useEffect(() => {
    if (!missing) return
    const controller = new AbortController()
    for (const id of missing.split(',')) {
      void fetchIssueRecord(id, controller.signal, data.workspace.urlKey)
        .then(issue => { if (!controller.signal.aborted && issue) setFetched(current => new Map(current).set(id, issue)) })
        .catch(() => undefined)
    }
    return () => controller.abort()
  }, [missing, data.workspace.urlKey])
  return useMemo(() => {
    const map = new Map(fetched)
    for (const id of idsKey ? idsKey.split(',') : []) {
      const local = data.issues.find(item => item.id === id)
      if (local) map.set(id, local)
    }
    return map
  }, [fetched, data.issues, idsKey])
}

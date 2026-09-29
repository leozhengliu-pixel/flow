import { useEffect, useMemo, useState } from 'react'
import { listIssueRecords } from '@/lib/api'
import type { Issue } from '@/types/flow'

const SEARCH_DELAY_MS = 150
const SEARCH_LIMIT = 50

/**
 * Issues for a picker: the ones already loaded plus a server search for `query`, since paged workspace data
 * only holds the issues on screen.
 */
export function useIssueSearch(query: string, loaded: Issue[], enabled = true) {
  const [remote, setRemote] = useState<Issue[]>([])
  const text = query.trim()
  useEffect(() => {
    if (!enabled) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      void listIssueRecords({ q: text || undefined, limit: SEARCH_LIMIT, sort: 'updatedAt', direction: 'desc' }, controller.signal)
        .then(page => { if (!controller.signal.aborted) setRemote(page.items ?? []) })
        .catch(() => undefined)
    }, SEARCH_DELAY_MS)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [text, enabled])
  return useMemo(() => {
    const seen = new Set(loaded.map(issue => issue.id))
    return [...loaded, ...remote.filter(issue => !seen.has(issue.id))]
  }, [loaded, remote])
}

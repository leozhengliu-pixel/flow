import { useEffect, useMemo, useState } from 'react'
import { listIssueRecords, type IssueQueryInput } from '@/lib/api'
import type { BootstrapData, Issue } from '@/types/flow'

const PAGE_SIZE = 250
const MAX_ISSUES = 2500

/**
 * Issues for a scoped surface (cycle, triage…). With a complete bootstrap collection the local
 * predicate is used; in server-paged workspaces the scope is loaded from `/api/issue-records`
 * (all pages up to MAX_ISSUES) and fresher local copies win, so edits show immediately.
 */
export function useScopedIssueRecords(data: BootstrapData, query: IssueQueryInput, predicate: (issue: Issue) => boolean, enabled = true) {
  const paged = Boolean(data.issueCollectionPaged) && enabled
  const [remote, setRemote] = useState<Issue[]>([])
  const [loading, setLoading] = useState(paged)
  const [loadedKey, setLoadedKey] = useState<string>()
  const key = JSON.stringify(query)
  useEffect(() => {
    if (!paged) return
    const controller = new AbortController()
    setLoading(true)
    void (async () => {
      const items: Issue[] = []
      let cursor: string | undefined
      do {
        const page = await listIssueRecords({ ...query, limit: PAGE_SIZE, cursor }, controller.signal, data.workspace.urlKey)
        items.push(...page.items)
        cursor = page.nextCursor
      } while (cursor && items.length < MAX_ISSUES && !controller.signal.aborted)
      if (!controller.signal.aborted) { setRemote(items); setLoading(false); setLoadedKey(key) }
    })().catch(() => { if (!controller.signal.aborted) { setLoading(false); setLoadedKey(key) } })
    return () => controller.abort()
    // The serialized query is the dependency; the object identity changes every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data.workspace.urlKey, key, paged])
  const issues = useMemo(() => {
    if (!enabled) return []
    const local = data.issues.filter(predicate)
    if (!paged) return local
    const byId = new Map(remote.map(issue => [issue.id, issue]))
    for (const issue of data.issues) if (byId.has(issue.id)) byId.set(issue.id, issue)
    for (const issue of local) byId.set(issue.id, issue)
    return [...byId.values()].filter(predicate)
  }, [data.issues, enabled, paged, predicate, remote])
  // A new scope shows its loading state until its own first page arrives.
  return { issues, loading: paged && (loading || loadedKey !== key) }
}

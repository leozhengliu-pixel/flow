import { useEffect, useMemo, useRef, useState } from 'react'
import { listIssueRecords, type IssueQueryInput } from '@/lib/api'
import type { BootstrapData, IssueQueryPage } from '@/types/flow'
import { issueToExplorerRow } from './issue-explorer-model'
import { ISSUE_QUERY_INVALIDATED, type IssueQueryInvalidation } from './paged-issue-invalidation'

export async function readInsightPages(query: IssueQueryInput, signal: AbortSignal, workspace: string) {
  const items: IssueQueryPage['items'] = []
  const statusIntervals: NonNullable<IssueQueryPage['statusIntervals']> = Object.create(null)
  const seen = new Set<string>()
  let cursor: string | undefined
  do {
    signal.throwIfAborted()
    const page = await listIssueRecords({ ...query, cursor, limit: 500, includeTotal: false, sort: 'createdAt', direction: 'asc', groupBy: 'none', groupValue: undefined }, signal, workspace)
    signal.throwIfAborted()
    items.push(...page.items)
    Object.assign(statusIntervals, page.statusIntervals)
    if (!page.hasMore) break
    if (!page.nextCursor || seen.has(page.nextCursor)) throw new Error('Invalid insights cursor')
    cursor = page.nextCursor
    seen.add(cursor)
  } while (cursor)
  return { items: [...new Map(items.map(item => [item.id, item])).values()], statusIntervals }
}

export function useInsightSource(data: BootstrapData, query: IssueQueryInput | undefined, refresh: number) {
  const enabled = Boolean(query)
  const key = JSON.stringify([data.workspace.urlKey, data.viewer.id, query, refresh, data.issueCollectionRevision])
  const [revision, setRevision] = useState(0)
  const [state, setState] = useState<{ key: string; revision: number; page?: Awaited<ReturnType<typeof readInsightPages>>; error?: string }>()
  const queryRef = useRef(query); queryRef.current = query
  useEffect(() => {
    if (!enabled) return
    let timer: ReturnType<typeof setTimeout> | undefined
    const invalidate = (event: Event) => {
      if ((event as CustomEvent<IssueQueryInvalidation>).detail.workspaceKey !== data.workspace.urlKey) return
      clearTimeout(timer)
      timer = setTimeout(() => setRevision(value => value + 1), 120)
    }
    window.addEventListener(ISSUE_QUERY_INVALIDATED, invalidate)
    return () => { clearTimeout(timer); window.removeEventListener(ISSUE_QUERY_INVALIDATED, invalidate) }
  }, [enabled, data.workspace.urlKey])
  useEffect(() => {
    if (!queryRef.current) return
    const abort = new AbortController()
    void readInsightPages(queryRef.current, abort.signal, data.workspace.urlKey)
      .then(page => { if (!abort.signal.aborted) setState({ key, revision, page }) })
      .catch(error => { if (!abort.signal.aborted) setState({ key, revision, error: String(error.message ?? error) }) })
    return () => abort.abort()
  }, [key, revision, data.workspace.urlKey])
  const ready = state?.key === key && state.revision === revision
  const rows = useMemo(() => ready && state.page ? state.page.items.map(issue => ({
    ...issueToExplorerRow(issue, data.workspace.urlKey, state.page!.items, data),
    ...(state.page!.statusIntervals[issue.id] ? { statusIntervals: state.page!.statusIntervals[issue.id] } : {}),
  })) : [], [ready, state, data])
  return { rows, loading: Boolean(query) && !ready, error: ready ? state.error : undefined }
}

import type { IssueQueryInput } from '@/lib/api'
import { registerSessionCacheClearer } from '@/lib/session-cache-registry'
import { PagedIssueCache } from './paged-issue-cache'
import { ISSUE_QUERY_INVALIDATED, type IssueQueryInvalidation } from './paged-issue-invalidation'

export type IssueListGroup = { value: string; count: number; loaded: number; hasMore: boolean }
export type CachedIssueListState = { cache: PagedIssueCache; groups: IssueListGroup[] }
/** Who the cached rows belong to: a viewer's view of one workspace. */
export type IssueListScope = { viewerId: string; workspaceKey: string }

type Entry = { state: CachedIssueListState; scope: IssueListScope; mounts: number }

const cachedIssueLists = new Map<string, Entry>()
const MAX_CACHED_ISSUE_LISTS = 8
/** Retained bytes across every cached list; the least recently used lists are dropped to stay under it. */
export const ISSUE_LIST_CACHE_BUDGET_BYTES = 24 * 1024 * 1024
/** One list may hold at most half the budget, so a second list can always stay warm next to it. */
const LIST_BYTE_LIMIT = ISSUE_LIST_CACHE_BUDGET_BYTES / 2

/** The viewer is part of the key: rows cached for one viewer are never served to another. */
export function pagedIssueListSignature(viewerId: string, workspaceId: string, query: IssueQueryInput): string {
  return JSON.stringify([viewerId, workspaceId, query])
}

export function issueListState(signature: string, scope: IssueListScope): CachedIssueListState {
  const current = cachedIssueLists.get(signature)
  if (current) {
    // Map insertion order is our small LRU; recently revisited project lists stay warm.
    cachedIssueLists.delete(signature)
    current.scope = scope
    cachedIssueLists.set(signature, current)
    return current.state
  }
  const created: Entry = { state: { cache: new PagedIssueCache(40, LIST_BYTE_LIMIT, signature), groups: [] }, scope, mounts: 0 }
  cachedIssueLists.set(signature, created)
  trimPagedIssueListCache()
  return created.state
}

/** A mounted list clears and refreshes itself; only lists nobody shows are dropped behind its back. */
export function retainIssueList(state: CachedIssueListState): () => void {
  const entry = [...cachedIssueLists.values()].find(item => item.state === state)
  if (!entry) return () => {}
  entry.mounts += 1
  return () => { entry.mounts = Math.max(0, entry.mounts - 1) }
}

/** Evicts least recently used lists (unmounted ones first) until the count and byte budgets hold. */
export function trimPagedIssueListCache(): void {
  const total = () => [...cachedIssueLists.values()].reduce((sum, entry) => sum + entry.state.cache.retainedBytes, 0)
  // A list that alone exceeds the budget is not worth caching.
  for (const [signature, entry] of [...cachedIssueLists]) if (entry.state.cache.retainedBytes > ISSUE_LIST_CACHE_BUDGET_BYTES) cachedIssueLists.delete(signature)
  while (cachedIssueLists.size > MAX_CACHED_ISSUE_LISTS || total() > ISSUE_LIST_CACHE_BUDGET_BYTES) {
    const victim = [...cachedIssueLists].find(([, entry]) => entry.mounts === 0) ?? [...cachedIssueLists][0]
    if (!victim) return
    cachedIssueLists.delete(victim[0])
  }
}

export function pagedIssueListCacheStats() {
  return { entries: cachedIssueLists.size, bytes: [...cachedIssueLists.values()].reduce((sum, entry) => sum + entry.state.cache.retainedBytes, 0) }
}

/** Drops cached lists: everything, one viewer's, or one viewer's lists of one workspace. */
export function clearPagedIssueListCache(viewerId?: string, workspaceKey?: string) {
  for (const [signature, entry] of [...cachedIssueLists]) {
    if (viewerId && entry.scope.viewerId !== viewerId) continue
    if (workspaceKey && entry.scope.workspaceKey !== workspaceKey) continue
    cachedIssueLists.delete(signature)
  }
}

registerSessionCacheClearer(clearPagedIssueListCache)

// A forced (permission) invalidation means rows the viewer may have lost. Mounted lists clear themselves;
// the rest of the workspace's cached lists would otherwise show those rows the next time they open.
if (typeof window !== 'undefined') {
  window.addEventListener(ISSUE_QUERY_INVALIDATED, event => {
    const detail = (event as CustomEvent<IssueQueryInvalidation>).detail
    if (!detail?.force) return
    for (const [signature, entry] of [...cachedIssueLists]) {
      if (entry.scope.workspaceKey === detail.workspaceKey && entry.mounts === 0) cachedIssueLists.delete(signature)
    }
  })
}

import { PagedIssueCache } from './paged-issue-cache'

export type IssueListGroup = { value: string; count: number; loaded: number; hasMore: boolean }
export type CachedIssueListState = { cache: PagedIssueCache; groups: IssueListGroup[] }

const cachedIssueLists = new Map<string, CachedIssueListState>()
const MAX_CACHED_ISSUE_LISTS = 8

export function issueListState(signature: string): CachedIssueListState {
  const current = cachedIssueLists.get(signature)
  if (current) {
    // Map insertion order is our small LRU; recently revisited project lists stay warm.
    cachedIssueLists.delete(signature)
    cachedIssueLists.set(signature, current)
    return current
  }
  const created = { cache: new PagedIssueCache(40, 16 * 1024 * 1024, signature), groups: [] }
  cachedIssueLists.set(signature, created)
  while (cachedIssueLists.size > MAX_CACHED_ISSUE_LISTS) cachedIssueLists.delete(cachedIssueLists.keys().next().value!)
  return created
}

export function clearPagedIssueListCache() {
  cachedIssueLists.clear()
}

import { useCallback, useEffect, useInsertionEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { GroupedVirtuoso, Virtuoso } from 'react-virtuoso'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import { listIssueRecordGroups, listIssueRecords, type IssueQueryInput } from '@/lib/api'
import { MyIssuesGroupHeader, MyIssuesRow, type MyIssuesGroupData, type MyIssuesListProps } from '@/components/my-issues/my-issues-list'
import { issueToExplorerRow } from './issue-explorer-model'
import { nestedIssuePage, nestedPageLines } from './paged-issue-cache'
import { teamEquivalentState } from '@/lib/team-workflow'
import { focusGroupDescriptor } from './issue-grouping'
import { pagedGroupQuery, pagedStatusGroups } from './paged-issue-groups'
import { issueListState, pagedIssueListSignature, retainIssueList, trimPagedIssueListCache, type IssueListGroup } from './paged-issue-list-cache'
import { ISSUE_QUERY_INVALIDATED, issueMayMatchQuery, queryUsesLabels, type IssueQueryInvalidation } from './paged-issue-invalidation'
import styles from '@/components/my-issues/my-issues-list.module.css'
import boardStyles from './issue-board.module.css'
import { HiddenBoardColumns, IssueBoardCard, IssueBoardGroupHeader } from './issue-board'
import { LoadingContainer, LoadingListFooter } from '@/components/ui/loading-caret'
/** The server's customer group for requests whose customer no longer exists. */
const UNKNOWN_CUSTOMER_GROUP = '__unknown__'

const PAGE_SIZE = 100
type Group = IssueListGroup

/** One group page; a nested query's page comes back as rows with each sub-issue under its parent. */
function fetchIssuePage(query: IssueQueryInput, signal: AbortSignal) {
  return listIssueRecords(query, signal).then(page => query.nested ? nestedIssuePage(page) : page)
}

export function PagedIssueList({ data, query: listQuery, collapsedGroupIds, onGroupCollapsedChange, onCreateIssue, onOpenIssueRecord, onTotalChange, onLoadedIssuesChange, layout = 'list', onMoveIssueRecord, hiddenGroupIds = [], onHideGroup, onShowGroup, nestedSubIssues = false, ...rowProps }: Omit<MyIssuesListProps, 'groups' | 'onOpenIssue'> & {
  data: BootstrapData
  query: IssueQueryInput
  onOpenIssueRecord: (issue: Issue, sequence?: string[]) => void
  onTotalChange?: (count: number) => void
  onLoadedIssuesChange?: (issues: Issue[]) => void
  layout?: 'list' | 'board'
  hiddenGroupIds?: string[]
  onHideGroup?: (id: string) => void
  onShowGroup?: (id: string) => void
  onMoveIssueRecord?: (issue: Issue, input: IssueUpdateInput) => Promise<unknown>
}) {
  const viewerId = data.viewer.id, workspaceKey = data.workspace.urlKey
  // Nested sub-issues (list layout): the server leaves sub-issues whose parent is in the list out of the groups and
  // returns them with the parent's page, wherever their own group is (like the full list). The board stays flat.
  const nested = layout === 'list' && nestedSubIssues
  const query = useMemo(() => nested ? { ...listQuery, nested: true } : listQuery, [listQuery, nested])
  // "No grouping" is a flat list in the reference app: no "All issues" strip.
  const hideGroupHeaders = Boolean(rowProps.hideGroupHeaders) || query.groupBy === 'none'
  const signature = pagedIssueListSignature(viewerId, data.workspace.id, query)
  const [retry, setRetry] = useState(0)
  const refreshTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const requestRefresh = useCallback(() => {
    clearTimeout(refreshTimer.current)
    refreshTimer.current = setTimeout(() => setRetry(value => value + 1), 80)
  }, [])
  useEffect(() => () => clearTimeout(refreshTimer.current), [signature])
  const cachedState = useMemo(() => issueListState(signature, { viewerId, workspaceKey }), [signature, viewerId, workspaceKey])
  const cache = cachedState.cache
  useEffect(() => retainIssueList(cachedState), [cachedState])
  const [groups, setGroups] = useState<Group[]>(() => cachedState.groups)
  const [revision, setRevision] = useState(0)
  // Pages arrive after the entry was created: keep the lists together under the shared byte budget.
  useEffect(() => { trimPagedIssueListCache() }, [revision, groups])
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(() => cachedState.groups.length === 0)
  const updateGroups = useCallback((next: Group[] | ((current: Group[]) => Group[])) => {
    setGroups(current => {
      const value = typeof next === 'function' ? next(current) : next
      // Written to the state this list renders, so a cache entry dropped behind its back is not resurrected half-filled.
      cachedState.groups = value
      return value
    })
  }, [cachedState])
  const [dragging, setDragging] = useState<{ issue: Issue; group: string }>()
  // Bumped to forget the remembered previous result (see LastContent).
  const [lastContentEpoch, setLastContentEpoch] = useState(0)
  const active = useRef<{ signature: string; abort: AbortController; pending: Set<string>; ready: boolean } | undefined>(undefined)
  const queryRef = useRef(query)
  const totalRef = useRef(onTotalChange)
  const recordsRef = useRef(onLoadedIssuesChange)
  const groupSelection = useRef({ signature, records: new Map<string, Issue>() })
  const stateOrderRef = useRef(data.states)
  const dataRef = useRef(data)
  const groupsRef = useRef(groups)
  const openRef = useRef(onOpenIssueRecord)
  // Latest-value refs: current before any layout/passive effect or event handler of the same commit.
  useInsertionEffect(() => {
    queryRef.current = query
    totalRef.current = onTotalChange
    if (groupSelection.current.signature !== signature) groupSelection.current = { signature, records: new Map() }
    recordsRef.current = records => {
      const retained = groupSelection.current.records
      for (const id of retained.keys()) if (!rowProps.selectedIds?.has(id)) retained.delete(id)
      records.forEach(issue => { if (retained.has(issue.id)) retained.set(issue.id, issue) })
      // Selecting an entire group may exceed the viewport cache's capacity.
      onLoadedIssuesChange?.([...new Map([...retained.values(), ...records].map(issue => [issue.id, issue])).values()])
    }
    stateOrderRef.current = data.states
    dataRef.current = data
    groupsRef.current = groups
    openRef.current = onOpenIssueRecord
  })
  // Cached pages that predate the latest refresh: shown as they are until their rows are on screen, then re-fetched in place.
  const staleKeys = useRef(new Set<string>())
  // Row models keep their identity between renders so the memoised rows can be skipped.
  const rowModels = useRef(new WeakMap<Issue, { data: BootstrapData; items: Issue[]; row: ReturnType<typeof issueToExplorerRow> }>())
  const openTargets = useRef(new Map<string, { issue: Issue; group: string }>())
  const openRow = useCallback((row: { id: string }) => {
    const target = openTargets.current.get(row.id)
    if (target) openRef.current(target.issue, cache.sequence(target.group, target.issue.id))
  }, [cache])
  const rowFor = (issue: Issue, items: Issue[]) => {
    const cached = rowModels.current.get(issue)
    if (cached && cached.data === data && cached.items === items) return cached.row
    const row = issueToExplorerRow(issue, data.workspace.urlKey, items, data)
    rowModels.current.set(issue, { data, items, row })
    return row
  }
  const renderedSignature = useRef(signature)
  // A new query renders once with the old groups before its load starts: that is loading too.
  const [seenSignature, setSeenSignature] = useState(signature)
  if (seenSignature !== signature) {
    setSeenSignature(signature)
    setLoading(true)
  }
  // A load that starts or restarts clears the previous failure.
  const [seenRequest, setSeenRequest] = useState({ signature, retry, cache })
  if (seenRequest.signature !== signature || seenRequest.retry !== retry || seenRequest.cache !== cache) {
    setSeenRequest({ signature, retry, cache })
    setError(undefined)
  }
  useEffect(() => {
    const request = { signature, abort: new AbortController(), pending: new Set<string>(), ready: false }
    active.current = request
    const changed = renderedSignature.current !== signature
    renderedSignature.current = signature
    if (changed || !cache.retainedEntities) { recordsRef.current?.([]); updateGroups([]); setLoading(true) }
    const initialQuery = queryRef.current
    // A refresh of a list that is already showing rows keeps them (and the scroll position): see `revalidate`.
    const refreshing = !changed && cache.retainedEntities > 0 && groupsRef.current.length > 0
    const firstGroup = refreshing ? undefined
      : !changed && groupsRef.current.length ? groupsRef.current[0].value
      : initialQuery.groupBy === 'none' ? 'all' : initialQuery.groupBy === 'status' || !initialQuery.groupBy ? pagedStatusGroups(stateOrderRef.current.map(state => ({ value: state.id, count: 1 })), dataRef.current)[0]?.value : undefined
    // The common status/all-issues first page does not have to wait for counts.
    const prefetched = firstGroup === undefined ? undefined : fetchIssuePage({ ...pagedGroupQuery(initialQuery, firstGroup, dataRef.current), limit: PAGE_SIZE, includeTotal: false }, request.abort.signal)
    // A rejected speculative page is handled after groups resolve, never as an
    // unhandled rejection while the slower aggregate query is still pending.
    const firstPage = prefetched?.then(page => ({ page }), error => ({ error }))
    void listIssueRecordGroups(initialQuery, request.abort.signal).then(async result => {
      if (request.abort.signal.aborted) return
      const previous = refreshing ? new Map(groupsRef.current.map(group => [group.value, group])) : undefined
      // Statuses several team workflows share are one group; Focus cases come back in their own order.
      const serverGroups = queryRef.current.groupBy === 'status' || !queryRef.current.groupBy ? pagedStatusGroups(result.groups, dataRef.current) : result.groups
      const groups = serverGroups.map(group => {
        const before = previous?.get(group.value)
        const loaded = before ? Math.min(before.loaded, group.count) : 0
        return { ...group, loaded, hasMore: before ? before.hasMore && loaded < group.count : group.count > 0 }
      })
      if (queryRef.current.groupBy === 'customer') {
        // Customer groups: alphabetical, Unknown customer then No customer last.
        const rank = (value: string) => value === '' ? 2 : value === UNKNOWN_CUSTOMER_GROUP ? 1 : 0
        const names = new Map((dataRef.current.customers ?? []).map(customer => [customer.id, customer.name]))
        groups.sort((a, b) => rank(a.value) - rank(b.value) || (names.get(a.value) ?? '').localeCompare(names.get(b.value) ?? ''))
      }
      if (queryRef.current.groupBy === 'cases') groups.sort((a, b) => Number(focusGroupDescriptor(a.value).rank) - Number(focusGroupDescriptor(b.value).rank))
      if (refreshing) {
        cache.retainGroups(new Set(groups.map(group => group.value)))
        staleKeys.current = new Set(cache.pageKeys())
        request.ready = true
        recordsRef.current?.(cache.records())
        updateGroups(groups)
        setRevision(value => value + 1)
        totalRef.current?.(groups.reduce((total, group) => total + group.count, 0))
        return
      }
      const first = groups[0]
      let page
      if (first) {
        const prefetched = first.value === firstGroup ? await firstPage : undefined
        if (prefetched && 'error' in prefetched) throw prefetched.error
        page = prefetched?.page ?? await fetchIssuePage({ ...pagedGroupQuery(initialQuery, first.value, dataRef.current), limit: PAGE_SIZE, includeTotal: false }, request.abort.signal)
      }
      if (request.abort.signal.aborted) return
      cache.clear(); staleKeys.current.clear()
      if (first && page) { cache.put(first.value, 0, page); first.loaded = page.items.length; first.hasMore = page.hasMore }
      request.ready = true
      recordsRef.current?.(cache.records())
      updateGroups(groups)
      totalRef.current?.(groups.reduce((total, group) => total + group.count, 0))
    }).catch(error => { if (!request.abort.signal.aborted) setError(String(error.message ?? error)) })
      .finally(() => { if (!request.abort.signal.aborted) setLoading(false) })
    return () => request.abort.abort()
  }, [signature, retry, cache, updateGroups])
  const collectionRevision = useRef(data.issueCollectionRevision)
  useEffect(() => {
    if (collectionRevision.current === data.issueCollectionRevision) return
    collectionRevision.current = data.issueCollectionRevision
    setRetry(value => value + 1)
  }, [data.issueCollectionRevision])
  useEffect(() => {
    const invalidate = (event: Event) => {
      const detail = (event as CustomEvent<IssueQueryInvalidation>).detail
      if (detail.workspaceKey !== dataRef.current.workspace.urlKey) return
      if (detail.labelIds) {
        const deleted = new Set(detail.labelIds)
        for (const issue of cache.records()) if (issue.labels.some(label => deleted.has(label.id))) cache.update({ ...issue, labels: issue.labels.filter(label => !deleted.has(label.id)) })
        recordsRef.current?.(cache.records()); setRevision(value => value + 1)
        if (queryUsesLabels(queryRef.current)) requestRefresh()
        return
      }
      // A forced (permission) invalidation must not keep showing rows the viewer may have lost.
      if (detail.force) { cache.clear(); setLastContentEpoch(value => value + 1); recordsRef.current?.([]); updateGroups([]); setLoading(true) }
      if (detail.force) { clearTimeout(refreshTimer.current); setRetry(value => value + 1) }
      else if (!detail.issue || cache.contains(detail.issue.id) || issueMayMatchQuery(detail.issue, queryRef.current, dataRef.current)) requestRefresh()
    }
    window.addEventListener(ISSUE_QUERY_INVALIDATED, invalidate)
    return () => window.removeEventListener(ISSUE_QUERY_INVALIDATED, invalidate)
  }, [cache, requestRefresh, updateGroups])
  const observedIssues = useRef(data.issues)
  useEffect(() => {
    if (observedIssues.current === data.issues) return
    const oldRecords = new Map(observedIssues.current.map(issue => [issue.id, issue]))
    observedIssues.current = data.issues
    let refresh = false, updated = false
    for (const issue of data.issues) {
      const oldRecord = oldRecords.get(issue.id)
      oldRecords.delete(issue.id)
      if (oldRecord === issue) continue
      const previous = cache.update(issue)
      if (previous) updated = true
      if (previous ? issueQueryChanged(previous, issue, queryRef.current) : (!oldRecord || issueQueryChanged(oldRecord, issue, queryRef.current)) && (issueMayMatchQuery(issue, queryRef.current, dataRef.current) || Boolean(oldRecord && issueMayMatchQuery(oldRecord, queryRef.current, dataRef.current)))) refresh = true
    }
    // Bootstrap's bounded detail cache can evict unrelated entities. Its absence
    // alone is not a deletion; explicit deletion events invalidate the query.
    if (refresh) requestRefresh()
    // oxlint-disable-next-line react/set-state-in-effect -- the effect patches the imperative page cache (an external store) from the new bootstrap rows; the revision bump is its change notification and cannot be derived during render
    else if (updated) { recordsRef.current?.(cache.records()); setRevision(value => value + 1) }
  }, [cache, data.issues, requestRefresh])
  const load = useCallback((group: string, pageIndex: number) => {
    const request = active.current, key = cache.key(group, pageIndex)
    if (!request || !request.ready || request.signature !== signature || request.abort.signal.aborted || request.pending.has(key) || !cache.hasCursor(group, pageIndex)) return
    request.pending.add(key)
    const cursor = cache.cursors.get(key)
    void fetchIssuePage({ ...pagedGroupQuery(queryRef.current, group, dataRef.current), cursor, limit: PAGE_SIZE, includeTotal: false }, request.abort.signal).then(page => {
      if (request.abort.signal.aborted) return
      cache.put(group, pageIndex, page, cursor)
      recordsRef.current?.(cache.records())
      const end = cache.start(group, pageIndex) + page.items.length
      updateGroups(current => current.map(item => item.value === group ? { ...item, loaded: Math.max(item.loaded, end), hasMore: end >= item.loaded ? page.hasMore : item.hasMore } : item))
      setRevision(value => value + 1)
    }).catch(error => { if (!request.abort.signal.aborted) setError(String(error.message ?? error)) })
      .finally(() => request.pending.delete(key))
  }, [cache, signature, updateGroups])
  /** Re-fetches a stale page in place (same cursor). Its rows stay on screen meanwhile, so nothing flashes or moves. */
  const revalidate = useCallback((group: string, pageIndex: number) => {
    const request = active.current, key = cache.key(group, pageIndex)
    if (!request || !request.ready || request.signature !== signature || request.abort.signal.aborted || request.pending.has(key) || !staleKeys.current.has(key)) return
    request.pending.add(key)
    const cursor = cache.cursors.get(key)
    void fetchIssuePage({ ...pagedGroupQuery(queryRef.current, group, dataRef.current), cursor, limit: PAGE_SIZE, includeTotal: false }, request.abort.signal).then(page => {
      if (request.abort.signal.aborted) return
      staleKeys.current.delete(key)
      const previousLength = cache.get(group, pageIndex)?.items.length
      cache.put(group, pageIndex, page, cursor)
      // A page that changed length moves every later page: those are fetched again, in order, as they are reached.
      const resized = previousLength !== page.items.length
      if (resized) cache.truncateAfter(group, pageIndex)
      const end = cache.start(group, pageIndex) + page.items.length
      recordsRef.current?.(cache.records())
      updateGroups(current => current.map(item => item.value === group ? { ...item, loaded: resized ? end : item.loaded, hasMore: resized || end >= item.loaded ? page.hasMore : item.hasMore } : item))
      setRevision(value => value + 1)
    }).catch(() => undefined)
      .finally(() => request.pending.delete(key))
  }, [cache, signature, updateGroups])
  const descriptor = useCallback((group: Group): MyIssuesGroupData => {
    const field = query.groupBy ?? 'status'
    if (field === 'cases') {
      const { rank: _rank, ...focus } = focusGroupDescriptor(group.value, { cycles: data.cycles })
      return { ...focus, issues: [], totalCount: group.count }
    }
    const state = field === 'status' ? data.states.find(state => state.id === group.value) : undefined
    const label = state?.name ?? (field === 'none' ? 'All issues' : field === 'priority' ? ['No priority', 'Urgent', 'High', 'Medium', 'Low'][Number(group.value)] : field === 'assignee' || field === 'creator' ? data.users.find(user => user.id === group.value)?.displayName : field === 'project' ? data.projects.find(project => project.id === group.value)?.name : field === 'team' ? data.teams.find(team => team.id === group.value)?.name : field === 'cycle' ? data.cycles.find(cycle => cycle.id === group.value)?.name : field === 'label' ? data.labels.find(label => label.id === group.value)?.name : field === 'milestone' ? data.projects.flatMap(project => project.milestones ?? []).find(milestone => milestone.id === group.value)?.name : field === 'customer' ? (group.value === UNKNOWN_CUSTOMER_GROUP ? 'Unknown customer' : group.value ? data.customers?.find(customer => customer.id === group.value)?.name ?? 'Unknown customer' : 'No customer') : undefined) ?? (group.value || `No ${field}`)
    return { id: group.value, label, state, stateType: state?.type, issues: [], totalCount: group.count, createContext: state ? { stateId: state.id } : field === 'priority' ? { priority: Number(group.value) as 0|1|2|3|4 } : field === 'project' ? { projectId: group.value } : field === 'assignee' ? { assigneeId: group.value } : field === 'team' ? { teamId: group.value } : field === 'cycle' ? { cycleId: group.value } : field === 'label' ? { labelIds: group.value ? [group.value] : [] } : undefined }
  }, [data.states, data.users, data.projects, data.teams, data.cycles, data.labels, data.customers, query.groupBy])
  const counts = groups.map(group => collapsedGroupIds?.has(group.value) || hiddenGroupIds.includes(group.value) ? 0 : group.loaded + (group.hasMore ? 1 : 0))
  const selectGroup = async (group: Group) => {
    const request = active.current
    if (!request || !rowProps.onSelectIssue) return
    const ids = new Set<string>()
    const pages: { page: Awaited<ReturnType<typeof fetchIssuePage>>; cursor?: string }[] = []
    let cursor: string | undefined
    try {
      do {
        const page = await fetchIssuePage({ ...pagedGroupQuery(query, group.value, data), limit: PAGE_SIZE, includeTotal: false, cursor }, request.abort.signal)
        pages.push({ page, cursor })
        page.items.forEach(issue => ids.add(issue.id))
        cursor = page.hasMore ? page.nextCursor : undefined
      } while (cursor)
      if (!request.abort.signal.aborted) {
        pages.forEach(({ page, cursor }, index) => cache.put(group.value, index, page, cursor))
        pages.forEach(({ page }) => page.items.forEach(issue => groupSelection.current.records.set(issue.id, issue)))
        onLoadedIssuesChange?.([...new Map([...cache.records(), ...groupSelection.current.records.values()].map(issue => [issue.id, issue])).values()])
        updateGroups(current => current.map(item => item.value === group.value ? { ...item, loaded: ids.size, hasMore: false } : item))
        ids.forEach(id => rowProps.onSelectIssue?.(id, true, false))
      }
    } catch (error) {
      if (!request.abort.signal.aborted) setError(String(error instanceof Error ? error.message : error))
    }
  }
  // The identifier column fits the longest identifier loaded for this query (like the full list), and only grows
  // while pages load so rows do not shift as the list scrolls.
  const [identifierWidth, setIdentifierWidth] = useState({ signature, length: 6 })
  let identifierLength = identifierWidth.signature === signature ? identifierWidth.length : 6
  for (const issue of cache.records()) identifierLength = Math.max(identifierLength, [...issue.identifier].length)
  if (identifierWidth.signature !== signature || identifierWidth.length !== identifierLength) setIdentifierWidth({ signature, length: identifierLength })
  const listStyle = { '--issue-identifier-width': `${identifierLength}ch` } as CSSProperties
  const offsets: number[] = []; let offset = 0
  for (const count of counts) { offsets.push(offset); offset += count }
  const renderList = (): ReactNode => {
    if (error && !cache.retainedEntities) return <div className={styles.state} role="alert"><p>{error}</p><button onClick={() => { active.current?.abort.abort(); setRetry(value => value + 1) }}>Retry</button></div>
    if (rowProps.emptyState && groups.every(group => !group.count)) return rowProps.emptyState
    if (!groups.length) return <div className={styles.state}>No issues</div>
    if (layout === 'board') {
      const drop = (group: Group, before?: Issue) => {
        if (!dragging || !onMoveIssueRecord) return
        const property = query.groupBy === 'status' ? 'stateId' : query.groupBy === 'priority' ? 'priority' : query.groupBy === 'assignee' ? 'assigneeId' : query.groupBy === 'project' ? 'projectId' : query.groupBy === 'cycle' ? 'cycleId' : undefined
        if (!property) return
        const input: IssueUpdateInput = { [property]: property === 'priority' ? Number(group.value) : group.value }
        // A status column of another team's workflow moves the issue to its own team's equivalent status.
        if (property === 'stateId') {
          const target = data.states.find(state => state.id === group.value)
          const next = target ? teamEquivalentState(data, dragging.issue.team.id, target) : undefined
          if (target && !next) { setDragging(undefined); return }
          if (next) input.stateId = next.id
        }
        if (before) input.sortOrder = (before.sortOrder ?? 0) - .5
        const issue = dragging.issue; setDragging(undefined)
        void onMoveIssueRecord(issue, input).then(() => setRetry(value => value + 1)).catch(error => setError(String(error.message ?? error)))
      }
      return <div className={boardStyles.board} role="list" aria-label="Issue board">
        {groups.filter(group => !hiddenGroupIds.includes(group.value)).map(group => <VisibleIssueColumn key={group.value} label={descriptor(group).label}>
          <IssueBoardGroupHeader group={descriptor(group)} onCreateIssue={onCreateIssue} onHideGroup={onHideGroup} onSelectIssue={rowProps.onSelectIssue}/>
          <div style={{ flex: 1, minHeight: 0 }} onDragOver={event => event.preventDefault()} onDrop={event => { event.preventDefault(); drop(group) }}>
            <Virtuoso style={{ height: '100%' }} totalCount={group.loaded + (group.hasMore ? 1 : 0)} defaultItemHeight={120} context={revision} increaseViewportBy={240} itemContent={index => {
              const pageIndex = cache.pageAt(group.value, index), page = cache.get(group.value, pageIndex), issue = page?.items[index - cache.start(group.value, pageIndex)]
              if (!issue) return <IssuePagePlaceholder load={() => load(group.value, pageIndex)}/>
              const row = rowFor(issue, page!.items)
              return <div style={{ padding: '4px 8px' }}><StalePageProbe stale={staleKeys.current.has(cache.key(group.value, pageIndex))} revision={revision} run={() => revalidate(group.value, pageIndex)}/><IssueBoardCard issue={row} properties={rowProps.displayProperties ?? new Set(['id','status','priority','assignee'])} propertyOptions={rowProps.propertyOptions ?? { status: [], priority: [], assignee: [], dueDate: [], labels: [], project: [] }} selected={Boolean(rowProps.selectedIds?.has(issue.id))} dragging={dragging?.issue.id === issue.id} dropBefore={false} onDragStart={() => setDragging({ issue, group: group.value })} onDragEnd={() => setDragging(undefined)} onDragOver={event => event.preventDefault()} onDrop={event => { event.preventDefault(); event.stopPropagation(); drop(group, issue) }} onOpen={() => onOpenIssueRecord(issue, cache.sequence(group.value, issue.id))} onOpenSubIssue={child => { const issue = page!.items.find(issue => issue.id === child.id); if (issue) onOpenIssueRecord(issue, cache.sequence(group.value, issue.id)) }} onPropertyChange={(property, value) => rowProps.onPropertyChange?.(row, property, value)} onSelect={(selected, range) => rowProps.onSelectIssue?.(issue.id, selected, range)}/></div>
            }}/>
          </div>
        </VisibleIssueColumn>)}
        <HiddenBoardColumns groups={groups.filter(group => hiddenGroupIds.includes(group.value)).map(descriptor)} onShowGroup={onShowGroup}/>
      </div>
    }
    return <GroupedVirtuoso className={styles.virtualList} role="list" aria-label="Issues" style={listStyle} groupCounts={counts} context={revision} defaultItemHeight={44} increaseViewportBy={{ top: 176, bottom: 264 }}
      groupContent={index => hideGroupHeaders || hiddenGroupIds.includes(groups[index].value) ? null : <MyIssuesGroupHeader group={descriptor(groups[index])} collapsed={Boolean(collapsedGroupIds?.has(groups[index].value))} createIssueLabel="Create new issue" onCreateIssue={onCreateIssue} onGroupCollapsedChange={onGroupCollapsedChange} onCollapseAll={onGroupCollapsedChange ? () => groups.forEach(group => onGroupCollapsedChange(group.value, true)) : undefined} onSelectAll={rowProps.onSelectIssue ? () => void selectGroup(groups[index]) : undefined} onHideGroup={onHideGroup ? () => onHideGroup(groups[index].value) : undefined}/>}
      itemContent={(index, groupIndex) => {
        const group = groups[groupIndex], localIndex = index - offsets[groupIndex]
        const pageIndex = cache.pageAt(group.value, localIndex), page = cache.get(group.value, pageIndex)
        const issue = page?.items[localIndex - cache.start(group.value, pageIndex)]
        if (!issue) return <IssuePagePlaceholder load={() => load(group.value, pageIndex)}/>
        const row = rowFor(issue, page!.items)
        openTargets.current.set(issue.id, { issue, group: group.value })
        return <><StalePageProbe stale={staleKeys.current.has(cache.key(group.value, pageIndex))} revision={revision} run={() => revalidate(group.value, pageIndex)}/><MyIssuesRow issue={row} selected={rowProps.selectedIds?.has(issue.id)} displayProperties={rowProps.displayProperties} nestedLines={nested ? nestedPageLines(page!.items).get(issue.id) : undefined} propertyOptions={rowProps.propertyOptions} mutationError={rowProps.mutationErrors?.get(issue.id)} onOpen={openRow} onPropertyChange={rowProps.onPropertyChange} onSelect={rowProps.onSelectIssue} onContextAction={rowProps.onContextAction} onRetryMutation={rowProps.onRetryMutation}/></>
      }}/>
  }
  // While a query loads, the previous result stays (inert) and fades out after 500ms;
  // a first load is blank. Either way the loader caret shows from 1s, never text.
  const pending = loading
  return <LoadingContainer loading={pending} label="Loading issues"><LastContent content={pending ? null : renderList()} loading={pending} epoch={lastContentEpoch}/></LoadingContainer>
}

/** Remembers the last loaded list so a reload can keep showing it; a new `epoch` forgets it. */
function LastContent({ content, loading, epoch }: { content: ReactNode; loading: boolean; epoch: number }) {
  const [remembered, setRemembered] = useState<{ node: ReactNode; epoch: number }>({ node: loading ? null : content, epoch })
  let node = remembered.epoch === epoch ? remembered.node : null
  if (!loading && node !== content) node = content
  if (remembered.node !== node || remembered.epoch !== epoch) setRemembered({ node, epoch })
  return loading ? node : content
}

function VisibleIssueColumn({ label, children }: { label: string; children: ReactNode }) {
  const ref = useRef<HTMLElement>(null)
  const [visible, setVisible] = useState(typeof IntersectionObserver === 'undefined')
  useEffect(() => {
    if (!ref.current || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver(entries => setVisible(entries.some(entry => entry.isIntersecting)), { rootMargin: '0px 348px' })
    observer.observe(ref.current)
    return () => observer.disconnect()
  }, [])
  return <section ref={ref} className={boardStyles.column} role="listitem" aria-label={label} style={{ height: '100%', minHeight: 0, display: 'flex', flexDirection: 'column' }}>{visible ? children : <header className={boardStyles.columnHeader}><strong data-i18n-ignore>{label}</strong></header>}</section>
}

function issueQueryChanged(before: Issue, after: Issue, query: IssueQueryInput) {
  const fields = new Set([query.groupBy ?? 'status', query.sort ?? 'sortOrder'])
  // Nested lists place a sub-issue by its parent.
  if (query.nested) fields.add('parent')
  const visit = (node: unknown) => {
    if (!node || typeof node !== 'object') return
    const filter = node as Record<string, unknown>
    if (typeof filter.field === 'string') fields.add(({ assigneeId: 'assignee', creatorId: 'creator', projectId: 'project', labelId: 'labels', cycleId: 'cycle' } as Record<string, string>)[filter.field] ?? filter.field)
    for (const key of ['and','or']) { const children = filter[key]; if (Array.isArray(children)) children.forEach(visit) }
    visit(filter.not)
  }
  visit(query.filter)
  // Focus cases: a change of what decides an issue's bucket moves it to another group.
  if (query.groupBy === 'cases') { fields.delete('cases'); for (const item of query.groupCases ?? []) visit(item.filter) }
  if (before.archivedAt !== after.archivedAt || before.team.id !== after.team.id) return true
  if (query.projectId && before.project?.id !== after.project?.id || query.stateId && before.state.id !== after.state.id) return true
  const value = (issue: Issue, field: string | undefined) => field === 'status' ? issue.state.id : field === 'statusType' ? issue.state.type : field === 'team' ? issue.team.id : field === 'delegateId' ? issue.delegate?.id : field?.startsWith('relation:') ? (issue.relations ?? []).filter(relation => relation.type === field.slice(9)).length : field === 'project' ? issue.project?.id : field === 'assignee' ? issue.assignee?.id : field === 'creator' ? issue.creator.id : field === 'cycle' ? issue.cycleId : field === 'label' || field === 'labels' ? (issue.labels ?? []).map(label => label.id).sort().join(',') : field === 'parent' ? issue.parentId : field ? issue[field as keyof Issue] : undefined
  return [...fields].some(field => value(before, field) !== value(after, field)) || Boolean(query.q && (before.title !== after.title || before.description !== after.description))
}

/** Starts a stale page's re-fetch once one of its rows is rendered, and again whenever a refresh lands (`revision`). */
function StalePageProbe({ stale, revision, run }: { stale: boolean; revision: number; run: () => void }) {
  const runRef = useRef(run)
  useInsertionEffect(() => { runRef.current = run })
  useEffect(() => { if (stale) runRef.current() }, [stale, revision])
  return null
}

/** A not-yet-fetched page: fetches it and shows the list-footer caret meanwhile. */
function IssuePagePlaceholder({ load }: { load: () => void }) {
  const loadRef = useRef(load)
  useInsertionEffect(() => { loadRef.current = load })
  useEffect(() => { loadRef.current() }, [load])
  return <LoadingListFooter label="Loading issues"/>
}

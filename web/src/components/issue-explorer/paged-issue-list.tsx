import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { GroupedVirtuoso, Virtuoso } from 'react-virtuoso'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import { listIssueRecordGroups, listIssueRecords, type IssueQueryInput } from '@/lib/api'
import { MyIssuesGroupHeader, MyIssuesRow, type MyIssuesGroupData, type MyIssuesListProps } from '@/components/my-issues/my-issues-list'
import { issueToExplorerRow } from './issue-explorer-model'
import { PagedIssueCache } from './paged-issue-cache'
import { ISSUE_QUERY_INVALIDATED, issueMayMatchQuery, queryUsesLabels, type IssueQueryInvalidation } from './paged-issue-invalidation'
import styles from '@/components/my-issues/my-issues-list.module.css'
import boardStyles from './issue-board.module.css'
import { IssueBoardCard, IssueBoardGroupHeader } from './issue-board'

const PAGE_SIZE = 100
type Group = { value: string; count: number; loaded: number; hasMore: boolean }

export function PagedIssueList({ data, query, collapsedGroupIds, onGroupCollapsedChange, onCreateIssue, onOpenIssueRecord, onTotalChange, onLoadedIssuesChange, layout = 'list', onMoveIssueRecord, hiddenGroupIds = [], onHideGroup, onShowGroup, ...rowProps }: Omit<MyIssuesListProps, 'groups' | 'onOpenIssue'> & {
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
  const signature = JSON.stringify([data.workspace.id, query])
  const [retry, setRetry] = useState(0)
  const refreshTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const requestRefresh = useCallback(() => {
    clearTimeout(refreshTimer.current)
    refreshTimer.current = setTimeout(() => setRetry(value => value + 1), 80)
  }, [])
  useEffect(() => () => clearTimeout(refreshTimer.current), [signature])
  const cache = useMemo(() => new PagedIssueCache(40, 16 * 1024 * 1024, signature), [signature])
  const [groups, setGroups] = useState<Group[]>([])
  const [revision, setRevision] = useState(0)
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(true)
  const [dragging, setDragging] = useState<{ issue: Issue; group: string }>()
  const active = useRef<{ signature: string; abort: AbortController; pending: Set<string>; ready: boolean } | undefined>(undefined)
  const queryRef = useRef(query); queryRef.current = query
  const totalRef = useRef(onTotalChange); totalRef.current = onTotalChange
  const recordsRef = useRef(onLoadedIssuesChange); recordsRef.current = onLoadedIssuesChange
  const stateOrderRef = useRef(data.states); stateOrderRef.current = data.states
  const dataRef = useRef(data); dataRef.current = data
  const groupsRef = useRef(groups); groupsRef.current = groups
  const renderedSignature = useRef(signature)
  useEffect(() => {
    const request = { signature, abort: new AbortController(), pending: new Set<string>(), ready: false }
    active.current = request
    const changed = renderedSignature.current !== signature
    renderedSignature.current = signature
    if (changed || !cache.retainedEntities) { recordsRef.current?.([]); setGroups([]); setLoading(true) }
    setError(undefined)
    const initialQuery = queryRef.current
    const firstGroup = !changed && groupsRef.current.length ? groupsRef.current[0].value
      : initialQuery.groupBy === 'none' ? 'all' : initialQuery.groupBy === 'status' || !initialQuery.groupBy ? stateOrderRef.current[0]?.id : undefined
    // The common status/all-issues first page does not have to wait for counts.
    const prefetched = firstGroup === undefined ? undefined : listIssueRecords({ ...initialQuery, groupValue: firstGroup, limit: PAGE_SIZE, includeTotal: false }, request.abort.signal)
    // A rejected speculative page is handled after groups resolve, never as an
    // unhandled rejection while the slower aggregate query is still pending.
    const firstPage = prefetched?.then(page => ({ page }), error => ({ error }))
    void listIssueRecordGroups(initialQuery, request.abort.signal).then(async result => {
      if (request.abort.signal.aborted) return
      const groups = result.groups.map(group => ({ ...group, loaded: 0, hasMore: group.count > 0 }))
      if (queryRef.current.groupBy === 'status') {
        const order = new Map(stateOrderRef.current.map((state, index) => [state.id, index]))
        groups.sort((a, b) => (order.get(a.value) ?? 999) - (order.get(b.value) ?? 999))
      }
      const first = groups[0]
      let page
      if (first) {
        const prefetched = first.value === firstGroup ? await firstPage : undefined
        if (prefetched && 'error' in prefetched) throw prefetched.error
        page = prefetched?.page ?? await listIssueRecords({ ...initialQuery, groupValue: first.value, limit: PAGE_SIZE, includeTotal: false }, request.abort.signal)
      }
      if (request.abort.signal.aborted) return
      cache.clear()
      if (first && page) { cache.put(first.value, 0, page); first.loaded = page.items.length; first.hasMore = page.hasMore }
      request.ready = true
      recordsRef.current?.(cache.records())
      setGroups(groups)
      totalRef.current?.(groups.reduce((total, group) => total + group.count, 0))
    }).catch(error => { if (!request.abort.signal.aborted) setError(String(error.message ?? error)) })
      .finally(() => { if (!request.abort.signal.aborted) setLoading(false) })
    return () => request.abort.abort()
  }, [signature, retry, cache])
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
      if (detail.force) { cache.clear(); recordsRef.current?.([]); setGroups([]); setLoading(true) }
      if (detail.force) { clearTimeout(refreshTimer.current); setRetry(value => value + 1) }
      else if (!detail.issue || cache.contains(detail.issue.id) || issueMayMatchQuery(detail.issue, queryRef.current, dataRef.current)) requestRefresh()
    }
    window.addEventListener(ISSUE_QUERY_INVALIDATED, invalidate)
    return () => window.removeEventListener(ISSUE_QUERY_INVALIDATED, invalidate)
  }, [cache, requestRefresh])
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
    else if (updated) { recordsRef.current?.(cache.records()); setRevision(value => value + 1) }
  }, [cache, data.issues, requestRefresh])
  const load = useCallback((group: string, pageIndex: number) => {
    const request = active.current, key = cache.key(group, pageIndex)
    if (!request || !request.ready || request.signature !== signature || request.abort.signal.aborted || request.pending.has(key) || !cache.hasCursor(group, pageIndex)) return
    request.pending.add(key)
    const cursor = cache.cursors.get(key)
    void listIssueRecords({ ...queryRef.current, groupValue: group, cursor, limit: PAGE_SIZE, includeTotal: false }, request.abort.signal).then(page => {
      if (request.abort.signal.aborted) return
      cache.put(group, pageIndex, page, cursor)
      recordsRef.current?.(cache.records())
      const end = cache.start(group, pageIndex) + page.items.length
      setGroups(current => current.map(item => item.value === group ? { ...item, loaded: Math.max(item.loaded, end), hasMore: end >= item.loaded ? page.hasMore : item.hasMore } : item))
      setRevision(value => value + 1)
    }).catch(error => { if (!request.abort.signal.aborted) setError(String(error.message ?? error)) })
      .finally(() => request.pending.delete(key))
  }, [cache, signature])
  const descriptor = useCallback((group: Group): MyIssuesGroupData => {
    const field = query.groupBy ?? 'status'
    const state = field === 'status' ? data.states.find(state => state.id === group.value) : undefined
    const label = state?.name ?? (field === 'none' ? 'All issues' : field === 'priority' ? ['No priority', 'Urgent', 'High', 'Medium', 'Low'][Number(group.value)] : field === 'assignee' || field === 'creator' ? data.users.find(user => user.id === group.value)?.displayName : field === 'project' ? data.projects.find(project => project.id === group.value)?.name : field === 'team' ? data.teams.find(team => team.id === group.value)?.name : field === 'cycle' ? data.cycles.find(cycle => cycle.id === group.value)?.name : field === 'label' ? data.labels.find(label => label.id === group.value)?.name : field === 'milestone' ? data.projects.flatMap(project => project.milestones ?? []).find(milestone => milestone.id === group.value)?.name : undefined) ?? (group.value || `No ${field}`)
    return { id: group.value, label, state, stateType: state?.type, issues: [], totalCount: group.count, createContext: state ? { stateId: state.id } : field === 'priority' ? { priority: Number(group.value) as 0|1|2|3|4 } : field === 'project' ? { projectId: group.value } : field === 'assignee' ? { assigneeId: group.value } : field === 'team' ? { teamId: group.value } : field === 'cycle' ? { cycleId: group.value } : field === 'label' ? { labelIds: group.value ? [group.value] : [] } : undefined }
  }, [data.states, data.users, data.projects, data.teams, data.cycles, data.labels, query.groupBy])
  const counts = groups.map(group => collapsedGroupIds?.has(group.value) ? 0 : group.loaded + (group.hasMore ? 1 : 0))
  const offsets: number[] = []; let offset = 0
  for (const count of counts) { offsets.push(offset); offset += count }
  if (loading) return <div className={styles.loadingMore} role="status">Loading issues…</div>
  if (error && !cache.retainedEntities) return <div className={styles.state} role="alert"><p>{error}</p><button onClick={() => { active.current?.abort.abort(); setRetry(value => value + 1) }}>Retry</button></div>
  if (!groups.length) return <div className={styles.state}>No issues</div>
  if (layout === 'board') {
    const drop = (group: Group, before?: Issue) => {
      if (!dragging || !onMoveIssueRecord) return
      const property = query.groupBy === 'status' ? 'stateId' : query.groupBy === 'priority' ? 'priority' : query.groupBy === 'assignee' ? 'assigneeId' : query.groupBy === 'project' ? 'projectId' : query.groupBy === 'cycle' ? 'cycleId' : undefined
      if (!property) return
      const input: IssueUpdateInput = { [property]: property === 'priority' ? Number(group.value) : group.value }
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
            const row = issueToExplorerRow(issue, data.workspace.urlKey, page!.items, data)
            return <div style={{ padding: '4px 8px' }}><IssueBoardCard issue={row} properties={rowProps.displayProperties ?? new Set(['id','status','priority','assignee'])} propertyOptions={rowProps.propertyOptions ?? { status: [], priority: [], assignee: [], dueDate: [], labels: [], project: [] }} selected={Boolean(rowProps.selectedIds?.has(issue.id))} dragging={dragging?.issue.id === issue.id} dropBefore={false} onDragStart={() => setDragging({ issue, group: group.value })} onDragEnd={() => setDragging(undefined)} onDragOver={event => event.preventDefault()} onDrop={event => { event.preventDefault(); event.stopPropagation(); drop(group, issue) }} onOpen={() => onOpenIssueRecord(issue, cache.sequence(group.value, issue.id))} onOpenSubIssue={child => { const issue = page!.items.find(issue => issue.id === child.id); if (issue) onOpenIssueRecord(issue, cache.sequence(group.value, issue.id)) }} onPropertyChange={(property, value) => rowProps.onPropertyChange?.(row, property, value)} onSelect={(selected, range) => rowProps.onSelectIssue?.(issue.id, selected, range)}/></div>
          }}/>
        </div>
      </VisibleIssueColumn>)}
      {hiddenGroupIds.length > 0 && <section className={boardStyles.hiddenColumns}><strong>Hidden columns</strong>{groups.filter(group => hiddenGroupIds.includes(group.value)).map(group => <button key={group.value} onClick={() => onShowGroup?.(group.value)}><span data-i18n-ignore>{descriptor(group).label}</span><b>{group.count}</b></button>)}</section>}
    </div>
  }
  return <GroupedVirtuoso className={styles.virtualList} role="list" aria-label="Issues" groupCounts={counts} context={revision} defaultItemHeight={44} increaseViewportBy={{ top: 176, bottom: 264 }}
    groupContent={index => <MyIssuesGroupHeader group={descriptor(groups[index])} collapsed={Boolean(collapsedGroupIds?.has(groups[index].value))} createIssueLabel="Create new issue" onCreateIssue={onCreateIssue} onGroupCollapsedChange={onGroupCollapsedChange}/>}
    itemContent={(index, groupIndex) => {
      const group = groups[groupIndex], localIndex = index - offsets[groupIndex], pageIndex = cache.pageAt(group.value, localIndex)
      const page = cache.get(group.value, pageIndex), issue = page?.items[localIndex - cache.start(group.value, pageIndex)]
      if (!issue) return <IssuePagePlaceholder load={() => load(group.value, pageIndex)}/>
      const row = issueToExplorerRow(issue, data.workspace.urlKey, page!.items, data)
      return <MyIssuesRow issue={row} selected={rowProps.selectedIds?.has(issue.id)} displayProperties={rowProps.displayProperties} propertyOptions={rowProps.propertyOptions} mutationError={rowProps.mutationErrors?.get(issue.id)} onOpen={() => onOpenIssueRecord(issue, cache.sequence(group.value, issue.id))} onPropertyChange={rowProps.onPropertyChange} onSelect={rowProps.onSelectIssue} onContextAction={rowProps.onContextAction} onRetryMutation={rowProps.onRetryMutation}/>
    }}/>
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
  const visit = (node: unknown) => {
    if (!node || typeof node !== 'object') return
    const filter = node as Record<string, unknown>
    if (typeof filter.field === 'string') fields.add(({ assigneeId: 'assignee', creatorId: 'creator', projectId: 'project', labelId: 'labels', cycleId: 'cycle' } as Record<string, string>)[filter.field] ?? filter.field)
    for (const key of ['and','or']) { const children = filter[key]; if (Array.isArray(children)) children.forEach(visit) }
  }
  visit(query.filter)
  if (before.archivedAt !== after.archivedAt || before.team.id !== after.team.id) return true
  if (query.projectId && before.project?.id !== after.project?.id || query.stateId && before.state.id !== after.state.id) return true
  const value = (issue: Issue, field: string | undefined) => field === 'status' ? issue.state.id : field === 'project' ? issue.project?.id : field === 'assignee' ? issue.assignee?.id : field === 'creator' ? issue.creator.id : field === 'cycle' ? issue.cycleId : field === 'label' || field === 'labels' ? (issue.labels ?? []).map(label => label.id).sort().join(',') : field === 'parent' ? issue.parentId : field ? issue[field as keyof Issue] : undefined
  return [...fields].some(field => value(before, field) !== value(after, field)) || Boolean(query.q && (before.title !== after.title || before.description !== after.description))
}

function IssuePagePlaceholder({ load }: { load: () => void }) {
  const loadRef = useRef(load); loadRef.current = load
  useEffect(() => { loadRef.current() }, [load])
  return <div className={styles.loadingMore}><button type="button" onClick={load}>Load issues…</button></div>
}

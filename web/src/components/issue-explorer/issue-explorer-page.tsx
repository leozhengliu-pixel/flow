import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { BootstrapData, Issue, IssueUpdateInput, SavedView, SavedViewMutationInput, Team } from '@/types/flow'
import type { TeamIssuesRouteView } from '@/lib/app-routes'
import { MyIssuesBulkActionBar } from '@/components/my-issues/my-issues-bulk-action-bar'
import { MyIssuesDetailsPane, type MyIssuesDetailsSummary, type MyIssuesSummaryItem, type MyIssuesSummaryTab } from '@/components/my-issues/my-issues-details-pane'
import { MyIssuesFilterBar, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-bar'
import { MyIssuesList, type MyIssuesContextAction, type MyIssuesEditableProperty, type MyIssuesGroupData, type MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { defaultMyIssuesDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import type { MyIssuesDisplayOptions, MyIssuesFilterKey, MyIssuesFilterOption, MyIssuesGrouping, MyIssuesProperty } from '@/components/my-issues/my-issues-surface'
import { useMyIssuesSelection } from '@/components/my-issues/use-my-issues-state'
import { toggleFilterOption, updateFilterOperator, updateFilterValues } from '@/components/my-issues/my-issues-filter-types'
import { IssueExplorerSurface } from './issue-explorer-surface'
import { IssueBoard } from './issue-board'
import { SavedViewEditor, SavedViewMenu, type SavedViewTarget } from './saved-view-editor'
import type { ViewVisual } from '@/components/views/view-icon-picker'
import {
  ISSUE_FILTER_LABELS, applyExplorerFilters, executeExplorerBulkAction, explorerBulkOptions, explorerFilterOptions,
  explorerPropertyOptions, explorerUpdateForAction, explorerUpdateForProperty, issueToExplorerRow, optimisticExplorerRow,
  stateIdForExplorerGroup, withMapKey, withoutMapKey,
} from './issue-explorer-model'

export interface IssueExplorerPageProps {
  data: BootstrapData
  initialLabelId?: string
  scope: { kind: 'team'; team: Team } | { kind: 'workspace' }
  view: TeamIssuesRouteView
  viewHref: (view: TeamIssuesRouteView) => string
  savedView?: SavedView
  duplicateFrom?: SavedView
  creatingView?: boolean
  editingView?: boolean
  defaultSaveScope?: SavedView['scope']
  savedViews?: SavedView[]
  savedViewHref?: (view: SavedView) => string
  onNavigateView: (view: TeamIssuesRouteView) => void
  onNavigateSavedView?: (view: SavedView) => void
  onCreateSavedView?: (input: SavedViewMutationInput) => Promise<SavedView>
  onUpdateSavedView?: (viewId: string, input: SavedViewMutationInput) => Promise<SavedView>
  onDeleteSavedView?: (view: SavedView) => Promise<void>
  onToggleSavedViewFavorite?: (view: SavedView) => Promise<void>
  onSetSavedViewSubscriptionEvents?: (view: SavedView, events: string[]) => Promise<void>
  onDuplicateSavedView?: (view: SavedView) => void
  onCancelCreateSavedView?: () => void
  onBeginEditSavedView?: () => void
  onFinishEditSavedView?: () => void
  onNewViewResourceChange?: (resource: 'issues' | 'projects') => void
  onOpenIssue: (issue: Issue) => void
  renderIssuePreview?: (issue: Issue, onClose: () => void) => ReactNode
  onOpenSidebar?: () => void
  onCreateIssue?: (stateId?: string) => void
  onUpdateIssue: (issueId: string, input: IssueUpdateInput) => Promise<Issue>
  onUpdateIssues: (issueIds: string[], input: IssueUpdateInput) => Promise<Issue[]>
  onDeleteIssues: (issueIds: string[]) => Promise<void>
}

export function IssueExplorerPage({ data, initialLabelId, scope, view, viewHref, savedView, duplicateFrom, creatingView = false, editingView = false, defaultSaveScope, savedViews = [], savedViewHref, onNavigateView, onNavigateSavedView, onCreateSavedView, onUpdateSavedView, onDeleteSavedView, onToggleSavedViewFavorite, onSetSavedViewSubscriptionEvents, onDuplicateSavedView, onCancelCreateSavedView, onBeginEditSavedView, onFinishEditSavedView, onNewViewResourceChange, onOpenIssue, renderIssuePreview, onOpenSidebar, onCreateIssue, onUpdateIssue, onUpdateIssues, onDeleteIssues }: IssueExplorerPageProps) {
  const storageScope = scope.kind === 'team' ? `team:${scope.team.id}` : 'workspace'
  const preferencesKey = `${data.workspace.urlKey}:issue-explorer:${storageScope}:${view}`
  const sourceView = savedView ?? duplicateFrom
  const [filters, setFilters] = useState<MyIssuesAppliedFilter[]>(() => sourceView ? filtersFromSavedView(sourceView) : initialLabelFilter(data, initialLabelId) ?? readFilters(`${preferencesKey}:filters`))
  const [display, setDisplay] = useState<MyIssuesDisplayOptions>(() => sourceView ? displayFromSavedView(sourceView, view) : readDisplay(`${preferencesKey}:display`, view))
  const [detailsOpen, setDetailsOpen] = useState(() => savedView ? true : readBoolean(`${data.workspace.urlKey}:issue-explorer:${storageScope}:details`, false))
  const [detailsWidth, setDetailsWidth] = useState(350)
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set())
  const [filterOpenSignal, setFilterOpenSignal] = useState(0)
  const [mutationErrors, setMutationErrors] = useState<Map<string, string>>(new Map())
  const [rowOverrides, setRowOverrides] = useState<Map<string, MyIssuesRowData>>(new Map())
  const [manualOrder, setManualOrder] = useState<string[]>(() => readOrder(`${preferencesKey}:order`))
  const [previewIssueId, setPreviewIssueId] = useState<string>()
  const [viewEditor, setViewEditor] = useState<'create' | 'edit' | undefined>(creatingView ? 'create' : editingView ? 'edit' : undefined)
  const [viewSaving, setViewSaving] = useState(false)
  const mutationSequence = useRef(new Map<string, number>())
  const mutationQueues = useRef(new Map<string, Promise<Issue>>())
  const retryUpdates = useRef(new Map<string, IssueUpdateInput>())
  const savedViewFavorite = Boolean(savedView && (savedView.favorite || data.favorites.some(item => item.userId === data.viewer.id && item.resourceType === 'view' && item.resourceId === savedView.id)))
  const savedViewSubscribed = Boolean(savedView && (savedView.subscribed || data.subscriptions.some(item => item.userId === data.viewer.id && item.resourceType === 'view' && item.resourceId === savedView.id)))
  const savedViewSubscription = savedView ? data.subscriptions.find(item => item.userId === data.viewer.id && item.resourceType === 'view' && item.resourceId === savedView.id) : undefined
  const savedViewSubscriptionEvents = savedViewSubscription?.events?.length ? savedViewSubscription.events : savedViewSubscribed ? ['issue-added', 'issue-completed'] : []

  const scopedIssues = useMemo(() => issuesForScope(data.issues, scope, view), [data.issues, scope, view])
  const issuesById = useMemo(() => new Map(data.issues.map(issue => [issue.id, issue])), [data.issues])
  const rowOptions = useMemo(() => explorerPropertyOptions(data, scopedIssues), [data, scopedIssues])
  const visibleIssues = useMemo(() => applyExplorerFilters(scopedIssues, filters), [filters, scopedIssues])
  const rows = useMemo(() => visibleIssues.map(issue => rowOverrides.get(issue.id) ?? issueToExplorerRow(issue, data.workspace.urlKey)), [data.workspace.urlKey, rowOverrides, visibleIssues])
  const groups = useMemo(() => projectGroups(rows, display, data, view, manualOrder), [data, display, manualOrder, rows, view])
  const selection = useMyIssuesSelection(groups)
  const summary = useMemo(() => deriveSummary(groups), [groups])
  const previewIssue = previewIssueId ? issuesById.get(previewIssueId) : undefined
  const saveTargets = useMemo<SavedViewTarget[]>(() => [
    { scope: 'personal', label: 'Personal' },
    { scope: 'workspace', label: 'Workspace' },
    ...data.teams.map(team => ({ scope: 'team' as const, label: team.name, teamId: team.id })),
  ], [data.teams])
  const initialSaveTarget = saveTargets.find(target => target.scope === (sourceView?.scope ?? defaultSaveScope ?? scope.kind) && (target.scope !== 'team' || target.teamId === (sourceView?.teamId ?? (scope.kind === 'team' ? scope.team.id : undefined)))) ?? saveTargets[0]

  useEffect(() => {
    if (!savedView) return
    setFilters(filtersFromSavedView(savedView))
    setDisplay(displayFromSavedView(savedView, savedView.view))
  }, [savedView])

  useEffect(() => { if (!detailsOpen) setPreviewIssueId(undefined) }, [detailsOpen])

  useEffect(() => {
    if (creatingView) setViewEditor('create')
    else if (editingView) setViewEditor('edit')
  }, [creatingView, editingView])

  useEffect(() => {
    setRowOverrides(current => {
      if (!current.size) return current
      const next = new Map(current)
      for (const issue of data.issues) if (next.get(issue.id)?.updatedAt === issue.updatedAt) next.delete(issue.id)
      return next.size === current.size ? current : next
    })
  }, [data.issues])

  const persistFilters = (next: MyIssuesAppliedFilter[]) => { setFilters(next); writeValue(`${preferencesKey}:filters`, JSON.stringify(next)) }
  const changeDisplay = (next: MyIssuesDisplayOptions) => { setDisplay(next); writeValue(`${preferencesKey}:display`, JSON.stringify({ ...next, properties: [...next.properties] })) }
  const changeDetails = (open: boolean) => { setDetailsOpen(open); writeValue(`${data.workspace.urlKey}:issue-explorer:${storageScope}:details`, String(open)) }
  const openIssueFromExplorer = (row: MyIssuesRowData) => {
    const issue = issuesById.get(row.id)
    if (!issue) return
    if (detailsOpen && renderIssuePreview) setPreviewIssueId(issue.id)
    else onOpenIssue(issue)
  }
  const addFilter = (field: MyIssuesFilterKey, option: MyIssuesFilterOption) => {
    const label = ISSUE_FILTER_LABELS[field]
    if (label) persistFilters(toggleFilterOption(filters, field, label, option))
  }

  const updateOne = async (row: MyIssuesRowData, input: IssueUpdateInput) => {
    const sequence = (mutationSequence.current.get(row.id) ?? 0) + 1
    mutationSequence.current.set(row.id, sequence); retryUpdates.current.set(row.id, input)
    const snapshot = rowOverrides.get(row.id)
    setRowOverrides(current => new Map(current).set(row.id, optimisticExplorerRow(row, input, data)))
    setMutationErrors(current => withoutMapKey(current, row.id))
    const prior = mutationQueues.current.get(row.id)
    const request = (prior ? prior.catch(() => undefined) : Promise.resolve()).then(() => onUpdateIssue(row.id, input))
    mutationQueues.current.set(row.id, request)
    try {
      const updated = await request
      if (mutationSequence.current.get(row.id) === sequence) {
        setRowOverrides(current => new Map(current).set(row.id, issueToExplorerRow(updated, data.workspace.urlKey)))
        retryUpdates.current.delete(row.id)
      }
      return updated
    } catch (failure) {
      if (mutationSequence.current.get(row.id) === sequence) {
        setRowOverrides(current => { const next = new Map(current); if (snapshot) next.set(row.id, snapshot); else next.delete(row.id); return next })
        setMutationErrors(current => withMapKey(current, row.id, failure instanceof Error ? failure.message : 'Could not update issue'))
      }
      throw failure
    } finally { if (mutationQueues.current.get(row.id) === request) mutationQueues.current.delete(row.id) }
  }
  const changeProperty = (row: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => {
    const update = explorerUpdateForProperty(property, value)
    if (!update) return
    void updateOne(row, update).catch(() => undefined)
  }
  const contextAction = async (row: MyIssuesRowData, action: MyIssuesContextAction) => {
    if (action === 'delete') { if (window.confirm(`Delete ${row.identifier}? This cannot be undone.`)) await onDeleteIssues([row.id]); return }
    if (action === 'copy') { await navigator.clipboard.writeText(`${location.origin}${row.href}`); return }
    const update = explorerUpdateForAction(action)
    if (update) await updateOne(row, update)
  }
  const summaryFilter = (tab: MyIssuesSummaryTab, item: MyIssuesSummaryItem) => addFilter(tab === 'labels' ? 'labels' : tab === 'projects' ? 'project' : 'priority', { id: item.id, label: item.label, color: item.color })
  const moveIssue = (row: MyIssuesRowData, sourceGroupId: string, targetGroupId: string, targetIndex: number) => {
    const targetGroup = groups.find(group => group.id === targetGroupId)
    if (!targetGroup) return
    const visibleOrder = groups.flatMap(group => group.issues).map(issue => issue.id)
    const baseOrder = [...manualOrder, ...visibleOrder.filter(id => !manualOrder.includes(id))]
    const without = baseOrder.filter(id => id !== row.id)
    const targetIssues = targetGroup.issues.filter(issue => issue.id !== row.id)
    const targetIds = targetIssues.map(issue => issue.id)
    const beforeId = targetIds[targetIndex]
    const afterId = targetIds[targetIndex - 1]
    const insertAt = beforeId ? without.indexOf(beforeId) : afterId ? without.indexOf(afterId) + 1 : without.length
    const nextOrder = [...without.slice(0, Math.max(0, insertAt)), row.id, ...without.slice(Math.max(0, insertAt))]
    setManualOrder(nextOrder); writeValue(`${preferencesKey}:order`, JSON.stringify(nextOrder))
    const targetStateId = data.states.some(state => state.id === targetGroupId) ? targetGroupId : undefined
    const sortOrder = targetIndex === 0 ? (targetIssues[0]?.sortOrder ?? 1) - 1 : targetIndex >= targetIds.length ? (targetIssues.at(-1)?.sortOrder ?? 0) + 1 : ((targetIssues[targetIndex - 1]?.sortOrder ?? 0) + (targetIssues[targetIndex]?.sortOrder ?? 0)) / 2
    const update: IssueUpdateInput = { sortOrder }
    if (targetStateId && sourceGroupId !== targetGroupId) update.stateId = targetStateId
    void updateOne(row, update).catch(() => undefined)
  }
  const savedViewSnapshot = (): SavedViewMutationInput => ({ resource: 'issues', scope: scope.kind, teamId: scope.kind === 'team' ? scope.team.id : '', ownerId: data.viewer.id, view, filters, display: displaySnapshot(display) })
  const saveViewEditor = async (name: string, description: string, target: SavedViewTarget | undefined, visual: ViewVisual) => {
    if (viewSaving) return
    setViewSaving(true)
    try {
      if (viewEditor === 'edit' && savedView && onUpdateSavedView) await onUpdateSavedView(savedView.id, { ...savedViewSnapshot(), name, description, ...visual })
      else if (onCreateSavedView) {
        const destination = target ?? initialSaveTarget
        const created = await onCreateSavedView({ ...savedViewSnapshot(), name, description, ...visual, scope: destination.scope, teamId: destination.scope === 'team' ? destination.teamId : '' })
        onNavigateSavedView?.(created)
      }
      setViewEditor(undefined)
      if (viewEditor === 'edit') onFinishEditSavedView?.()
    } finally { setViewSaving(false) }
  }

  return <>
    <IssueExplorerSurface
      scopeName={scope.kind === 'team' ? scope.team.name : data.workspace.name}
      creatingView={creatingView}
      scopeHref={scope.kind === 'team' ? `/${data.workspace.urlKey}/team/${scope.team.key}/overview` : undefined}
      activeView={view}
      viewHref={viewHref}
      savedView={savedView}
      favorite={savedViewFavorite}
      savedViews={savedViews}
      savedViewHref={savedViewHref}
      filters={filters}
      displayOptions={display}
      detailsOpen={detailsOpen}
      itemCount={rows.length}
      filterOpenSignal={filterOpenSignal}
      filterOptions={field => explorerFilterOptions(field, rowOptions)}
      onFilterToggle={addFilter}
      onDisplayOptionsChange={changeDisplay}
      onDetailsOpenChange={changeDetails}
      onNavigateView={onNavigateView}
      onNewViewResourceChange={onNewViewResourceChange}
      onAddView={() => setViewEditor('create')}
      onSavedViewSelect={onNavigateSavedView}
      onToggleFavorite={() => { if (savedView && onToggleSavedViewFavorite) void onToggleSavedViewFavorite(savedView) }}
      viewActions={savedView && <SavedViewMenu
        view={savedView}
        users={data.users}
        teams={data.teams}
        subscriptionEvents={savedViewSubscriptionEvents}
        onEdit={() => { setViewEditor('edit'); onBeginEditSavedView?.() }}
        onDuplicate={onDuplicateSavedView ? () => onDuplicateSavedView(savedView) : undefined}
        onUpdate={onUpdateSavedView ? input => { void onUpdateSavedView(savedView.id, input) } : undefined}
        onSetSubscriptionEvents={onSetSavedViewSubscriptionEvents ? events => { void onSetSavedViewSubscriptionEvents(savedView, events) } : undefined}
        onCopy={() => { void navigator.clipboard.writeText(window.location.href) }}
        onExport={() => exportIssuesCsv(rows, savedView.name)}
        onDelete={() => { if (onDeleteSavedView && window.confirm(`Delete view "${savedView.name}"?`)) void onDeleteSavedView(savedView) }}
      />}
      onOpenSidebar={onOpenSidebar}
      viewEditor={viewEditor && <SavedViewEditor
        initialName={viewEditor === 'edit' ? savedView?.name : duplicateFrom?.name ?? ''}
        namePlaceholder="All issues"
        initialDescription={viewEditor === 'edit' ? savedView?.description : duplicateFrom?.description ?? ''}
        initialIcon={viewEditor === 'edit' ? savedView?.icon : duplicateFrom?.icon}
        initialColor={viewEditor === 'edit' ? savedView?.color : duplicateFrom?.color}
        initialTarget={initialSaveTarget}
        saveTargets={viewEditor === 'create' ? saveTargets : []}
        saving={viewSaving}
        onCancel={() => { setViewEditor(undefined); if (creatingView) onCancelCreateSavedView?.(); else if (viewEditor === 'edit') onFinishEditSavedView?.() }}
        onSave={(name, description, target, visual) => { void saveViewEditor(name, description, target, visual) }}
      />}
      filterBar={<MyIssuesFilterBar filters={filters} filterOptions={filter => explorerFilterOptions(filter.field, rowOptions)} onAdd={() => setFilterOpenSignal(value => value + 1)} onClear={() => persistFilters([])} onOperatorChange={(id, operator) => persistFilters(updateFilterOperator(filters, id, operator))} onRemove={id => persistFilters(filters.filter(filter => filter.id !== id))} onValuesChange={(id, options) => persistFilters(updateFilterValues(filters, id, options))}/>}>
      {display.layout === 'list' ? <MyIssuesList
        groups={groups}
        selectedIds={selection.selectedIds}
        collapsedGroupIds={collapsedGroups}
        displayProperties={display.properties}
        nestedSubIssues={display.nestedSubIssues}
        propertyOptions={rowOptions}
        mutationErrors={mutationErrors}
        onCreateIssue={group => onCreateIssue?.(stateIdForExplorerGroup(group, data))}
        onGroupCollapsedChange={(id, collapsed) => setCollapsedGroups(current => { const next = new Set(current); if (collapsed) next.add(id); else next.delete(id); return next })}
        onOpenIssue={openIssueFromExplorer}
        onPropertyChange={changeProperty}
        onRetryMutation={row => { const input = retryUpdates.current.get(row.id); if (input) void updateOne(row, input).catch(() => undefined) }}
        onSelectIssue={selection.selectIssue}
        onContextAction={(row, action) => { void contextAction(row, action) }}
      /> : <IssueBoard groups={groups} properties={display.properties} selectedIds={selection.selectedIds} onCreateIssue={group => onCreateIssue?.(stateIdForExplorerGroup(group, data))} onMove={moveIssue} onOpenIssue={openIssueFromExplorer} onSelectIssue={selection.selectIssue}/>}
      <MyIssuesDetailsPane open={detailsOpen} width={detailsWidth} onWidthChange={setDetailsWidth} onClose={() => { if (previewIssueId) setPreviewIssueId(undefined); else changeDetails(false) }} selectedIssue={previewIssue ? issueToExplorerRow(previewIssue, data.workspace.urlKey) : undefined} previewContent={previewIssue && renderIssuePreview ? renderIssuePreview(previewIssue, () => setPreviewIssueId(undefined)) : undefined} summary={summary} onSummaryItemSelect={summaryFilter}/>
    </IssueExplorerSurface>
    <MyIssuesBulkActionBar selectedIssues={selection.selectedIssues} actionOptions={action => explorerBulkOptions(action, rowOptions)} onAction={(action, _issues, value) => { void executeExplorerBulkAction({ action, ids: selection.selectedIssues.map(issue => issue.id), value, data, issuesById, onUpdateIssue, onUpdateIssues }).then(() => selection.clearSelection()) }} onClear={selection.clearSelection}/>
  </>
}

function exportIssuesCsv(rows: MyIssuesRowData[], name: string) {
  const quote = (value: unknown) => `"${String(value ?? '').replaceAll('"', '""')}"`
  const csv = [
    ['Identifier', 'Title', 'Status', 'Priority', 'Assignee', 'Project', 'Due date'],
    ...rows.map(row => [row.identifier, row.title, row.state.name, row.priority, row.assignee?.name ?? '', row.project?.name ?? '', row.dueDate ?? '']),
  ].map(line => line.map(quote).join(',')).join('\n')
  const url = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }))
  const link = document.createElement('a')
  link.href = url; link.download = `${name.replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '') || 'view'}.csv`; link.click()
  URL.revokeObjectURL(url)
}

function issuesForScope(issues: Issue[], scope: IssueExplorerPageProps['scope'], view: TeamIssuesRouteView) {
  return issues.filter(issue => !issue.archivedAt && (scope.kind === 'workspace' || issue.team.id === scope.team.id) && (view === 'all' || (view === 'backlog' ? issue.state.type === 'backlog' : issue.state.type === 'unstarted' || issue.state.type === 'started')))
}

function projectGroups(issues: MyIssuesRowData[], display: MyIssuesDisplayOptions, data: BootstrapData, view: TeamIssuesRouteView, manualOrder: string[]): MyIssuesGroupData[] {
  let projected = issues.filter(issue => display.showSubIssues || !issue.parentId)
  if (view !== 'all') projected = projected.filter(issue => issue.state.type !== 'completed' && issue.state.type !== 'canceled')
  else if (display.completedWindow === 'none') projected = projected.filter(issue => issue.state.type !== 'completed' && issue.state.type !== 'canceled')
  projected = [...projected].sort(issueComparator(display.ordering, manualOrder))
  if (display.grouping === 'none') return [{ id: 'all-issues', label: 'All issues', issues: projected }]
  const groups = new Map<string, MyIssuesGroupData>()
  for (const issue of projected) {
    const descriptor = groupForIssue(issue, display.grouping)
    const group = groups.get(descriptor.id) ?? { ...descriptor, issues: [] }
    group.issues.push(issue); groups.set(group.id, group)
  }
  if (display.layout === 'board' && display.grouping === 'status' && display.showEmptyGroups) {
    for (const state of data.states.filter(state => stateVisibleInView(state.type, view, display.completedWindow))) {
      if (!groups.has(state.id)) groups.set(state.id, { id: state.id, label: state.name, stateType: state.type, issues: [] })
    }
  }
  const stateOrder = new Map(data.states.map((state, index) => [state.id, index]))
  const ordered = [...groups.values()].sort((left, right) => {
    if (display.grouping !== 'status' && display.grouping !== 'focus') return left.label.localeCompare(right.label)
    return (stateOrder.get(left.id) ?? 99) - (stateOrder.get(right.id) ?? 99)
  })
  return display.groupOrder === 'desc' ? ordered.reverse() : ordered
}

function stateVisibleInView(type: string, view: TeamIssuesRouteView, completedWindow: MyIssuesDisplayOptions['completedWindow']) {
  if (view === 'active') return type === 'unstarted' || type === 'started'
  if (view === 'backlog') return type === 'backlog'
  return completedWindow !== 'none' || (type !== 'completed' && type !== 'canceled')
}

function groupForIssue(issue: MyIssuesRowData, grouping: MyIssuesGrouping): Omit<MyIssuesGroupData, 'issues'> {
  if (grouping === 'status' || grouping === 'focus') return { id: issue.state.id, label: issue.state.name, stateType: issue.state.type }
  if (grouping === 'priority') return { id: `priority-${issue.priority}`, label: ['No priority', 'Urgent', 'High', 'Medium', 'Low'][issue.priority] }
  if (grouping === 'project') return { id: `project-${issue.project?.id ?? 'none'}`, label: issue.project?.name ?? 'No project' }
  if (grouping === 'assignee') return { id: `assignee-${issue.assignee?.id ?? 'none'}`, label: issue.assignee?.name ?? 'No assignee' }
  if (grouping === 'label') { const label = issue.labels?.[0]; return { id: `label-${label?.id ?? 'none'}`, label: label?.name ?? 'No label' } }
  return { id: `${grouping}-none`, label: grouping[0].toUpperCase() + grouping.slice(1) }
}

function issueComparator(ordering: MyIssuesDisplayOptions['ordering'], manualOrder: string[]) {
  const manual = new Map(manualOrder.map((id, index) => [id, index]))
  return (left: MyIssuesRowData, right: MyIssuesRowData) => {
    if (manual.has(left.id) || manual.has(right.id)) return (manual.get(left.id) ?? 999999) - (manual.get(right.id) ?? 999999)
    if (ordering === 'created') return Date.parse(right.createdAt) - Date.parse(left.createdAt)
    if (ordering === 'updated') return Date.parse(right.updatedAt) - Date.parse(left.updatedAt)
    return (left.sortOrder ?? 0) - (right.sortOrder ?? 0) || left.priority - right.priority || Date.parse(right.updatedAt) - Date.parse(left.updatedAt)
  }
}

function deriveSummary(groups: MyIssuesGroupData[]): MyIssuesDetailsSummary {
  const issues = groups.flatMap(group => group.issues)
  return {
    labels: countItems(issues.flatMap(issue => issue.labels ?? []).map(item => ({ id: item.id, label: item.name, color: item.color }))),
    priority: countItems(issues.map(issue => ({ id: String(issue.priority), label: ['No priority', 'Urgent', 'High', 'Medium', 'Low'][issue.priority] }))),
    projects: countItems(issues.filter(issue => issue.project).map(issue => ({ id: issue.project!.id, label: issue.project!.name, color: issue.project!.color }))),
  }
}
function countItems(items: Omit<MyIssuesSummaryItem, 'count'>[]) { const values = new Map<string, MyIssuesSummaryItem>(); for (const item of items) values.set(item.id, { ...item, count: (values.get(item.id)?.count ?? 0) + 1 }); return [...values.values()].sort((a, b) => b.count - a.count) }
function defaultDisplay(view: TeamIssuesRouteView): MyIssuesDisplayOptions { return { ...defaultMyIssuesDisplayOptions, grouping: 'status', completedWindow: view === 'all' ? 'all' : 'none', properties: new Set(defaultMyIssuesDisplayOptions.properties) } }
function initialLabelFilter(data: BootstrapData, labelId?: string): MyIssuesAppliedFilter[] | undefined {
  if (!labelId) return
  const label = data.labels.find(item => item.id === labelId)
  if (!label) return
  const value = { value: label.id, valueLabel: label.name, color: label.color }
  return [{ id: `labels-${label.id}`, field: 'labels', fieldLabel: 'Labels', operator: 'is', ...value, values: [value] }]
}
function readFilters(key: string): MyIssuesAppliedFilter[] { try { const value = JSON.parse(localStorage.getItem(key) ?? '[]'); return Array.isArray(value) ? value : [] } catch { return [] } }
function readDisplay(key: string, view: TeamIssuesRouteView): MyIssuesDisplayOptions { const fallback = defaultDisplay(view); try { const value = JSON.parse(localStorage.getItem(key) ?? 'null'); return value ? { ...fallback, ...value, properties: new Set(Array.isArray(value.properties) ? value.properties : [...fallback.properties]) } : fallback } catch { return fallback } }
function readBoolean(key: string, fallback: boolean) { try { const value = localStorage.getItem(key); return value == null ? fallback : value === 'true' } catch { return fallback } }
function writeValue(key: string, value: string) { try { localStorage.setItem(key, value) } catch { /* Preferences are best-effort. */ } }
function readOrder(key: string): string[] { try { const value = JSON.parse(localStorage.getItem(key) ?? '[]'); return Array.isArray(value) && value.every(item => typeof item === 'string') ? value : [] } catch { return [] } }
function filtersFromSavedView(view: SavedView): MyIssuesAppliedFilter[] { return Array.isArray(view.filters) ? view.filters as MyIssuesAppliedFilter[] : [] }
function displayFromSavedView(view: SavedView, routeView: TeamIssuesRouteView): MyIssuesDisplayOptions {
  const fallback = defaultDisplay(routeView)
  const value = view.display && typeof view.display === 'object' ? view.display : {}
  return { ...fallback, ...value, properties: new Set(Array.isArray(value.properties) ? value.properties as MyIssuesProperty[] : [...fallback.properties]) } as MyIssuesDisplayOptions
}
function displaySnapshot(display: MyIssuesDisplayOptions): Record<string, unknown> { return { ...display, properties: [...display.properties] } }

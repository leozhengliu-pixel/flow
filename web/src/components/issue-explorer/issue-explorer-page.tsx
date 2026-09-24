import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { IssueRowActionsProvider } from '@/components/my-issues/issue-row-actions'
import { useActionGroupsForSelection } from '@/hooks/use-action-groups-for-selection'
import { clearSelectedModels, setSelectedModels } from '@/lib/selected-models-store'
import { boundedIssueSequence } from '@/lib/navigation-context'
import { teamHierarchy } from '@/lib/team-hierarchy'
import type { BootstrapData, Issue, IssueUpdateInput, SavedView, SavedViewMutationInput, Team } from '@/types/flow'
import type { TeamIssuesRouteView } from '@/lib/app-routes'
import { MyIssuesBulkActionBar } from '@/components/my-issues/my-issues-bulk-action-bar'
import { MyIssuesDetailsPane, type MyIssuesDetailsSummary, type MyIssuesSummaryItem, type MyIssuesSummaryTab } from '@/components/my-issues/my-issues-details-pane'
import { MyIssuesFilterBar, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-bar'
import { MyIssuesList, type MyIssuesContextAction, type MyIssuesCreateContext, type MyIssuesEditableProperty, type MyIssuesGroupData, type MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { defaultMyIssuesDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import type { MyIssuesDisplayOptions, MyIssuesFilterKey, MyIssuesFilterOption, MyIssuesProperty } from '@/components/my-issues/my-issues-surface'
import { useMyIssuesSelection } from '@/components/my-issues/use-my-issues-state'
import { issueFiltersToQueryAst, toggleFilterOption, updateFilterOperator, updateFilterValues } from '@/components/my-issues/my-issues-filter-types'
import { PagedIssueList } from './paged-issue-list'
import { fetchIssueRecord } from '@/lib/api'
import { toast } from 'sonner'
import { IssueExplorerSurface } from './issue-explorer-surface'
import { IssueBoard } from './issue-board'
import { SavedViewEditor, SavedViewMenu, type SavedViewTarget } from './saved-view-editor'
import { EditCustomViewHeader } from './edit-custom-view-header'
import { filtersAreDirty, SaveCustomViewButtons } from './save-custom-view-buttons'
import { discardCustomViewDraft } from './custom-view-draft'
import { InsightHiddenNotice, SavedViewDetailsPanel, SavedViewInsightsPanel, type SavedViewInsightsConfig } from './saved-view-panels'
import { confirmAction } from '@/components/ui/action-dialog-service'
import type { ViewVisual } from '@/components/views/view-icon-picker'
import {
  ISSUE_FILTER_LABELS, applyExplorerFilters, buildExplorerIssueGroups, executeExplorerBulkAction, explorerBoardGroupUpdate, explorerBulkOptions, explorerFilterOptions,
  explorerPropertyOptions, explorerUpdateForAction, explorerUpdateForProperty, issueToExplorerRow, optimisticExplorerRow,
  stateIdForExplorerGroup, withMapKey, withoutMapKey,
} from './issue-explorer-model'
import { IssuesSplitLayout, IssueViewSplitPage } from '@/components/issues-split-view'
import { PAGED_GROUPINGS, PAGED_ORDERINGS, pagedDisplayQuery } from './issue-grouping'

export interface IssueExplorerPageProps {
  data: BootstrapData
  initialLabelId?: string
  initialStatusId?: string
  initialInsightFilters?: { teamIds?: string[]; stateIds?: string[]; assigneeIds?: string[]; labelIds?: string[] }
  scope: { kind: 'team'; team: Team } | { kind: 'workspace' }
  view: TeamIssuesRouteView
  /** `/team/:key/board`: the same view with its own board-layout preferences. */
  boardRoute?: boolean
  /** Overrides the localStorage preference scope (label pages, member profiles…). */
  preferenceScope?: string
  /** Titled header for resource-scoped issue views. */
  resourceHeader?: { icon?: ReactNode; title: ReactNode; actions?: ReactNode; tabs?: { id: string; label: string; href: string; active: boolean; onSelect: () => void }[] }
  /** Extra row predicate applied before filters (for example issues of one member). */
  scopeFilter?: (issue: Issue) => boolean
  /** Server query equivalent of `scopeFilter` for paged workspaces. */
  scopeConditions?: Record<string, unknown>[]
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
  onShareSavedView?: (view: SavedView) => Promise<string | undefined>
  onDuplicateSavedView?: (view: SavedView) => void
  onCancelCreateSavedView?: () => void
  onBeginEditSavedView?: () => void
  onFinishEditSavedView?: () => void
  onNewViewResourceChange?: (resource: 'issues' | 'projects') => void
  onOpenIssue: (issue: Issue, sequence?: string[]) => void
  renderIssuePreview?: (issue: Issue, onClose: () => void) => ReactNode
  onOpenSidebar?: () => void
  onCreateIssue?: (context?: MyIssuesCreateContext) => void
  onUpdateIssue: (issueId: string, input: IssueUpdateInput) => Promise<Issue>
  onUpdateIssues: (issueIds: string[], input: IssueUpdateInput) => Promise<Issue[]>
  onDeleteIssues: (issueIds: string[]) => Promise<void>
}

export function IssueExplorerPage({ boardRoute = false, preferenceScope, resourceHeader, scopeFilter, scopeConditions, data, initialLabelId, initialStatusId, initialInsightFilters, scope, view, viewHref, savedView, duplicateFrom, creatingView = false, editingView = false, defaultSaveScope, savedViews = [], savedViewHref, onNavigateView, onNavigateSavedView, onCreateSavedView, onUpdateSavedView, onDeleteSavedView, onToggleSavedViewFavorite, onSetSavedViewSubscriptionEvents, onShareSavedView, onDuplicateSavedView, onCancelCreateSavedView, onBeginEditSavedView, onFinishEditSavedView, onNewViewResourceChange, onOpenIssue, renderIssuePreview, onOpenSidebar, onCreateIssue, onUpdateIssue, onUpdateIssues, onDeleteIssues }: IssueExplorerPageProps) {
  const storageScope = scope.kind === 'team' ? `team:${scope.team.id}` : 'workspace'
  const preferencesKey = `${data.workspace.urlKey}:issue-explorer:${preferenceScope ?? storageScope}:${boardRoute ? 'board' : view}`
  const sourceView = savedView ?? duplicateFrom
  const [filters, setFilters] = useState<MyIssuesAppliedFilter[]>(() => sourceView ? filtersFromSavedView(sourceView) : initialInsightFilters ? insightPropertyFilters(data, initialInsightFilters) : initialPropertyFilters(data, initialLabelId, initialStatusId) ?? readFilters(`${preferencesKey}:filters`))
  // Saved views keep a personal display layer on top of the view default (Linear "Reset to view default").
  const personalViewKey = savedView ? `${data.workspace.urlKey}:issue-explorer:view:${savedView.id}:display` : undefined
  const [display, setDisplay] = useState<MyIssuesDisplayOptions>(() => personalViewKey ? readPersonalDisplay(personalViewKey, savedView!, view) : duplicateFrom ? displayFromSavedView(duplicateFrom, view) : readDisplay(`${preferencesKey}:display`, view, boardRoute))
  const [detailsOpen, setDetailsOpen] = useState(() => readBoolean(`${data.workspace.urlKey}:issue-explorer:${storageScope}:details`, false))
  const [insightsOpen, setInsightsOpen] = useState(false)
  const [drillRows, setDrillRows] = useState<MyIssuesRowData[]>()
  const [draftInsights, setDraftInsights] = useState<SavedViewInsightsConfig>()
  const [detailsWidth, setDetailsWidth] = useState(350)
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set())
  const [filterOpenSignal, setFilterOpenSignal] = useState(0)
  const [mutationErrors, setMutationErrors] = useState<Map<string, string>>(new Map())
  const [rowOverrides, setRowOverrides] = useState<Map<string, MyIssuesRowData>>(new Map())
  const [manualOrder, setManualOrder] = useState<string[]>(() => readOrder(`${preferencesKey}:order`))
  const [previewIssueId, setPreviewIssueId] = useState<string>()
  const [pagedTotal, setPagedTotal] = useState(0)
  const [pagedIssues, setPagedIssues] = useState<Issue[]>([])
  const [viewEditor, setViewEditor] = useState<'create' | 'edit' | undefined>(creatingView ? 'create' : editingView ? 'edit' : undefined)
  const [viewSaving, setViewSaving] = useState(false)
  const hydratedSavedViewId = useRef(savedView?.id)
  const mutationSequence = useRef(new Map<string, number>())
  const mutationQueues = useRef(new Map<string, Promise<Issue>>())
  const retryUpdates = useRef(new Map<string, IssueUpdateInput>())
  const savedViewFavorite = Boolean(savedView && (savedView.favorite || data.favorites.some(item => item.userId === data.viewer.id && item.resourceType === 'view' && item.resourceId === savedView.id)))
  const savedViewSubscribed = Boolean(savedView && (savedView.subscribed || data.subscriptions.some(item => item.userId === data.viewer.id && item.resourceType === 'view' && item.resourceId === savedView.id)))
  const savedViewSubscription = savedView ? data.subscriptions.find(item => item.userId === data.viewer.id && item.resourceType === 'view' && item.resourceId === savedView.id) : undefined
  const savedViewSubscriptionEvents = savedViewSubscription?.events?.length ? savedViewSubscription.events : savedViewSubscribed ? ['issue-added', 'issue-completed'] : []

  useEffect(() => { const onKey = (event: KeyboardEvent) => { if (!event.altKey || event.metaKey || event.ctrlKey || event.key.toLowerCase() !== 'v' || savedView || creatingView || viewEditor || (event.target as HTMLElement | null)?.closest('input,textarea,[contenteditable=true],[role=textbox]')) return; event.preventDefault(); setViewEditor('create') }; addEventListener('keydown', onKey); return () => removeEventListener('keydown', onKey) }, [creatingView, savedView, viewEditor])

  const showSubTeams = display.showSubTeamIssues !== false
  const scopeTeamIds = useMemo(() => scope.kind === 'team' ? (showSubTeams ? teamHierarchy(data.teams, data.teamSettings).subtree(scope.team.id) : new Set([scope.team.id])) : undefined, [data.teams, data.teamSettings, scope, showSubTeams])
  const scopedIssues = useMemo(() => filterInsightTeams(issuesForScope(data.issues, scope, view, Boolean(display.showArchived), scopeTeamIds), initialInsightFilters?.teamIds).filter(issue => !scopeFilter || scopeFilter(issue)), [data.issues, display.showArchived, initialInsightFilters?.teamIds, scope, scopeFilter, scopeTeamIds, view])
  const insightIssues = useMemo(() => insightsOpen ? filterInsightTeams(issuesForScope(data.issues, scope, view, true, scopeTeamIds), initialInsightFilters?.teamIds) : [], [data.issues, initialInsightFilters?.teamIds, insightsOpen, scope, scopeTeamIds, view])
  const issuesById = useMemo(() => new Map([...data.issues, ...pagedIssues].map(issue => [issue.id, issue])), [data.issues, pagedIssues])
  const rowOptions = useMemo(() => explorerPropertyOptions(data, scopedIssues), [data, scopedIssues])
  // Bootstrap/sync owns the complete visible collection. Group before virtualizing;
  // replacing it with one query page truncates both group counts and membership.
  const visibleIssues = useMemo(() => data.issueCollectionPaged ? pagedIssues : applyExplorerFilters(scopedIssues, filters, data), [data, filters, scopedIssues, pagedIssues])
  const baseRows = useMemo(() => visibleIssues.map(issue => rowOverrides.get(issue.id) ?? issueToExplorerRow(issue, data.workspace.urlKey,data.issues,data)), [data, rowOverrides, visibleIssues])
  const rows = drillRows ?? baseRows
  const insightRows = useMemo(() => insightsOpen ? applyExplorerFilters(insightIssues, filters, data).map(issue => rowOverrides.get(issue.id) ?? issueToExplorerRow(issue, data.workspace.urlKey,data.issues,data)) : [], [data, filters, insightIssues, insightsOpen, rowOverrides])
  const activeInsightRows = useMemo(() => insightRows.filter(row => !row.archivedAt), [insightRows])
  const groups = useMemo(() => buildExplorerIssueGroups(rows, display, data, view, manualOrder), [data, display, manualOrder, rows, view])
  const pagedQuery = useMemo(() => {
    const { sort, direction, groupBy, archived, conditions } = pagedDisplayQuery(display)
    if (view === 'backlog') conditions.push({ field: 'status', operator: 'is', values: ['backlog'] })
    if (view === 'active') conditions.push({ field: 'status', operator: 'in', values: ['unstarted', 'started'] })
    const includeSubTeams = scope.kind === 'team' && display.showSubTeamIssues !== false
    return { teamId: scope.kind === 'team' ? scope.team.id : initialInsightFilters?.teamIds, includeSubTeams, archived, groupBy, sort, direction, filter: { and: [issueFiltersToQueryAst(filters), ...conditions, ...(scopeConditions ?? [])] } }
  }, [display, filters, initialInsightFilters?.teamIds, scope, scopeConditions, view])
  const insightQuery = useMemo(() => ({
    teamId: scope.kind === 'team' ? scope.team.id : initialInsightFilters?.teamIds,
    includeSubTeams: scope.kind === 'team',
    filter: { and: [issueFiltersToQueryAst(filters), ...(view === 'backlog' ? [{ field: 'status', values: ['backlog'] }] : view === 'active' ? [{ field: 'status', values: ['unstarted', 'started'] }] : [])] },
  }), [filters, initialInsightFilters?.teamIds, scope, view])
  const selection = useMyIssuesSelection(groups)
  useActionGroupsForSelection(['Issues', 'Projects'])
  useEffect(() => {
    if (selection.selectedIds.size > 0) setSelectedModels(['Issue'])
    else clearSelectedModels()
    return () => clearSelectedModels()
  }, [selection.selectedIds])
  const summary = useMemo(() => deriveSummary(groups), [groups])
  const previewIssue = previewIssueId ? issuesById.get(previewIssueId) : undefined
  const saveTargets = useMemo<SavedViewTarget[]>(() => [
    { scope: 'personal', label: 'Personal' },
    { scope: 'workspace', label: 'Workspace' },
    ...data.teams.map(team => ({ scope: 'team' as const, label: team.name, teamId: team.id, team })),
  ], [data.teams])
  const initialSaveTarget = saveTargets.find(target => target.scope === (sourceView?.scope ?? defaultSaveScope ?? scope.kind) && (target.scope !== 'team' || target.teamId === (sourceView?.teamId ?? (scope.kind === 'team' ? scope.team.id : undefined)))) ?? saveTargets[0]

  useEffect(() => {
    if (!savedView || hydratedSavedViewId.current === savedView.id) return
    hydratedSavedViewId.current = savedView.id
    setFilters(filtersFromSavedView(savedView))
    setDisplay(readPersonalDisplay(`${data.workspace.urlKey}:issue-explorer:view:${savedView.id}:display`, savedView, savedView.view))
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
  const changeDisplay = (next: MyIssuesDisplayOptions) => {
    setDisplay(next)
    // Never write through to a shared saved view; "Save as default for view" does that explicitly.
    writeValue(personalViewKey ?? `${preferencesKey}:display`, JSON.stringify({ ...next, properties: [...next.properties] }))
  }
  const resetDisplay = () => {
    const next = savedView ? displayFromSavedView(savedView, savedView.view) : { ...defaultDisplay(view), ...(boardRoute ? { layout: 'board' as const, showEmptyGroups: true } : {}) }
    setDisplay(next)
    removeValue(personalViewKey ?? `${preferencesKey}:display`)
  }
  const saveDisplayAsViewDefault = savedView && onUpdateSavedView ? () => {
    void onUpdateSavedView(savedView.id, { resource: 'issues', scope: savedView.scope, teamId: savedView.teamId, ownerId: savedView.ownerId, view: savedView.view, filters: filtersFromSavedView(savedView), display: displaySnapshot(display) })
      .then(() => { if (personalViewKey) removeValue(personalViewKey); toast.success('Saved as default for view') })
      .catch(() => toast.error('Could not save view default'))
  } : undefined
  const displayMenuProps = {
    availableGroupings: data.issueCollectionPaged ? PAGED_GROUPINGS : undefined,
    availableOrderings: data.issueCollectionPaged ? PAGED_ORDERINGS : undefined,
    toggles: (scope.kind === 'team' ? ['triage', 'archived', 'subTeam'] : ['triage', 'archived']) as ('triage' | 'archived' | 'subTeam')[],
    onReset: resetDisplay,
    resetLabel: savedView ? 'Reset to view default' : 'Reset to default',
    onSaveDefault: saveDisplayAsViewDefault,
  }
  const changeDetails = (open: boolean) => { setDetailsOpen(open); if (open) setInsightsOpen(false); writeValue(`${data.workspace.urlKey}:issue-explorer:${storageScope}:details`, String(open)) }
  const changeInsights = (open: boolean) => { setInsightsOpen(open); if (open) { setDetailsOpen(false); setPreviewIssueId(undefined) } }
  const openIssueFromExplorer = (row: MyIssuesRowData) => {
    const issue = issuesById.get(row.id)
    if (!issue) { void fetchIssueRecord(row.id, undefined, data.workspace.urlKey).then(issue => onOpenIssue(issue, boundedIssueSequence(rows.map(row => row.id), issue.id))).catch(() => toast.error('Could not load issue')); return }
    if (detailsOpen) setPreviewIssueId(issue.id)
    else onOpenIssue(issue, boundedIssueSequence(groups.find(group => group.issues.some(row => row.id === issue.id))?.issues.map(row => row.id) ?? [issue.id], issue.id))
  }
  const splitOrigin = savedView
    ? { type: 'customView' as const, customViewId: savedView.id, label: savedView.name }
    : scope.kind === 'team'
      ? { type: 'teamView' as const, teamKey: scope.team.key, viewKind: view }
      : { type: 'issueView' as const, teamKey: data.teams[0]?.key }
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
        setRowOverrides(current => new Map(current).set(row.id, issueToExplorerRow(updated, data.workspace.urlKey,data.issues,data)))
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
    if (action === 'delete') { if (await confirmAction(`Delete ${row.identifier}?`,{description:'This cannot be undone.',confirmLabel:'Delete'})) await onDeleteIssues([row.id]); return }
    if (action === 'copy' || action === 'copyUrl') { await navigator.clipboard.writeText(`${location.origin}${row.href}`); return }
    if (action === 'copyId') { await navigator.clipboard.writeText(row.identifier); return }
    if (action === 'copyTitle') { await navigator.clipboard.writeText(row.title); return }
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
    const sortOrder = targetIndex === 0 ? (targetIssues[0]?.sortOrder ?? 1) - 1 : targetIndex >= targetIds.length ? (targetIssues.at(-1)?.sortOrder ?? 0) + 1 : ((targetIssues[targetIndex - 1]?.sortOrder ?? 0) + (targetIssues[targetIndex]?.sortOrder ?? 0)) / 2
    const update: IssueUpdateInput = { sortOrder, ...(sourceGroupId === targetGroupId ? {} : explorerBoardGroupUpdate(row, display.grouping, targetGroupId, data)) }
    void updateOne(row, update).catch(() => undefined)
  }
  const savedViewSnapshot = (): SavedViewMutationInput => ({ resource: 'issues', scope: scope.kind, teamId: scope.kind === 'team' ? scope.team.id : '', ownerId: data.viewer.id, view, filters, display: displaySnapshot(display), ...(draftInsights ? { insights: draftInsights as unknown as Record<string, unknown> } : {}) })
  const previewTarget = initialSaveTarget ?? (scope.kind === 'team' ? { scope: 'team' as const, teamId: scope.team.id, label: scope.team.name, team: scope.team } : { scope: 'workspace' as const, label: data.workspace.name })
  const insightsView: SavedView = savedView ?? { id: creatingView ? '__new-view' : preferencesKey, name: scope.kind === 'team' ? scope.team.name : 'All issues', description: '', resource: 'issues', scope: previewTarget.scope, teamId: previewTarget.scope === 'team' ? previewTarget.teamId : '', ownerId: data.viewer.id, view, filters, display: displaySnapshot(display), insights: draftInsights as unknown as Record<string, unknown> | undefined, createdAt: '', updatedAt: '' }
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
  const filtersDirty = Boolean(savedView && filtersAreDirty(filters, filtersFromSavedView(savedView)))
  const updateSavedViewFilters = () => {
    if (!savedView || !onUpdateSavedView) return
    void onUpdateSavedView(savedView.id, { resource: 'issues', scope: savedView.scope, teamId: savedView.teamId, ownerId: savedView.ownerId, view: savedView.view, filters, display: displaySnapshot(display) }).catch(() => undefined)
  }

  const savedViewMenu = savedView && <SavedViewMenu
    view={savedView}
    users={data.users}
    teams={data.teams}
    subscriptionEvents={savedViewSubscriptionEvents}
    onEdit={() => { setViewEditor('edit'); onBeginEditSavedView?.() }}
    onDuplicate={onDuplicateSavedView ? () => onDuplicateSavedView(savedView) : undefined}
    onUpdate={onUpdateSavedView ? input => { void onUpdateSavedView(savedView.id, input) } : undefined}
    onSetSubscriptionEvents={onSetSavedViewSubscriptionEvents ? events => { void onSetSavedViewSubscriptionEvents(savedView, events) } : undefined}
    onShare={onShareSavedView ? () => { void onShareSavedView(savedView).then(path => { if (path) void navigator.clipboard.writeText(`${location.origin}${path}`) }) } : undefined}
    onCopy={() => { void navigator.clipboard.writeText(window.location.href) }}
    onExport={() => exportIssuesCsv(rows, savedView.name)}
    onDelete={() => { if (onDeleteSavedView) void confirmAction(`Delete view “${savedView.name}”?`,{confirmLabel:'Delete view'}).then(confirmed=>{if(confirmed)return onDeleteSavedView(savedView)}) }}
  />

  const rowActions = { data, onUpdateIssue, onDeleteIssues, onOpenIssue: (issue: Issue) => onOpenIssue(issue) }
  return <IssueRowActionsProvider value={rowActions}>
    <IssueExplorerSurface
      scopeName={scope.kind === 'team' ? scope.team.name : data.workspace.name}
      scopeTeam={scope.kind === 'team' ? scope.team : undefined}
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
      insightsOpen={insightsOpen}
      itemCount={drillRows ? drillRows.length : data.issueCollectionPaged ? pagedTotal : rows.length}
      filterOpenSignal={filterOpenSignal}
      filterOptions={field => explorerFilterOptions(field, rowOptions)}
      onFilterToggle={addFilter}
      onDisplayOptionsChange={changeDisplay}
      onDetailsOpenChange={changeDetails}
      onInsightsOpenChange={changeInsights}
      onNavigateView={onNavigateView}
      onNewViewResourceChange={onNewViewResourceChange}
      onAddView={() => setViewEditor('create')}
      onSavedViewSelect={onNavigateSavedView}
      onToggleFavorite={() => { if (savedView && onToggleSavedViewFavorite) void onToggleSavedViewFavorite(savedView) }}
      viewActions={savedViewMenu}
      onOpenSidebar={onOpenSidebar}
      displayMenuProps={displayMenuProps}
      resourceHeader={resourceHeader}
      viewEditor={viewEditor && (viewEditor === 'edit' && savedView ? <EditCustomViewHeader
        orgKey={data.workspace.urlKey}
        viewId={savedView.id}
        initialName={savedView.name}
        initialDescription={savedView.description}
        initialIcon={savedView.icon}
        initialColor={savedView.color}
        initialTarget={initialSaveTarget}
        saveTargets={[]}
        saving={viewSaving}
        hasChanges
        onCancel={() => { setViewEditor(undefined); onFinishEditSavedView?.() }}
        onSave={(name, description, target, visual) => { void saveViewEditor(name, description, target, visual) }}
      /> : <SavedViewEditor
        initialName={duplicateFrom?.name ?? ''}
        namePlaceholder="All issues"
        initialDescription={duplicateFrom?.description ?? ''}
        initialIcon={duplicateFrom?.icon}
        initialColor={duplicateFrom?.color}
        initialTarget={initialSaveTarget}
        saveTargets={saveTargets}
        saving={viewSaving}
        onCancel={() => { setViewEditor(undefined); if (creatingView) onCancelCreateSavedView?.(); discardCustomViewDraft(data.workspace.urlKey, 'new') }}
        onSave={(name, description, target, visual) => { void saveViewEditor(name, description, target, visual) }}
      />)}
      filterBar={(filters.length > 0 || viewEditor || !savedView) && <>
        <MyIssuesFilterBar filters={filters} filterOptions={filter => explorerFilterOptions(filter.field, rowOptions)} onAdd={() => setFilterOpenSignal(value => value + 1)} onClear={() => persistFilters([])} onOperatorChange={(id, operator) => persistFilters(updateFilterOperator(filters, id, operator))} onRemove={id => persistFilters(filters.filter(filter => filter.id !== id))} onValuesChange={(id, options) => persistFilters(updateFilterValues(filters, id, options))}/>
        {savedView && filtersDirty && !viewEditor && <SaveCustomViewButtons saving={viewSaving} onUpdate={updateSavedViewFilters} onCreate={() => setViewEditor('create')} />}
      </>}
    >
      <IssuesSplitLayout
        detailsOpen={detailsOpen && !insightsOpen}
        list={<>
      {data.issueCollectionPaged && !drillRows ? <PagedIssueList
        data={data}
        query={pagedQuery}
        layout={display.layout}
        hiddenGroupIds={display.hiddenGroupIds}
        onHideGroup={id => changeDisplay({ ...display, hiddenGroupIds: [...display.hiddenGroupIds, id] })}
        onShowGroup={id => changeDisplay({ ...display, hiddenGroupIds: display.hiddenGroupIds.filter(value => value !== id) })}
        onMoveIssueRecord={(issue, input) => onUpdateIssue(issue.id, input)}
        onTotalChange={setPagedTotal}
        onLoadedIssuesChange={setPagedIssues}
        collapsedGroupIds={collapsedGroups}
        displayProperties={display.properties}
        propertyOptions={rowOptions}
        selectedIds={selection.selectedIds}
        mutationErrors={mutationErrors}
        onOpenIssueRecord={onOpenIssue}
        onCreateIssue={group => onCreateIssue?.(scope.kind === 'team' ? { ...group.createContext, teamId: scope.team.id } : group.createContext)}
        onGroupCollapsedChange={(id, collapsed) => setCollapsedGroups(current => { const next = new Set(current); if (collapsed) next.add(id); else next.delete(id); return next })}
        onPropertyChange={changeProperty}
        onSelectIssue={selection.selectIssue}
        onContextAction={(row, action) => { void contextAction(row, action) }}
      /> : display.layout === 'list' ? <MyIssuesList
        groups={groups}
        selectedIds={selection.selectedIds}
        collapsedGroupIds={collapsedGroups}
        displayProperties={display.properties}
        nestedSubIssues={display.nestedSubIssues}
        propertyOptions={rowOptions}
        mutationErrors={mutationErrors}
        onCreateIssue={group => { const stateId = stateIdForExplorerGroup(group, data); const context = group.createContext ?? (stateId ? { stateId } : undefined); onCreateIssue?.(scope.kind === 'team' ? { ...context, teamId: scope.team.id } : context) }}
        onGroupCollapsedChange={(id, collapsed) => setCollapsedGroups(current => { const next = new Set(current); if (collapsed) next.add(id); else next.delete(id); return next })}
        onOpenIssue={openIssueFromExplorer}
        onPropertyChange={changeProperty}
        onRetryMutation={row => { const input = retryUpdates.current.get(row.id); if (input) void updateOne(row, input).catch(() => undefined) }}
        onSelectIssue={selection.selectIssue}
        onContextAction={(row, action) => { void contextAction(row, action) }}
      /> : <IssueBoard
        groups={groups}
        hiddenGroupIds={display.hiddenGroupIds}
        properties={display.properties}
        propertyOptions={rowOptions}
        selectedIds={selection.selectedIds}
        onCreateIssue={group => { const stateId = stateIdForExplorerGroup(group, data); const context = group.createContext ?? (stateId ? { stateId } : undefined); onCreateIssue?.(scope.kind === 'team' ? { ...context, teamId: scope.team.id } : context) }}
        onHideGroup={groupId => changeDisplay({ ...display, hiddenGroupIds: [...new Set([...display.hiddenGroupIds, groupId])] })}
        onShowGroup={groupId => changeDisplay({ ...display, hiddenGroupIds: display.hiddenGroupIds.filter(id => id !== groupId) })}
        onMove={moveIssue}
        onOpenIssue={openIssueFromExplorer}
        onPropertyChange={changeProperty}
        onSelectIssue={selection.selectIssue}
      />}
        </>}
        detail={
          previewIssue ? (
            <IssueViewSplitPage
              workspaceSlug={data.workspace.urlKey}
              origin={splitOrigin}
              selectedIssue={issueToExplorerRow(previewIssue, data.workspace.urlKey, data.issues, data)}
              preview={renderIssuePreview ? renderIssuePreview(previewIssue, () => setPreviewIssueId(undefined)) : undefined}
              onClose={() => setPreviewIssueId(undefined)}
            />
          ) : savedView ? (
            <SavedViewDetailsPanel
              favorite={savedViewFavorite}
              menu={savedViewMenu}
              onClose={() => changeDetails(false)}
              onSummaryItemSelect={(dimension, id, label, color) => addFilter(dimension === 'assignee' ? 'assignee' : dimension === 'project' ? 'project' : 'labels', { id, label, color })}
              onToggleFavorite={() => { if (onToggleSavedViewFavorite) void onToggleSavedViewFavorite(savedView) }}
              rows={rows}
              team={data.teams.find(team => team.id === savedView.teamId)}
              users={data.users}
              view={savedView}
              workspace={data.workspace}
            />
          ) : (
            <IssueViewSplitPage
              workspaceSlug={data.workspace.urlKey}
              origin={splitOrigin}
              summary={summary}
              onClose={() => changeDetails(false)}
              onSummaryItemSelect={summaryFilter}
            />
          )
        }
        fallbackDetail={
          <>
            {(!savedView || previewIssue) && <MyIssuesDetailsPane
              open={detailsOpen}
              width={detailsWidth}
              onWidthChange={setDetailsWidth}
              onClose={() => { if (previewIssueId) setPreviewIssueId(undefined); else changeDetails(false) }}
              selectedIssue={previewIssue ? issueToExplorerRow(previewIssue, data.workspace.urlKey, data.issues, data) : undefined}
              previewContent={previewIssue && renderIssuePreview ? renderIssuePreview(previewIssue, () => setPreviewIssueId(undefined)) : undefined}
              summary={summary}
              onSummaryItemSelect={summaryFilter}
            />}
            {savedView && detailsOpen && !previewIssue && <SavedViewDetailsPanel
              favorite={savedViewFavorite}
              menu={savedViewMenu}
              onClose={() => changeDetails(false)}
              onSummaryItemSelect={(dimension, id, label, color) => addFilter(dimension === 'assignee' ? 'assignee' : dimension === 'project' ? 'project' : 'labels', { id, label, color })}
              onToggleFavorite={() => { if (onToggleSavedViewFavorite) void onToggleSavedViewFavorite(savedView) }}
              rows={rows}
              team={data.teams.find(team => team.id === savedView.teamId)}
              users={data.users}
              view={savedView}
              workspace={data.workspace}
            />}
          </>
        }
      />
      {drillRows && <InsightHiddenNotice hidden={drillRows.length - new Set(groups.flatMap(group => group.issues.map(issue => issue.id))).size} onShow={() => changeDisplay({ ...display, completedWindow: 'all', showSubIssues: true, hiddenGroupIds: [] })}/>}
      {insightsView && insightsOpen && <SavedViewInsightsPanel
        allRows={insightRows}
        data={data}
        onClose={() => changeInsights(false)}
        onSave={async (config: SavedViewInsightsConfig) => { if (savedView && onUpdateSavedView) await onUpdateSavedView(savedView.id, { insights: config as unknown as Record<string, unknown> }); else setDraftInsights(config) }}
        rows={activeInsightRows}
        query={data.issueCollectionPaged ? insightQuery : undefined}
        onDrillChange={setDrillRows}
        onOpenIssue={openIssueFromExplorer}
        view={insightsView}
      />}
    </IssueExplorerSurface>
    <MyIssuesBulkActionBar selectedIssues={selection.selectedIssues} destructiveActions={['archive', 'delete']} actionOptions={action => explorerBulkOptions(action, rowOptions)} onAction={(action, _issues, value) => { void executeExplorerBulkAction({ action, ids: selection.selectedIssues.map(issue => issue.id), value, data, issuesById, onUpdateIssue, onUpdateIssues, onDeleteIssues }).then(() => selection.clearSelection()) }} onClear={selection.clearSelection}/>
  </IssueRowActionsProvider>
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

function issuesForScope(issues: Issue[], scope: IssueExplorerPageProps['scope'], view: TeamIssuesRouteView, includeArchived = false, scopeTeamIds?: Set<string>) {
  return issues.filter(issue => (includeArchived || !issue.archivedAt) && (scope.kind === 'workspace' || (scopeTeamIds ? scopeTeamIds.has(issue.team.id) : issue.team.id === scope.team.id)) && (view === 'all' || (view === 'backlog' ? issue.state.type === 'backlog' : issue.state.type === 'unstarted' || issue.state.type === 'started')))
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
function initialPropertyFilters(data: BootstrapData, labelId?: string, statusId?: string): MyIssuesAppliedFilter[] | undefined {
  const filters: MyIssuesAppliedFilter[] = []
  const label = data.labels.find(item => item.id === labelId)
  if (label) { const value = { value: label.id, valueLabel: label.name, color: label.color }; filters.push({ id: `labels-${label.id}`, field: 'labels', fieldLabel: 'Labels', operator: 'is', ...value, values: [value] }) }
  const state = data.states.find(item => item.id === statusId)
  if (state) { const value = { value: state.id, valueLabel: state.name, color: state.color }; filters.push({ id: `status-${state.id}`, field: 'status', fieldLabel: 'Status', operator: 'is', ...value, values: [value] }) }
  return filters.length ? filters : undefined
}
function insightPropertyFilters(data: BootstrapData, filters: NonNullable<IssueExplorerPageProps['initialInsightFilters']>): MyIssuesAppliedFilter[] {
  const result: MyIssuesAppliedFilter[] = []
  const add = (field: 'status'|'assignee'|'labels', fieldLabel: string, values: Array<{id:string;label:string;color?:string}>) => {
    if (!values.length) return
    const mapped = values.map(item => ({ value:item.id, valueLabel:item.label, color:item.color }))
    result.push({ id:`insight-${field}`, field, fieldLabel, operator:'is', ...mapped[0], values:mapped })
  }
  add('status','Status',(filters.stateIds??[]).map(id=>data.states.find(item=>item.id===id)).filter((item):item is NonNullable<typeof item>=>Boolean(item)).map(item=>({id:item.id,label:item.name,color:item.color})))
  add('assignee','Assignee',(filters.assigneeIds??[]).map(id=>data.users.find(item=>item.id===id)).filter((item):item is NonNullable<typeof item>=>Boolean(item)).map(item=>({id:item.id,label:item.displayName})))
  add('labels','Labels',(filters.labelIds??[]).map(id=>data.labels.find(item=>item.id===id)).filter((item):item is NonNullable<typeof item>=>Boolean(item)).map(item=>({id:item.id,label:item.name,color:item.color})))
  return result
}
function filterInsightTeams(issues: Issue[], teamIds?: string[]) { return teamIds?.length ? issues.filter(issue=>teamIds.includes(issue.team.id)) : issues }
function readFilters(key: string): MyIssuesAppliedFilter[] { try { const value = JSON.parse(localStorage.getItem(key) ?? '[]'); return Array.isArray(value) ? value : [] } catch { return [] } }
function readDisplay(key: string, view: TeamIssuesRouteView, board = false): MyIssuesDisplayOptions { const fallback = { ...defaultDisplay(view), ...(board ? { layout: 'board' as const, showEmptyGroups: true } : {}) }; try { const value = JSON.parse(localStorage.getItem(key) ?? 'null'); return value ? { ...fallback, ...value, properties: new Set(Array.isArray(value.properties) ? value.properties : [...fallback.properties]) } : fallback } catch { return fallback } }
function readBoolean(key: string, fallback: boolean) { try { const value = localStorage.getItem(key); return value == null ? fallback : value === 'true' } catch { return fallback } }
function writeValue(key: string, value: string) { try { localStorage.setItem(key, value) } catch { /* Preferences are best-effort. */ } }
function removeValue(key: string) { try { localStorage.removeItem(key) } catch { /* Preferences are best-effort. */ } }
function readPersonalDisplay(key: string, view: SavedView, routeView: TeamIssuesRouteView): MyIssuesDisplayOptions {
  const fallback = displayFromSavedView(view, routeView)
  try { const value = JSON.parse(localStorage.getItem(key) ?? 'null'); return value ? { ...fallback, ...value, properties: new Set(Array.isArray(value.properties) ? value.properties : [...fallback.properties]) } : fallback } catch { return fallback }
}
function readOrder(key: string): string[] { try { const value = JSON.parse(localStorage.getItem(key) ?? '[]'); return Array.isArray(value) && value.every(item => typeof item === 'string') ? value : [] } catch { return [] } }
function filtersFromSavedView(view: SavedView): MyIssuesAppliedFilter[] { return Array.isArray(view.filters) ? view.filters as MyIssuesAppliedFilter[] : [] }
function displayFromSavedView(view: SavedView, routeView: TeamIssuesRouteView): MyIssuesDisplayOptions {
  const fallback = defaultDisplay(routeView)
  const value = view.display && typeof view.display === 'object' ? view.display : {}
  return { ...fallback, ...value, properties: new Set(Array.isArray(value.properties) ? value.properties as MyIssuesProperty[] : [...fallback.properties]) } as MyIssuesDisplayOptions
}
function displaySnapshot(display: MyIssuesDisplayOptions): Record<string, unknown> { return { ...display, properties: [...display.properties] } }

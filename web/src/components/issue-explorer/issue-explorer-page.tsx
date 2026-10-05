import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
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
import { fetchIssueRecord, listIssueRecords, updateStructuredTeamSettings } from '@/lib/api'
import { toast } from 'sonner'
import { IssueExplorerSurface } from './issue-explorer-surface'
import { IssueBoard } from './issue-board'
import { SavedViewEditor, SavedViewMenu, type SavedViewDraft, type SavedViewTarget } from './saved-view-editor'
import { HiddenByFiltersFooter, NoMatchingIssues, SavedViewBandCommands } from './saved-view-filter-band'
import bandStyles from './saved-view-filter-band.module.css'
import explorerStyles from './issue-explorer.module.css'
import { AdvancedFilterChip } from './advanced-filter-editor'
import { createAdvancedFilter, decodeFiltersParam, encodeFiltersParam, normalizeStoredFilters, savableFilters } from './advanced-filter'
import { suggestView } from './view-suggestions'
import { useI18n } from '@/i18n/i18n'
import type { ViewEditorControls } from './issue-explorer-surface'
import { InsightHiddenNotice, SavedViewDetailsPanel, SavedViewInsightsPanel, type SavedViewInsightsConfig } from './saved-view-panels'
import { confirmAction } from '@/components/ui/action-dialog-service'
import type { ViewVisual } from '@/components/views/view-icon-picker'
import {
  ISSUE_FILTER_LABELS, applyExplorerFilters, buildExplorerIssueGroups, executeExplorerBulkAction, explorerBoardGroupUpdate, explorerBulkOptions, explorerFilterOptions,
  explorerPropertyOptions, explorerUpdateForAction, explorerUpdateForProperty, issueToExplorerRow, optimisticExplorerRow,
  stateIdForExplorerGroup, withMapKey, withoutMapKey,
} from './issue-explorer-model'
import { IssuesSplitLayout, IssueViewSplitPage } from '@/components/issues-split-view'
import { MyIssuesSummaryCard } from '@/components/my-issues/my-issues-summary-card'
import { labelGroups, PAGED_GROUPINGS, PAGED_ORDERINGS, groupSummaries, pagedDisplayQuery } from './issue-grouping'
import { isActiveSubscription } from '@/lib/subscription-records'

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
  /** Surface defaults on top of the team-view defaults (for example member profiles show triage issues). */
  defaultDisplayOverrides?: Partial<MyIssuesDisplayOptions>
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
  /** Public sharing lives in the Views list row menu; the view header menu matches Linear (no share item). */
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
  /** Replaces the view-summary details panel (member profiles show a profile aside beside the list). */
  detailsPanel?: ReactNode
  /** localStorage key for the details open state; defaults to the scope's key. */
  detailsStorageKey?: string
  /** Replaces the empty list state. */
  emptyState?: ReactNode
  /** Extra class on the explorer container. */
  className?: string
  /** Noun for the insights toolbar button ("Open {label}"); defaults to "view insights". */
  insightsLabel?: string
}

export function IssueExplorerPage({ boardRoute = false, preferenceScope, resourceHeader, scopeFilter, scopeConditions, defaultDisplayOverrides, data, initialLabelId, initialStatusId, initialInsightFilters, scope, view, viewHref, savedView, duplicateFrom, creatingView = false, editingView = false, defaultSaveScope, savedViews = [], savedViewHref, onNavigateView, onNavigateSavedView, onCreateSavedView, onUpdateSavedView, onDeleteSavedView, onToggleSavedViewFavorite, onSetSavedViewSubscriptionEvents, onDuplicateSavedView, onCancelCreateSavedView, onBeginEditSavedView, onFinishEditSavedView, onNewViewResourceChange, onOpenIssue, renderIssuePreview, onOpenSidebar, onCreateIssue, onUpdateIssue, onUpdateIssues, onDeleteIssues, detailsPanel, detailsStorageKey, emptyState, className, insightsLabel }: IssueExplorerPageProps) {
  const storageScope = scope.kind === 'team' ? `team:${scope.team.id}` : 'workspace'
  const preferencesKey = `${data.workspace.urlKey}:issue-explorer:${preferenceScope ?? storageScope}:${boardRoute ? 'board' : view}`
  const sourceView = savedView ?? duplicateFrom
  const [filters, setFilters] = useState<MyIssuesAppliedFilter[]>(() => sourceView ? filtersFromSavedView(sourceView) : initialInsightFilters ? insightPropertyFilters(data, initialInsightFilters) : initialPropertyFilters(data, initialLabelId, initialStatusId) ?? readFilters(`${preferencesKey}:filters`))
  // Saved views keep a personal display layer on top of the view default (Linear "Reset to view default").
  const teamViewKey = boardRoute ? 'board' : view
  const [teamDefault, setTeamDefault] = useState<Record<string, unknown> | undefined>(() => scope.kind === 'team' ? data.teamSettings?.[scope.team.id]?.issueViewDefaults?.[teamViewKey] : undefined)
  const personalViewKey = savedView ? `${data.workspace.urlKey}:issue-explorer:view:${savedView.id}:display` : undefined
  const [display, setDisplay] = useState<MyIssuesDisplayOptions>(() => personalViewKey ? readPersonalDisplay(personalViewKey, savedView!, view) : duplicateFrom ? displayFromSavedView(duplicateFrom, view) : readDisplay(`${preferencesKey}:display`, view, boardRoute, teamDefault, defaultDisplayOverrides))
  const detailsKey = detailsStorageKey ?? `${data.workspace.urlKey}:issue-explorer:${storageScope}:details`
  const [storedDetailsOpen, setDetailsOpen] = useState(() => readBoolean(detailsKey, false))
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
  // Linear's new-view page has no details panel.
  const detailsOpen = storedDetailsOpen && !(creatingView || viewEditor === 'create')
  // Linear: filters added on a saved view (outside edit mode) are temporary — a band with Clear / Save,
  // shareable as `?filter=`; the view's own filters live in the edit card.
  const [extraFilters, setExtraFilters] = useState<MyIssuesAppliedFilter[]>(() => savedView && !editingView && typeof location !== 'undefined' ? decodeFiltersParam(new URLSearchParams(location.search).get('filter')) : [])
  const [openAdvancedId, setOpenAdvancedId] = useState<string>()
  const [editorDraft, setEditorDraft] = useState<SavedViewDraft>()
  const [bandCreateRestore, setBandCreateRestore] = useState<{ filters: MyIssuesAppliedFilter[]; extra: MyIssuesAppliedFilter[] }>()
  const [baseTotal, setBaseTotal] = useState<number>()
  const { locale } = useI18n()
  const hydratedSavedViewId = useRef(savedView?.id)
  const mutationSequence = useRef(new Map<string, number>())
  const mutationQueues = useRef(new Map<string, Promise<Issue>>())
  const retryUpdates = useRef(new Map<string, IssueUpdateInput>())
  const savedViewFavorite = Boolean(savedView && (savedView.favorite || data.favorites.some(item => item.userId === data.viewer.id && item.resourceType === 'view' && item.resourceId === savedView.id)))
  const savedViewSubscribed = Boolean(savedView && (savedView.subscribed || data.subscriptions.some(item => item.userId === data.viewer.id && item.resourceType === 'view' && item.resourceId === savedView.id && isActiveSubscription(item))))
  const savedViewSubscription = savedView ? data.subscriptions.find(item => item.userId === data.viewer.id && item.resourceType === 'view' && item.resourceId === savedView.id) : undefined
  const savedViewSubscriptionEvents = savedViewSubscription?.events?.length ? savedViewSubscription.events : savedViewSubscribed ? ['issue-added', 'issue-completed'] : []


  const showSubTeams = display.showSubTeamIssues !== false
  const scopeTeamIds = useMemo(() => scope.kind === 'team' ? (showSubTeams ? teamHierarchy(data.teams, data.teamSettings).subtree(scope.team.id) : new Set([scope.team.id])) : undefined, [data.teams, data.teamSettings, scope, showSubTeams])
  const scopedIssues = useMemo(() => filterInsightTeams(issuesForScope(data.issues, scope, view, Boolean(display.showArchived), scopeTeamIds), initialInsightFilters?.teamIds).filter(issue => !scopeFilter || scopeFilter(issue)), [data.issues, display.showArchived, initialInsightFilters?.teamIds, scope, scopeFilter, scopeTeamIds, view])
  const insightIssues = useMemo(() => insightsOpen ? filterInsightTeams(issuesForScope(data.issues, scope, view, true, scopeTeamIds), initialInsightFilters?.teamIds) : [], [data.issues, initialInsightFilters?.teamIds, insightsOpen, scope, scopeTeamIds, view])
  const issuesById = useMemo(() => new Map([...data.issues, ...pagedIssues].map(issue => [issue.id, issue])), [data.issues, pagedIssues])
  const rowOptions = useMemo(() => explorerPropertyOptions(data, scopedIssues), [data, scopedIssues])
  // Bootstrap/sync owns the complete visible collection. Group before virtualizing;
  // replacing it with one query page truncates both group counts and membership.
  const bandActive = Boolean(savedView && !viewEditor)
  const effectiveFilters = useMemo(() => bandActive && extraFilters.length ? [...filters, ...extraFilters] : filters, [bandActive, extraFilters, filters])
  const visibleIssues = useMemo(() => data.issueCollectionPaged ? pagedIssues : applyExplorerFilters(scopedIssues, effectiveFilters, data), [data, effectiveFilters, scopedIssues, pagedIssues])
  const baseRows = useMemo(() => visibleIssues.map(issue => rowOverrides.get(issue.id) ?? issueToExplorerRow(issue, data.workspace.urlKey,data.issues,data)), [data, rowOverrides, visibleIssues])
  const rows = drillRows ?? baseRows
  const insightRows = useMemo(() => insightsOpen ? applyExplorerFilters(insightIssues, effectiveFilters, data).map(issue => rowOverrides.get(issue.id) ?? issueToExplorerRow(issue, data.workspace.urlKey,data.issues,data)) : [], [data, effectiveFilters, insightIssues, insightsOpen, rowOverrides])
  const activeInsightRows = useMemo(() => insightRows.filter(row => !row.archivedAt), [insightRows])
  const groups = useMemo(() => buildExplorerIssueGroups(rows, display, data, view, manualOrder), [data, display, manualOrder, rows, view])
  const buildPagedQuery = useCallback((list: MyIssuesAppliedFilter[]) => {
    const triageTeamIds = Object.values(data.teamSettings ?? {}).filter(settings => settings.triageEnabled).map(settings => settings.teamId)
    const { sort, direction, groupBy, archived, conditions } = pagedDisplayQuery(display, Date.now(), triageTeamIds)
    if (view === 'backlog') conditions.push({ field: 'status', operator: 'is', values: ['backlog'] })
    if (view === 'active') conditions.push({ field: 'status', operator: 'in', values: ['unstarted', 'started'] })
    const includeSubTeams = scope.kind === 'team' && display.showSubTeamIssues !== false
    return { teamId: scope.kind === 'team' ? scope.team.id : initialInsightFilters?.teamIds, includeSubTeams, archived, groupBy, sort, direction, filter: { and: [issueFiltersToQueryAst(list, { data }), ...conditions, ...(scopeConditions ?? [])] } }
  }, [data, display, initialInsightFilters?.teamIds, scope, scopeConditions, view])
  const pagedQuery = useMemo(() => buildPagedQuery(effectiveFilters), [buildPagedQuery, effectiveFilters])
  // "N issues hidden by filters": the saved view's own count without the temporary filters.
  const baseQuery = useMemo(() => bandActive && extraFilters.length && data.issueCollectionPaged ? buildPagedQuery(filters) : undefined, [bandActive, buildPagedQuery, data.issueCollectionPaged, extraFilters.length, filters])
  useEffect(() => {
    if (!baseQuery) { setBaseTotal(undefined); return }
    const abort = new AbortController()
    void listIssueRecords({ ...baseQuery, groupBy: undefined, limit: 1, includeTotal: true }, abort.signal).then(page => setBaseTotal(page.total)).catch(() => undefined)
    return () => abort.abort()
  }, [baseQuery])
  const clientBaseCount = useMemo(() => bandActive && extraFilters.length && !data.issueCollectionPaged ? applyExplorerFilters(scopedIssues, filters, data).length : undefined, [bandActive, data, extraFilters.length, filters, scopedIssues])
  const insightQuery = useMemo(() => ({
    teamId: scope.kind === 'team' ? scope.team.id : initialInsightFilters?.teamIds,
    includeSubTeams: scope.kind === 'team',
    filter: { and: [issueFiltersToQueryAst(effectiveFilters, { data }), ...(view === 'backlog' ? [{ field: 'status', values: ['backlog'] }] : view === 'active' ? [{ field: 'status', values: ['unstarted', 'started'] }] : [])] },
  }), [effectiveFilters, initialInsightFilters?.teamIds, scope, view])
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

  const split = display.layout === 'split'
  useEffect(() => { if (!detailsOpen && !split) setPreviewIssueId(undefined) }, [detailsOpen, split])
  // Split layout (Linear `split`: narrow list beside the selected issue) always has a selection.
  const flatRowIds = useMemo(() => groups.flatMap(group => group.issues.map(issue => issue.id)), [groups])
  const splitRowIds = data.issueCollectionPaged ? pagedIssues.map(issue => issue.id) : flatRowIds
  useEffect(() => { if (split && (!previewIssueId || !splitRowIds.includes(previewIssueId)) && splitRowIds[0]) setPreviewIssueId(splitRowIds[0]) }, [previewIssueId, split, splitRowIds])
  useEffect(() => {
    if (!split) return
    const onKey = (event: KeyboardEvent) => {
      if (event.metaKey || event.ctrlKey || event.altKey || (event.target instanceof Element && event.target.closest('input,textarea,[contenteditable=true],[role=textbox],[role=dialog]'))) return
      const key = event.key.toLowerCase()
      const step = key === 'j' || key === 'arrowdown' ? 1 : key === 'k' || key === 'arrowup' ? -1 : 0
      if (!step) return
      event.preventDefault()
      const index = splitRowIds.indexOf(previewIssueId ?? '')
      const next = splitRowIds[Math.max(0, Math.min(splitRowIds.length - 1, index + step))]
      if (next) setPreviewIssueId(next)
    }
    addEventListener('keydown', onKey)
    return () => removeEventListener('keydown', onKey)
  }, [previewIssueId, split, splitRowIds])
  const splitProperties = useMemo(() => new Set([...display.properties].filter(property => property === 'id' || property === 'status' || property === 'priority' || property === 'assignee')), [display.properties])

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

  // A saved view's filters belong to the view (changed only through edit / "Save to this view").
  const persistFilters = (next: MyIssuesAppliedFilter[]) => { setFilters(next); if (!sourceView && !creatingView) writeValue(`${preferencesKey}:filters`, JSON.stringify(next)) }
  useEffect(() => {
    if (!savedView || viewEditor || typeof location === 'undefined') return
    const url = new URL(location.href)
    if (extraFilters.length) url.searchParams.set('filter', encodeFiltersParam(extraFilters))
    else url.searchParams.delete('filter')
    if (url.href !== location.href) history.replaceState(history.state, '', url)
  }, [extraFilters, savedView, viewEditor])
  const changeDisplay = (next: MyIssuesDisplayOptions) => {
    setDisplay(next)
    // Never write through to a shared saved view; "Save as default for view" does that explicitly.
    writeValue(personalViewKey ?? `${preferencesKey}:display`, JSON.stringify({ ...next, properties: [...next.properties] }))
  }
  const resetDisplay = () => {
    const next = savedView ? displayFromSavedView(savedView, savedView.view) : withTeamDefault({ ...defaultDisplay(view, defaultDisplayOverrides), ...(boardRoute ? { layout: 'board' as const, showEmptyGroups: true } : {}) }, teamDefault)
    setDisplay(next)
    removeValue(personalViewKey ?? `${preferencesKey}:display`)
  }
  const saveDisplayAsViewDefault = savedView && onUpdateSavedView ? () => {
    void onUpdateSavedView(savedView.id, { display: displaySnapshot(display) })
      .then(() => { if (personalViewKey) removeValue(personalViewKey); toast.success('Saved as default for view') })
      .catch(() => toast.error('Could not save view default'))
  } : scope.kind === 'team' ? () => {
    const snapshot = displaySnapshot(display)
    void updateStructuredTeamSettings(scope.team.id, { issueViewDefaults: { [teamViewKey]: snapshot } })
      .then(() => { setTeamDefault(snapshot); removeValue(`${preferencesKey}:display`); toast.success(`Saved as ${scope.team.name} default`) })
      .catch(() => toast.error('Could not save team default'))
  } : undefined
  const menuGroups = groupSummaries(groups)
  const listGroups = groups.filter(group => !display.hiddenGroupIds.includes(group.id) && !display.hiddenGroupIds.includes(group.parentGroupId ?? ''))
  const displayMenuProps = {
    groups: menuGroups,
    labelGroupOptions: labelGroups(data.labels),
    availableGroupings: data.issueCollectionPaged ? PAGED_GROUPINGS : undefined,
    availableOrderings: data.issueCollectionPaged ? PAGED_ORDERINGS : undefined,
    toggles: displayToggles(scope.kind === 'team' ? scope.team.id : undefined, data),
    onReset: resetDisplay,
    resetLabel: savedView ? 'Reset to view default' : teamDefault ? 'Reset to team default' : 'Reset to default',
    onSaveDefault: saveDisplayAsViewDefault,
    saveDefaultLabel: savedView ? 'Save as default for view' : 'Save as team default',
  }
  const changeDetails = (open: boolean) => { setDetailsOpen(open); if (open) setInsightsOpen(false); writeValue(detailsKey, String(open)) }
  const changeInsights = (open: boolean) => { setInsightsOpen(open); if (open) { setDetailsOpen(false); setPreviewIssueId(undefined) } }
  const openIssueFromExplorer = (row: MyIssuesRowData) => {
    const issue = issuesById.get(row.id)
    if (!issue) { void fetchIssueRecord(row.id, undefined, data.workspace.urlKey).then(issue => onOpenIssue(issue, boundedIssueSequence(rows.map(row => row.id), issue.id))).catch(() => toast.error('Could not load issue')); return }
    // A custom details panel (profile aside) is not an issue preview pane: rows open the issue.
    if ((detailsOpen && !detailsPanel) || split) setPreviewIssueId(issue.id)
    else onOpenIssue(issue, boundedIssueSequence(groups.find(group => group.issues.some(row => row.id === issue.id))?.issues.map(row => row.id) ?? [issue.id], issue.id))
  }
  const splitOrigin = savedView
    ? { type: 'customView' as const, customViewId: savedView.id, label: savedView.name }
    : scope.kind === 'team'
      ? { type: 'teamView' as const, teamKey: scope.team.key, viewKind: view }
      : { type: 'issueView' as const, teamKey: data.teams[0]?.key }
  // AI filter applies several filters in one go; chain them on the latest list instead of the render-time snapshot.
  const latestFilters = useRef({ base: filters, extra: extraFilters })
  latestFilters.current = { base: filters, extra: extraFilters }
  const filterTarget: 'base' | 'extra' = bandActive ? 'extra' : 'base'
  const setListFilters = (list: 'base' | 'extra', next: MyIssuesAppliedFilter[]) => {
    latestFilters.current = { ...latestFilters.current, [list]: next }
    if (list === 'extra') setExtraFilters(next)
    else persistFilters(next)
  }
  const updateList = (list: 'base' | 'extra', change: (current: MyIssuesAppliedFilter[]) => MyIssuesAppliedFilter[]) => setListFilters(list, change(latestFilters.current[list]))
  const addFilter = (field: MyIssuesFilterKey, option: MyIssuesFilterOption) => {
    const label = ISSUE_FILTER_LABELS[field]
    if (!label) return
    setListFilters(filterTarget, toggleFilterOption(latestFilters.current[filterTarget], field, label, option))
  }
  const addAdvancedFilter = () => {
    const chip = createAdvancedFilter()
    setOpenAdvancedId(chip.id)
    setListFilters(filterTarget, [...latestFilters.current[filterTarget], chip])
  }
  const renderAdvanced = (list: 'base' | 'extra') => (filter: MyIssuesAppliedFilter) => filter.field !== 'advanced' ? undefined : <AdvancedFilterChip
    key={filter.id}
    defaultOpen={filter.id === openAdvancedId}
    filter={filter}
    filterOptions={field => explorerFilterOptions(field, rowOptions)}
    onChange={tree => updateList(list, current => current.map(item => item.id === filter.id ? { ...item, tree } : item))}
    onOpenChange={open => { if (!open) setOpenAdvancedId(current => current === filter.id ? undefined : current) }}
    onRemove={() => updateList(list, current => current.filter(item => item.id !== filter.id))}
  />
  const filterBarProps = (list: 'base' | 'extra') => {
    const current = list === 'extra' ? extraFilters : filters
    return {
      filters: current,
      filterOptions: (filter: MyIssuesAppliedFilter) => explorerFilterOptions(filter.field, rowOptions),
      renderFilter: renderAdvanced(list),
      onAdd: () => setFilterOpenSignal(value => value + 1),
      onClear: () => setListFilters(list, []),
      onOperatorChange: (id: string, operator: MyIssuesAppliedFilter['operator']) => updateList(list, items => updateFilterOperator(items, id, operator)),
      onRemove: (id: string) => updateList(list, items => items.filter(filter => filter.id !== id)),
      onValuesChange: (id: string, options: MyIssuesFilterOption[]) => updateList(list, items => updateFilterValues(items, id, options)),
    }
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
    const update: IssueUpdateInput = { sortOrder, ...(sourceGroupId === targetGroupId ? {} : explorerBoardGroupUpdate(row, display.grouping, targetGroupId, data, display.labelGroupId)) }
    void updateOne(row, update).catch(() => undefined)
  }
  const savedViewSnapshot = (): SavedViewMutationInput => ({ resource: 'issues', scope: scope.kind, teamId: scope.kind === 'team' ? scope.team.id : '', ownerId: data.viewer.id, view, filters: savableFilters(filters), display: displaySnapshot(display), ...(draftInsights ? { insights: draftInsights as unknown as Record<string, unknown> } : {}) })
  const previewTarget = initialSaveTarget ?? (scope.kind === 'team' ? { scope: 'team' as const, teamId: scope.team.id, label: scope.team.name, team: scope.team } : { scope: 'workspace' as const, label: data.workspace.name })
  const insightsView: SavedView = savedView ?? { id: creatingView ? '__new-view' : preferencesKey, name: scope.kind === 'team' ? scope.team.name : 'All issues', description: '', resource: 'issues', scope: previewTarget.scope, teamId: previewTarget.scope === 'team' ? previewTarget.teamId : '', ownerId: data.viewer.id, view, filters: effectiveFilters, display: displaySnapshot(display), insights: draftInsights as unknown as Record<string, unknown> | undefined, createdAt: '', updatedAt: '' }
  const saveViewEditor = async (name: string, description: string, target: SavedViewTarget | undefined, visual: ViewVisual) => {
    if (viewSaving) return
    setViewSaving(true)
    try {
      if (viewEditor === 'edit' && savedView && onUpdateSavedView) {
        // Only what the card edits: the owner and resource stay; scope moves only through "Save to".
        const destination = target ?? initialSaveTarget
        await onUpdateSavedView(savedView.id, { name, description, ...visual, filters: savableFilters(filters), display: displaySnapshot(display), scope: destination.scope, teamId: destination.scope === 'team' ? destination.teamId : '' })
        if (personalViewKey) removeValue(personalViewKey)
      } else if (onCreateSavedView) {
        const destination = target ?? initialSaveTarget
        const created = await onCreateSavedView({ ...savedViewSnapshot(), name, description, ...visual, scope: destination.scope, teamId: destination.scope === 'team' ? destination.teamId : '' })
        onNavigateSavedView?.(created)
      }
      setViewEditor(undefined)
      if (viewEditor === 'edit') onFinishEditSavedView?.()
    } finally { setViewSaving(false) }
  }
  // Band: "Save to this view" merges the temporary filters into the view (nothing else changes).
  const saveBandToView = () => {
    if (!savedView || !onUpdateSavedView || !extraFilters.length) return
    const merged = savableFilters([...filters, ...extraFilters])
    void onUpdateSavedView(savedView.id, { filters: merged }).then(() => { setFilters(merged); setExtraFilters([]) }).catch(() => undefined)
  }
  // Band: "Create new view…" opens the new-view card with the view's filters plus the temporary ones.
  const createViewFromBand = () => {
    setBandCreateRestore({ filters, extra: extraFilters })
    setFilters([...filters, ...extraFilters])
    setExtraFilters([])
    setViewEditor('create')
  }
  const cancelViewEditor = () => {
    const mode = viewEditor
    setViewEditor(undefined)
    setEditorDraft(undefined)
    if (bandCreateRestore) { setFilters(bandCreateRestore.filters); setExtraFilters(bandCreateRestore.extra); setBandCreateRestore(undefined) }
    if (mode === 'edit') onFinishEditSavedView?.()
    else if (creatingView) onCancelCreateSavedView?.()
  }
  const clearAllFilters = () => setListFilters(filterTarget, [])
  // Linear shortcuts: ⌥⇧F clear all filters, ⌥S save to this view, ⌥V create new view, ⌥F favorite view.
  const shortcutActions = useRef({ clearAllFilters, saveBandToView, createViewFromBand, openCreate: () => setViewEditor('create'), favorite: () => { if (savedView && onToggleSavedViewFavorite) void onToggleSavedViewFavorite(savedView) } })
  shortcutActions.current = { clearAllFilters, saveBandToView, createViewFromBand, openCreate: () => setViewEditor('create'), favorite: () => { if (savedView && onToggleSavedViewFavorite) void onToggleSavedViewFavorite(savedView) } }
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!event.altKey || event.metaKey || event.ctrlKey || event.defaultPrevented) return
      if ((event.target as HTMLElement | null)?.closest('input,textarea,[contenteditable=true],[role=textbox]')) return
      const actions = shortcutActions.current
      if (event.code === 'KeyF' && event.shiftKey) { event.preventDefault(); actions.clearAllFilters(); return }
      if (event.shiftKey) return
      if (event.code === 'KeyS' && bandActive && extraFilters.length) { event.preventDefault(); actions.saveBandToView() }
      else if (event.code === 'KeyV' && !viewEditor && !creatingView) { event.preventDefault(); if (savedView) { if (extraFilters.length) actions.createViewFromBand() } else actions.openCreate() }
      else if (event.code === 'KeyF' && savedView && !viewEditor) { event.preventDefault(); actions.favorite() }
    }
    addEventListener('keydown', onKey)
    return () => removeEventListener('keydown', onKey)
  }, [bandActive, creatingView, extraFilters.length, savedView, viewEditor])
  const editorMode = viewEditor === 'edit' && savedView ? 'edit' : viewEditor ? 'create' : undefined
  // Suggestions follow filter changes (Linear): a view being edited keeps its own icon / description until its filters change.
  const filtersChanged = editorMode !== 'edit' || !savedView || JSON.stringify(savableFilters(filters)) !== JSON.stringify(savableFilters(filtersFromSavedView(savedView)))
  const suggestion = useMemo(() => editorMode && filtersChanged ? suggestView(filters, { states: data.states, locale }) : {}, [data.states, editorMode, filters, filtersChanged, locale])
  const shownSavedView = savedView && editorMode === 'edit' && editorDraft ? { ...savedView, name: editorDraft.name.trim() || savedView.name, description: editorDraft.description, icon: editorDraft.visual.icon, color: editorDraft.visual.color } : savedView
  const hiddenByFilters = bandActive && extraFilters.length ? Math.max(0, (data.issueCollectionPaged ? baseTotal ?? 0 : clientBaseCount ?? 0) - (data.issueCollectionPaged ? pagedTotal : rows.length)) : 0
  const editorCard = editorMode && ((controls: ViewEditorControls) => <SavedViewEditor
    mode={editorMode}
    ariaLabel={editorMode === 'edit' ? 'Edit view' : 'New view'}
    initialName={editorMode === 'edit' ? savedView!.name : duplicateFrom?.name ?? ''}
    namePlaceholder="All issues"
    suggestedName={suggestion.name}
    suggestedDescription={suggestion.description}
    suggestedIcon={suggestion.icon}
    initialDescription={editorMode === 'edit' ? savedView!.description : duplicateFrom?.description ?? ''}
    initialIcon={editorMode === 'edit' ? savedView!.icon : duplicateFrom?.icon}
    initialColor={editorMode === 'edit' ? savedView!.color : duplicateFrom?.color}
    initialTarget={editorMode === 'edit' ? initialSaveTarget : bandCreateRestore ? saveTargets.find(target => target.scope === 'personal') ?? initialSaveTarget : initialSaveTarget}
    saveTargets={saveTargets}
    saving={viewSaving}
    onCancel={cancelViewEditor}
    onDraftChange={setEditorDraft}
    onSave={(name, description, target, visual) => { void saveViewEditor(name, description, target, visual).then(() => setBandCreateRestore(undefined)) }}
    actions={editorMode === 'edit'
      ? <MyIssuesFilterBar {...filterBarProps('base')} className={explorerStyles.cardBar} compact showEmpty wrap commands={<div className={explorerStyles.editorButtons}>{controls.filterButton}{controls.displayButton}</div>}/>
      : <>
          <div className={explorerStyles.editorFilters}>{controls.resourceTabs}<div className={explorerStyles.editorButtons}>{controls.filterButton}{controls.displayButton}</div></div>
          {filters.length > 0 && <MyIssuesFilterBar {...filterBarProps('base')} className={explorerStyles.cardBar} compact wrap commands={<span/>}/>}
        </>}
  />)

  const savedViewMenu = savedView && <SavedViewMenu
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
    onDelete={() => { if (onDeleteSavedView) void confirmAction(`Delete view “${savedView.name}”?`,{confirmLabel:'Delete view'}).then(confirmed=>{if(confirmed)return onDeleteSavedView(savedView)}) }}
  />

  const rowActions = { data, onUpdateIssue, onDeleteIssues, onOpenIssue: (issue: Issue) => onOpenIssue(issue), onCreateIssue }
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
      filters={filterTarget === 'extra' ? extraFilters : filters}
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
      className={className}
      insightsLabel={insightsLabel}
      detailsShortcutTooltip={Boolean(detailsPanel)}
      viewEditor={editorCard || undefined}
      viewEditorMode={editorMode}
      draftName={editorDraft?.name.trim() || undefined}
      draftPlaceholder={suggestion.name}
      draftVisual={editorDraft?.visual}
      filterActive={bandActive && extraFilters.length > 0}
      onAdvancedFilter={addAdvancedFilter}
      filterBar={bandActive
        ? extraFilters.length > 0 && <MyIssuesFilterBar {...filterBarProps('extra')} className={bandStyles.band} compact wrap commands={<SavedViewBandCommands canUpdate={Boolean(onUpdateSavedView)} onClear={() => setExtraFilters([])} onCreate={createViewFromBand} onUpdate={saveBandToView}/>}/>
        : !editorMode && <MyIssuesFilterBar {...filterBarProps('base')}/>}
      footer={hiddenByFilters > 0 && <HiddenByFiltersFooter hidden={hiddenByFilters} onClear={() => setExtraFilters([])}/>}
    >
      <IssuesSplitLayout
        detailsOpen={((detailsOpen && Boolean(previewIssue)) || split) && !insightsOpen}
        // Linear: with no issue open, details sit beside the list — the view's
        // card stack on saved views, the shared summary card elsewhere (same
        // component as My issues); custom detailsPanel pages keep their own.
        aside={detailsOpen && editorMode !== 'create' && !previewIssue && !split && !insightsOpen && !detailsPanel && !savedView ? <MyIssuesSummaryCard summary={summary} onItemSelect={summaryFilter}/> : savedView && detailsOpen && !previewIssue && !split && !insightsOpen ? <SavedViewDetailsPanel
          inline
          belowFilterBar={Boolean((bandActive && extraFilters.length > 0) || viewEditor)}
          favorite={savedViewFavorite}
          menu={savedViewMenu}
          onClose={() => changeDetails(false)}
          onSummaryItemSelect={(dimension, id, label, color) => addFilter(dimension === 'assignee' ? 'assignee' : dimension === 'project' ? 'project' : 'labels', { id, label, color })}
          onToggleFavorite={() => { if (onToggleSavedViewFavorite) void onToggleSavedViewFavorite(savedView) }}
          rows={rows}
          team={data.teams.find(team => team.id === savedView.teamId)}
          users={data.users}
          view={shownSavedView!}
          workspace={data.workspace}
        /> : undefined}
        list={<>
      {data.issueCollectionPaged && !drillRows ? <PagedIssueList
        data={data}
        query={pagedQuery}
        layout={display.layout === 'board' ? 'board' : 'list'}
        hiddenGroupIds={display.hiddenGroupIds}
        onHideGroup={id => changeDisplay({ ...display, hiddenGroupIds: [...display.hiddenGroupIds, id] })}
        onShowGroup={id => changeDisplay({ ...display, hiddenGroupIds: display.hiddenGroupIds.filter(value => value !== id) })}
        onMoveIssueRecord={(issue, input) => onUpdateIssue(issue.id, input)}
        onTotalChange={setPagedTotal}
        onLoadedIssuesChange={setPagedIssues}
        emptyState={emptyState ?? (effectiveFilters.length ? <NoMatchingIssues/> : undefined)}
        collapsedGroupIds={collapsedGroups}
        displayProperties={split || (detailsOpen && previewIssueId) ? splitProperties : display.properties}
        propertyOptions={rowOptions}
        selectedIds={selection.selectedIds}
        activeIssueId={split ? previewIssueId : undefined}
        mutationErrors={mutationErrors}
        onOpenIssueRecord={split ? issue => setPreviewIssueId(issue.id) : onOpenIssue}
        onCreateIssue={group => onCreateIssue?.(scope.kind === 'team' ? { ...group.createContext, teamId: scope.team.id } : group.createContext)}
        onGroupCollapsedChange={(id, collapsed) => setCollapsedGroups(current => { const next = new Set(current); if (collapsed) next.add(id); else next.delete(id); return next })}
        onPropertyChange={changeProperty}
        onSelectIssue={selection.selectIssue}
        onContextAction={(row, action) => { void contextAction(row, action) }}
      /> : display.layout !== 'board' ? <MyIssuesList
        groups={listGroups}
        selectedIds={selection.selectedIds}
        activeIssueId={split ? previewIssueId : undefined}
        collapsedGroupIds={collapsedGroups}
        displayProperties={split || (detailsOpen && previewIssueId) ? splitProperties : display.properties}
        nestedSubIssues={display.nestedSubIssues}
        emptyState={emptyState ?? (effectiveFilters.length ? <NoMatchingIssues/> : undefined)}
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
              view={shownSavedView!}
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
            {(!savedView || previewIssue) && (!detailsPanel || previewIssue) && <MyIssuesDetailsPane
              open={detailsOpen}
              width={detailsWidth}
              onWidthChange={setDetailsWidth}
              onClose={() => { if (previewIssueId) setPreviewIssueId(undefined); else changeDetails(false) }}
              selectedIssue={previewIssue ? issueToExplorerRow(previewIssue, data.workspace.urlKey, data.issues, data) : undefined}
              previewContent={previewIssue && renderIssuePreview ? renderIssuePreview(previewIssue, () => setPreviewIssueId(undefined)) : undefined}
              summary={summary}
              onSummaryItemSelect={summaryFilter}
            />}
            {detailsPanel && detailsOpen && !previewIssue && !insightsOpen && detailsPanel}
            {savedView && detailsOpen && !previewIssue && <SavedViewDetailsPanel
              favorite={savedViewFavorite}
              menu={savedViewMenu}
              onClose={() => changeDetails(false)}
              onSummaryItemSelect={(dimension, id, label, color) => addFilter(dimension === 'assignee' ? 'assignee' : dimension === 'project' ? 'project' : 'labels', { id, label, color })}
              onToggleFavorite={() => { if (onToggleSavedViewFavorite) void onToggleSavedViewFavorite(savedView) }}
              rows={rows}
              team={data.teams.find(team => team.id === savedView.teamId)}
              users={data.users}
              view={shownSavedView!}
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
/** Linear defaults: team/workspace/custom views hide triage issues (they live in Triage). */
function defaultDisplay(view: TeamIssuesRouteView, overrides?: Partial<MyIssuesDisplayOptions>): MyIssuesDisplayOptions { return { ...defaultMyIssuesDisplayOptions, grouping: 'status', completedWindow: view === 'all' ? 'all' : 'none', showTriageIssues: false, properties: new Set(defaultMyIssuesDisplayOptions.properties), ...overrides } }
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
function readFilters(key: string): MyIssuesAppliedFilter[] { try { return normalizeStoredFilters(JSON.parse(localStorage.getItem(key) ?? '[]')) } catch { return [] } }
function readDisplay(key: string, view: TeamIssuesRouteView, board = false, teamDefault?: Record<string, unknown>, overrides?: Partial<MyIssuesDisplayOptions>): MyIssuesDisplayOptions { const fallback = withTeamDefault({ ...defaultDisplay(view, overrides), ...(board ? { layout: 'board' as const, showEmptyGroups: true } : {}) }, teamDefault); try { const value = JSON.parse(localStorage.getItem(key) ?? 'null'); return value ? { ...fallback, ...value, properties: new Set(Array.isArray(value.properties) ? value.properties : [...fallback.properties]) } : fallback } catch { return fallback } }
function readBoolean(key: string, fallback: boolean) { try { const value = localStorage.getItem(key); return value == null ? fallback : value === 'true' } catch { return fallback } }
function writeValue(key: string, value: string) { try { localStorage.setItem(key, value) } catch { /* Preferences are best-effort. */ } }
function removeValue(key: string) { try { localStorage.removeItem(key) } catch { /* Preferences are best-effort. */ } }
function readPersonalDisplay(key: string, view: SavedView, routeView: TeamIssuesRouteView): MyIssuesDisplayOptions {
  const fallback = displayFromSavedView(view, routeView)
  try { const value = JSON.parse(localStorage.getItem(key) ?? 'null'); return value ? { ...fallback, ...value, properties: new Set(Array.isArray(value.properties) ? value.properties : [...fallback.properties]) } : fallback } catch { return fallback }
}
function readOrder(key: string): string[] { try { const value = JSON.parse(localStorage.getItem(key) ?? '[]'); return Array.isArray(value) && value.every(item => typeof item === 'string') ? value : [] } catch { return [] } }
function filtersFromSavedView(view: SavedView): MyIssuesAppliedFilter[] { return normalizeStoredFilters(view.filters) }
function displayFromSavedView(view: SavedView, routeView: TeamIssuesRouteView): MyIssuesDisplayOptions {
  const fallback = defaultDisplay(routeView)
  const value = view.display && typeof view.display === 'object' ? view.display : {}
  return { ...fallback, ...value, properties: new Set(Array.isArray(value.properties) ? value.properties as MyIssuesProperty[] : [...fallback.properties]) } as MyIssuesDisplayOptions
}
function withTeamDefault(display: MyIssuesDisplayOptions, teamDefault?: Record<string, unknown>): MyIssuesDisplayOptions {
  if (!teamDefault) return display
  return { ...display, ...teamDefault, properties: new Set(Array.isArray(teamDefault.properties) ? teamDefault.properties as MyIssuesProperty[] : [...display.properties]) } as MyIssuesDisplayOptions
}
function displaySnapshot(display: MyIssuesDisplayOptions): Record<string, unknown> { return { ...display, properties: [...display.properties] } }

/** Display toggles the reference offers: triage when a team in scope uses triage, sub-teams when the team has any. */
function displayToggles(teamId: string | undefined, data: BootstrapData): ('triage' | 'subTeam')[] {
  const settings = data.teamSettings ?? {}
  const teamIds = teamId ? [teamId] : data.teams.map(team => team.id)
  const toggles: ('triage' | 'subTeam')[] = []
  if (teamIds.some(id => settings[id]?.triageEnabled)) toggles.push('triage')
  if (teamId && Object.values(settings).some(item => item?.parentTeamId === teamId)) toggles.push('subTeam')
  return toggles
}

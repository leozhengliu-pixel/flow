import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { IssueRowActionsProvider } from './issue-row-actions'
import { PAGED_GROUPINGS, PAGED_ORDERINGS, pagedDisplayQuery } from '@/components/issue-explorer/issue-grouping'
import { boundedIssueSequence } from '@/lib/navigation-context'
import { PagedIssueList } from '@/components/issue-explorer/paged-issue-list'
import { issueFiltersToQueryAst } from './my-issues-filter-types'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import { MyIssuesBulkActionBar, type MyIssuesBulkAction, type MyIssuesBulkActionOption } from './my-issues-bulk-action-bar'
import { MyIssuesDetailsPane, type MyIssuesSummaryItem, type MyIssuesSummaryTab } from './my-issues-details-pane'
import { MyIssuesFilterBar, type MyIssuesAppliedFilter } from './my-issues-filter-bar'
import { MyIssuesList, type MyIssuesContextAction, type MyIssuesCreateContext, type MyIssuesEditableProperty, type MyIssuesGroupData, type MyIssuesRowData } from './my-issues-list'
import { defaultMyIssuesDisplayOptions } from './my-issues-display-defaults'
import { MyIssuesSurface, type MyIssuesDisplayOptions, type MyIssuesFilterKey, type MyIssuesFilterOption, type MyIssuesView } from './my-issues-surface'
import { useMyIssuesController } from './use-my-issues-controller'
import { applyExplorerFilters, executeExplorerBulkAction, explorerBoardGroupUpdate, explorerFilterOptions, explorerPropertyOptions, issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import { InsightHiddenNotice, SavedViewInsightsPanel, type SavedViewInsightsConfig } from '@/components/issue-explorer/saved-view-panels'
import { IssueBoard } from '@/components/issue-explorer/issue-board'
import type { SavedView } from '@/types/flow'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { fetchIssueRecord } from '@/lib/api'
import { toast } from 'sonner'
import { IssuesSplitLayout, IssueViewSplitPage } from '@/components/issues-split-view'

export interface MyIssuesPageProps {
  data: BootstrapData
  initialView?: MyIssuesView
  loading?: boolean
  error?: string
  workspaceSlug?: string
  onClearError?: () => void
  onCreateIssue?: (context?: MyIssuesCreateContext) => void
  onDeleteIssues: (issueIds: string[]) => Promise<void>
  onNavigateView?: (view: MyIssuesView, href: string) => void
  onOpenIssue: (issue: Issue, sequence?: string[]) => void
  onOpenSidebar?: () => void
  /** Full issue view for the split layout / preview pane. */
  renderIssuePreview?: (issue: Issue, onClose: () => void) => ReactNode
  onPersistDisplay?: (view: MyIssuesView, options: MyIssuesDisplayOptions) => Promise<void>
  onPersistFilters?: (view: MyIssuesView, filters: MyIssuesAppliedFilter[]) => Promise<void>
  onUpdateIssue: (issueId: string, input: IssueUpdateInput) => Promise<Issue>
  onUpdateIssues: (issueIds: string[], input: IssueUpdateInput) => Promise<Issue[]>
}

const FILTER_LABELS: Partial<Record<MyIssuesFilterKey, string>> = { ai:'AI filter',advanced:'Advanced filter',status:'Status',assignee:'Assignee',agent:'Agent',agentSession:'Agent Session',creator:'Creator',priority:'Priority',labels:'Labels',relations:'Relations',suggestedLabel:'Suggested label',dates:'Dates',projectMilestone:'Project milestone',project:'Project',projectProperties:'Project properties',initiative:'Initiative',cycle:'Cycle',addedToCycle:'Added to cycle',releases:'Releases',customers:'Customers',subscribers:'Subscribers',externalSource:'External source',autoClosed:'Auto-closed',content:'Content',links:'Links',template:'Template' }

export function MyIssuesPage({ data, initialView = 'assigned', loading = false, error, workspaceSlug = data.workspace.urlKey, onClearError, onCreateIssue, onDeleteIssues, onNavigateView, onOpenIssue, onOpenSidebar, renderIssuePreview, onPersistDisplay, onPersistFilters, onUpdateIssue, onUpdateIssues }: MyIssuesPageProps) {
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set())
  const [projectedView, setProjectedView] = useState(initialView)
  const [pagedIssues, setPagedIssues] = useState<Issue[]>([])
  const [filterOpenSignal, setFilterOpenSignal] = useState(0)
  const [insightsOpen,setInsightsOpen]=useState(false)
  const [drillRows,setDrillRows]=useState<MyIssuesRowData[]>()
  const [insightsConfig,setInsightsConfig]=useState<Record<string,unknown>>(()=>readInsights(`${workspaceSlug}:my-issues:${initialView}:insights`))
  const [previewIssueId, setPreviewIssueId] = useState<string>()
  const [mutationErrors, setMutationErrors] = useState<Map<string, string>>(new Map())
  const mutationSequence = useRef(new Map<string, number>())
  const mutationQueues = useRef(new Map<string, Promise<Issue>>())
  const retryUpdates = useRef(new Map<string, IssueUpdateInput>())
  const sourceIssues = useMemo(() => data.issueCollectionPaged ? pagedIssues : issuesForView(data, projectedView), [data, projectedView, pagedIssues])
  const sourceIssueIds = useMemo(() => new Set(sourceIssues.map(issue => issue.id)), [sourceIssues])
  const hierarchyIssues = useMemo(() => data.issueCollectionPaged ? sourceIssues : issuesWithHierarchyContext(sourceIssues, data.issues), [data.issueCollectionPaged, data.issues, sourceIssues])
  const initialGroups = useMemo(() => groupIssues(hierarchyIssues, workspaceSlug, data, sourceIssueIds), [data, hierarchyIssues, sourceIssueIds, workspaceSlug])
  const issuesById = useMemo(() => new Map([...data.issues, ...pagedIssues].map(issue => [issue.id, issue])), [data.issues, pagedIssues])
  const rowOptions = useMemo(() => explorerPropertyOptions(data, sourceIssues), [data, sourceIssues])

  const groupingContext = useMemo(() => ({ data }), [data])
  const controller = useMyIssuesController({
    drillRows,
    workspaceSlug,
    initialView,
    initialGroups,
    initialDisplay: defaultMyIssuesDisplayOptions,
    groupingContext,
    adapter: {
      navigate: href => onNavigateView?.(viewFromHref(href), href),
      persistDisplay: (view, options) => onPersistDisplay?.(view, options) ?? Promise.resolve(),
      persistFilters: onPersistFilters,
      executeBulk: async (action, ids, value) => (await executeExplorerBulkAction({ action, ids, value, data, issuesById, onUpdateIssue, onUpdateIssues, onDeleteIssues }))?.map(issue => toRow(issue, workspaceSlug, data, issueMatchesView(issue, data, projectedView))),
    },
  })
  const myIssuesPagedQuery = useMemo(() => {
    const { sort, direction, groupBy, archived, conditions } = pagedDisplayQuery(controller.display)
    return { archived, groupBy, sort, direction, filter: { and: [issueFiltersToQueryAst(controller.filters, { data }), { field: projectedView === 'created' ? 'creator' : projectedView === 'subscribed' ? 'subscribers' : projectedView === 'activity' ? 'myActivity' : projectedView === 'shared' ? 'sharedWith' : 'assignee', values: [data.viewer.id] }, ...conditions] } }
  }, [controller.display, controller.filters, data.viewer.id, projectedView])

  const addFilter = (field: MyIssuesFilterKey, option?: MyIssuesFilterOption) => {
    const fieldLabel = FILTER_LABELS[field]
    if (!fieldLabel || !option) return
    controller.addFilter({ id: `${field}-${option.id}-${Date.now()}`, field, fieldLabel, operator: 'is', value: option.id, valueLabel: option.label, color: option.color })
  }
  const updateOne = async (row: MyIssuesRowData, input: IssueUpdateInput) => {
    const sequence = (mutationSequence.current.get(row.id) ?? 0) + 1
    mutationSequence.current.set(row.id, sequence)
    retryUpdates.current.set(row.id, input)
    const snapshot = row
    controller.replaceGroups(current => replaceRow(current, optimisticRow(row, input, data)))
    setMutationErrors(current => withoutKey(current, row.id))
    const prior = mutationQueues.current.get(row.id)
    const request = (prior ? prior.catch(() => undefined) : Promise.resolve()).then(() => onUpdateIssue(row.id, input))
    mutationQueues.current.set(row.id, request)
    try {
      const updated = await request
      if (mutationSequence.current.get(row.id) === sequence) {
        controller.replaceGroups(current => replaceRow(current, toRow(updated, workspaceSlug, data, issueMatchesView(updated, data, projectedView))))
        retryUpdates.current.delete(row.id)
      }
      return updated
    } catch (failure) {
      if (mutationSequence.current.get(row.id) === sequence) {
        controller.replaceGroups(current => replaceRow(current, snapshot))
        setMutationErrors(current => withKey(current, row.id, failure instanceof Error ? failure.message : 'Could not update issue'))
      }
      throw failure
    } finally {
      if (mutationQueues.current.get(row.id) === request) mutationQueues.current.delete(row.id)
    }
  }
  const changeProperty = (row: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => {
    const update = updateForProperty(property, value)
    if (update) void updateOne(row, update).catch(() => undefined)
  }
  const contextAction = async (row: MyIssuesRowData, action: MyIssuesContextAction, value?: string) => {
    if (action === 'delete') {
      if (await confirmAction(`Delete ${row.identifier}?`,{description:'This cannot be undone.',confirmLabel:'Delete'})) await onDeleteIssues([row.id])
      return
    }
    if (action === 'copy' || action === 'copyUrl') { await navigator.clipboard.writeText(`${location.origin}${row.href ?? issueUrl(workspaceSlug, row.identifier)}`); return }
    if (action === 'copyId') { await navigator.clipboard.writeText(row.identifier); return }
    if (action === 'copyTitle') { await navigator.clipboard.writeText(row.title); return }
    const update = updateForAction(action, value)
    if (update) await updateOne(row, update)
  }
  const moveIssue = (row: MyIssuesRowData, sourceGroupId: string, targetGroupId: string, targetIndex: number) => {
    const targetGroup = controller.visibleGroups.find(group => group.id === targetGroupId)
    if (!targetGroup) return
    const targetIssues = targetGroup.issues.filter(issue => issue.id !== row.id)
    const before = targetIssues[targetIndex]
    const after = targetIssues[targetIndex - 1]
    const sortOrder = before
      ? (before.sortOrder ?? 0) - 1
      : after
        ? (after.sortOrder ?? 0) + 1
        : (row.sortOrder ?? 0)
    const grouping = controller.display.grouping
    const groupUpdate = explorerBoardGroupUpdate(row, grouping, targetGroupId, data)
    if (!Object.keys(groupUpdate).length && sourceGroupId !== targetGroupId) return
    const update: IssueUpdateInput = { ...groupUpdate, sortOrder }
    const snapshot = controller.groups
    const optimistic = optimisticRow(row, update, data)
    controller.replaceGroups(current => reorderMyIssuesGroups(current, optimistic, grouping, targetGroupId, targetIndex))
    void onUpdateIssue(row.id, update).then(updated => {
      controller.replaceGroups(current => reorderMyIssuesGroups(current, toRow(updated, workspaceSlug, data, issueMatchesView(updated, data, projectedView)), grouping, targetGroupId, targetIndex))
    }).catch(() => controller.replaceGroups(() => snapshot))
  }
  const summaryFilter = (tab: MyIssuesSummaryTab, item: MyIssuesSummaryItem) => {
    const field = tab === 'labels' ? 'labels' : tab === 'projects' ? 'project' : 'priority'
    addFilter(field, { id: item.id, label: item.label, color: item.color })
  }
  const displayedGroups = controller.visibleGroups
  // Empty status columns come from the shared grouping engine (showEmptyGroups).
  const boardGroups = displayedGroups
  const allInsightRows=useMemo(()=>insightsOpen?applyExplorerFilters(issuesForView(data,projectedView,true),controller.filters,data).map(issue=>issueToExplorerRow(issue,workspaceSlug,data.issues,data)):[],[controller.filters,data,insightsOpen,projectedView,workspaceSlug])
  const insightRows = useMemo(() => allInsightRows.filter(row => !row.archivedAt), [allInsightRows])
  const insightQuery = useMemo(() => ({ filter: { and: [issueFiltersToQueryAst(controller.filters, { data }), { field: projectedView === 'created' ? 'creator' : projectedView === 'subscribed' ? 'subscribers' : projectedView === 'activity' ? 'myActivity' : projectedView === 'shared' ? 'sharedWith' : 'assignee', values: [data.viewer.id] }] } }), [controller.filters, projectedView, data.viewer.id])
  const insightsView:SavedView={id:`my-issues-${controller.view}`,name:({assigned:'Assigned to me',created:'Created by me',subscribed:'Subscribed',activity:'Activity',shared:'Shared with me'} as const)[controller.view],description:'',resource:'issues',scope:'personal',ownerId:data.viewer.id,view:'all',filters:controller.filters,display:{},insights:insightsConfig,createdAt:'',updatedAt:''}
  const split = controller.display.layout === 'split'
  const splitRowIds = useMemo(() => data.issueCollectionPaged ? pagedIssues.map(issue => issue.id) : displayedGroups.flatMap(group => group.issues.map(issue => issue.id)), [data.issueCollectionPaged, displayedGroups, pagedIssues])
  useEffect(() => { if (split && (!previewIssueId || !splitRowIds.includes(previewIssueId)) && splitRowIds[0]) setPreviewIssueId(splitRowIds[0]) }, [previewIssueId, split, splitRowIds])
  useEffect(() => {
    if (!split) return
    const onKey = (event: KeyboardEvent) => {
      if (event.metaKey || event.ctrlKey || event.altKey || (event.target instanceof Element && event.target.closest('input,textarea,[contenteditable=true],[role=textbox],[role=dialog]'))) return
      const key = event.key.toLowerCase()
      const step = key === 'j' || key === 'arrowdown' ? 1 : key === 'k' || key === 'arrowup' ? -1 : 0
      if (!step) return
      event.preventDefault()
      const next = splitRowIds[Math.max(0, Math.min(splitRowIds.length - 1, splitRowIds.indexOf(previewIssueId ?? '') + step))]
      if (next) setPreviewIssueId(next)
    }
    addEventListener('keydown', onKey)
    return () => removeEventListener('keydown', onKey)
  }, [previewIssueId, split, splitRowIds])
  const splitProperties = useMemo(() => new Set([...controller.display.properties].filter(property => property === 'id' || property === 'status' || property === 'priority' || property === 'assignee')), [controller.display.properties])
  const previewIssue = previewIssueId ? issuesById.get(previewIssueId) : undefined
  const previewRow = previewIssue ? toRow(previewIssue, workspaceSlug, data, issueMatchesView(previewIssue, data, projectedView)) : undefined
  const openRow = (row: MyIssuesRowData) => {
    const sequence = boundedIssueSequence(displayedGroups.find(group => group.issues.some(issue => issue.id === row.id))?.issues.map(issue => issue.id) ?? [row.id], row.id)
    const issue = issuesById.get(row.id)
    if (controller.detailsOpen || split) {
      setPreviewIssueId(row.id)
      return
    }
    if (issue) onOpenIssue(issue, sequence)
    else void fetchIssueRecord(row.id, undefined, workspaceSlug).then(issue => onOpenIssue(issue, sequence)).catch(() => toast.error('Could not load issue'))
  }

  return <IssueRowActionsProvider value={{ data, onUpdateIssue, onDeleteIssues, onOpenIssue: issue => onOpenIssue?.(issue) }}>
    <MyIssuesSurface
      activeView={controller.view}
      detailsOpen={controller.detailsOpen}
      insightsOpen={insightsOpen}
      displayOptions={controller.display}
      displayMenuProps={{
        availableGroupings: data.issueCollectionPaged ? PAGED_GROUPINGS : undefined,
        availableOrderings: data.issueCollectionPaged ? PAGED_ORDERINGS : undefined,
        toggles: ['triage'],
        onReset: () => controller.changeDisplay(defaultMyIssuesDisplayOptions),
        resetLabel: 'Reset to default',
      }}
      filterOpenSignal={filterOpenSignal}
      filters={controller.filters}
      filterOptions={field => explorerFilterOptions(field, rowOptions)}
      viewCounts={data.issueCollectionPaged ? undefined : controller.counts}
      viewHref={controller.viewHref}
      onDetailsOpenChange={open=>{controller.setDetailsOpen(open);if(open)setInsightsOpen(false);else setPreviewIssueId(undefined)}}
      onInsightsOpenChange={open=>{setInsightsOpen(open);if(open){controller.setDetailsOpen(false);setInsightsConfig(readInsights(`${workspaceSlug}:my-issues:${controller.view}:insights`))}}}
      onDisplayOptionsChange={controller.changeDisplay}
      onFilterSelect={addFilter}
      onFilterToggle={(field, option) => { const fieldLabel = FILTER_LABELS[field]; if (fieldLabel) controller.toggleFilter(field, fieldLabel, option) }}
      onOpenSidebar={onOpenSidebar}
      onViewChange={view => { setProjectedView(view); controller.changeView(view) }}
      filterBar={<MyIssuesFilterBar
        filters={controller.filters}
        filterOptions={filter => explorerFilterOptions(filter.field, rowOptions)}
        saveState={controller.filterSaveState}
        onAdd={() => setFilterOpenSignal(value => value + 1)}
        onClear={controller.clearFilters}
        onOperatorChange={controller.changeFilterOperator}
        onRemove={controller.removeFilter}
        onSave={onPersistFilters ? controller.saveFilters : undefined}
        onValuesChange={controller.changeFilterValues}
      />}
    >
      <IssuesSplitLayout
        detailsOpen={controller.detailsOpen || split}
        list={<>
      {data.issueCollectionPaged && !drillRows ? <PagedIssueList
        data={data}
        onLoadedIssuesChange={setPagedIssues}
        layout={controller.display.layout === 'board' ? 'board' : 'list'}
        hiddenGroupIds={controller.display.hiddenGroupIds}
        onHideGroup={id => controller.changeDisplay({ ...controller.display, hiddenGroupIds: [...controller.display.hiddenGroupIds, id] })}
        onShowGroup={id => controller.changeDisplay({ ...controller.display, hiddenGroupIds: controller.display.hiddenGroupIds.filter(value => value !== id) })}
        onMoveIssueRecord={(issue, input) => onUpdateIssue(issue.id, input)}
        query={myIssuesPagedQuery}
        collapsedGroupIds={collapsedGroups}
        displayProperties={split ? splitProperties : controller.display.properties}
        propertyOptions={rowOptions}
        selectedIds={controller.selectedIds}
        activeIssueId={split ? previewIssueId : undefined}
        mutationErrors={mutationErrors}
        onOpenIssueRecord={split ? issue => setPreviewIssueId(issue.id) : onOpenIssue}
        onCreateIssue={group => onCreateIssue?.(group.createContext)}
        onGroupCollapsedChange={(id, collapsed) => setCollapsedGroups(current => { const next = new Set(current); if (collapsed) next.add(id); else next.delete(id); return next })}
        onPropertyChange={changeProperty}
        onSelectIssue={controller.selectIssue}
        onContextAction={(row, action) => { void contextAction(row, action) }}
      /> : controller.display.layout === 'board' ? <IssueBoard
        groups={boardGroups}
        hiddenGroupIds={controller.display.hiddenGroupIds}
        properties={controller.display.properties}
        propertyOptions={rowOptions}
        selectedIds={controller.selectedIds}
        onCreateIssue={group => onCreateIssue?.(group.createContext ?? (stateIdForGroup(group, data) ? { stateId: stateIdForGroup(group, data) } : undefined))}
        onHideGroup={groupId => controller.changeDisplay({ ...controller.display, hiddenGroupIds: [...new Set([...controller.display.hiddenGroupIds, groupId])] })}
        onShowGroup={groupId => controller.changeDisplay({ ...controller.display, hiddenGroupIds: controller.display.hiddenGroupIds.filter(id => id !== groupId) })}
        onMove={moveIssue}
        onOpenIssue={openRow}
        onPropertyChange={changeProperty}
        onSelectIssue={controller.selectIssue}
      /> : <MyIssuesList
        groups={displayedGroups}
        loading={loading}
        error={error}
        selectedIds={controller.selectedIds}
        activeIssueId={split ? previewIssueId : undefined}
        collapsedGroupIds={collapsedGroups}
        displayProperties={split ? splitProperties : controller.display.properties}
        nestedSubIssues={controller.display.nestedSubIssues}
        propertyOptions={rowOptions}
        mutationErrors={mutationErrors}
        onClearError={onClearError}
        onCreateIssue={group => onCreateIssue?.(group.createContext ?? (stateIdForGroup(group, data) ? { stateId: stateIdForGroup(group, data) } : undefined))}
        onGroupCollapsedChange={(id, collapsed) => setCollapsedGroups(current => { const next = new Set(current); if (collapsed) next.add(id); else next.delete(id); return next })}
        onOpenIssue={openRow}
        onPropertyChange={changeProperty}
        onRetryMutation={row => { const input = retryUpdates.current.get(row.id); if (input) void updateOne(row, input).catch(() => undefined) }}
        onSelectIssue={controller.selectIssue}
        onContextAction={(row, action) => { void contextAction(row, action) }}
      />}
        </>}
        detail={
          <IssueViewSplitPage
            workspaceSlug={workspaceSlug}
            origin={{ type: 'myIssues', view: controller.view }}
            selectedIssue={previewRow}
            preview={previewIssue && renderIssuePreview ? renderIssuePreview(previewIssue, () => setPreviewIssueId(undefined)) : undefined}
            summary={previewRow ? undefined : controller.summary}
            onClose={() => { if (previewIssueId) setPreviewIssueId(undefined); else controller.setDetailsOpen(false) }}
            onSummaryItemSelect={summaryFilter}
          />
        }
        fallbackDetail={
          <MyIssuesDetailsPane
            open={controller.detailsOpen}
            width={controller.detailsWidth}
            onWidthChange={controller.setDetailsWidth}
            onClose={() => { if (previewIssueId) setPreviewIssueId(undefined); else controller.setDetailsOpen(false) }}
            selectedIssue={previewRow}
            summary={controller.summary}
            onSummaryItemSelect={summaryFilter}
          />
        }
      />
      {drillRows && <InsightHiddenNotice hidden={drillRows.length - new Set(displayedGroups.flatMap(group => group.issues.map(issue => issue.id))).size} onShow={() => controller.changeDisplay({ ...controller.display, completedWindow: 'all', showSubIssues: true, hiddenGroupIds: [] })}/>}
      {insightsOpen && <SavedViewInsightsPanel
        allRows={allInsightRows}
        data={data}
        rows={insightRows}
        query={data.issueCollectionPaged ? insightQuery : undefined}
        onDrillChange={setDrillRows}
        onOpenIssue={openRow}
        view={insightsView}
        onClose={() => setInsightsOpen(false)}
        onSave={async (config: SavedViewInsightsConfig) => {
          const value = config as unknown as Record<string, unknown>
          setInsightsConfig(value)
          try { localStorage.setItem(`${workspaceSlug}:my-issues:${controller.view}:insights`, JSON.stringify(value)) } catch {}
        }}
      />}
    </MyIssuesSurface>
    <MyIssuesBulkActionBar selectedIssues={controller.selectedIssues} destructiveActions={['archive', 'delete']} loading={controller.bulkLoading} error={controller.bulkError} actionOptions={action => bulkOptions(action, rowOptions)} onAction={(action, _issues, value) => { void controller.executeBulk(action, value) }} onClear={controller.clearSelection}/>
  </IssueRowActionsProvider>
}

function issuesForView(data: BootstrapData, view: MyIssuesView, includeArchived = false) {
  const active = data.issues.filter(issue => includeArchived || !issue.archivedAt)
  return active.filter(issue => issueMatchesView(issue, data, view))
}

function issueMatchesView(issue: Issue, data: BootstrapData, view: MyIssuesView) {
  if (view === 'created') return issue.creator.id === data.viewer.id
  if (view === 'subscribed') return issue.subscriberIds.includes(data.viewer.id)
  if (view === 'activity') return Boolean(data.activities[issue.id]?.some(activity => activity.actor.id === data.viewer.id))
  if (view === 'shared') return isSharedWithViewer(issue, data)
  return issue.assignee?.id === data.viewer.id
}

/** Linear "Shared with me": issues granted to the viewer directly, or visible only through a share. */
function isSharedWithViewer(issue: Issue, data: BootstrapData) {
  if (issue.permissions?.some(permission => permission.subjectType === 'user' && permission.subjectId === data.viewer.id)) return true
  const memberOf = data.teamMembers.some(member => member.teamId === issue.team.id && member.userId === data.viewer.id)
  return !memberOf && Boolean(issue.permissions?.length) && data.teams.find(team => team.id === issue.team.id)?.private === true
}

function issuesWithHierarchyContext(primary: Issue[], issues: Issue[]) {
  const byId=new Map(issues.map(issue=>[issue.id,issue])),children=new Map<string,Issue[]>(),included=new Set(primary.map(issue=>issue.id))
  for(const issue of issues){if(!issue.parentId)continue;const siblings=children.get(issue.parentId)??[];siblings.push(issue);children.set(issue.parentId,siblings)}
  const addDescendants=(id:string)=>{for(const child of children.get(id)??[]){if(child.archivedAt||included.has(child.id))continue;included.add(child.id);addDescendants(child.id)}}
  for(const issue of primary){addDescendants(issue.id);let parentId=issue.parentId;while(parentId){const parent=byId.get(parentId);if(!parent||parent.archivedAt||included.has(parent.id))break;included.add(parent.id);parentId=parent.parentId}}
  return issues.filter(issue=>included.has(issue.id)&&!issue.archivedAt)
}

function groupIssues(issues: Issue[], workspaceSlug: string, data: BootstrapData, viewMatches: ReadonlySet<string>): MyIssuesGroupData[] {
  const groups = new Map<string, MyIssuesGroupData>()
  for (const issue of issues) {
    const id = issue.state.type === 'started' ? 'other-active' : issue.state.id
    const group = groups.get(id) ?? { id, label: issue.state.type === 'started' ? 'Other active' : issue.state.name, stateType: issue.state.type, state: issue.state, createContext: issue.state.type === 'started' ? undefined : { stateId: issue.state.id }, issues: [] }
    group.issues.push(toRow(issue, workspaceSlug, data, viewMatches.has(issue.id))); groups.set(id, group)
  }
  return [...groups.values()]
}

function toRow(issue: Issue, workspaceSlug: string, data: BootstrapData, viewMatch = true): MyIssuesRowData {
  const sla=data.issueSlas.find(item=>item.issueId===issue.id&&item.status!=='removed');const rule=sla?data.slaRules.find(item=>item.id===sla.ruleId):undefined
  return { ...issueToExplorerRow(issue,workspaceSlug,data.issues,data), viewMatch, sla:sla?{...sla,ruleName:rule?.name}:undefined }
}

function bulkOptions(action: MyIssuesBulkAction, options: ReturnType<typeof explorerPropertyOptions>): MyIssuesBulkActionOption[] | undefined {
  if (action === 'status') return options.status
  if (action === 'priority') return options.priority
  if (action === 'assign') return options.assignee
  if (action === 'project') return options.project
  if (action === 'labels') return options.labels
  if (action === 'dueDate') return dueDateOptions()
  if (action === 'subscribers') return options.assignee.filter(option => option.id)
}

function updateForAction(action: MyIssuesBulkAction | MyIssuesContextAction, value?: string): IssueUpdateInput | undefined {
  if (value == null) return
  if (action === 'status') return { stateId: value }
  if (action === 'priority') return { priority: Number(value) }
  if (action === 'assign' || action === 'assignee') return { assigneeId: value }
  if (action === 'project') return { projectId: value }
  if (action === 'cycle') return { cycleId: value }
  if (action === 'dueDate') return { dueDate: value }
}

function updateForProperty(property: MyIssuesEditableProperty, value: string | string[]): IssueUpdateInput | undefined {
  if (property === 'labels' && Array.isArray(value)) return { labelIds: value }
  if (Array.isArray(value)) return
  if (property === 'status') return { stateId: value }
  if (property === 'priority') return { priority: Number(value) }
  if (property === 'assignee') return { assigneeId: value }
  if (property === 'project') return { projectId: value }
  if (property === 'cycle') return { cycleId: value }
  if (property === 'dueDate') return { dueDate: value }
}

function optimisticRow(row: MyIssuesRowData, input: IssueUpdateInput, data: BootstrapData): MyIssuesRowData {
  return {
    ...row,
    state: input.stateId === undefined ? row.state : data.states.find(state => state.id === input.stateId) ?? row.state,
    priority: input.priority === undefined ? row.priority : clampPriority(input.priority),
    assignee: input.assigneeId === undefined ? row.assignee : input.assigneeId ? (() => { const user = data.users.find(item => item.id === input.assigneeId); return user ? { id: user.id, name: user.displayName, avatarUrl: user.avatarUrl } : row.assignee })() : undefined,
    project: input.projectId === undefined ? row.project : input.projectId ? data.projects.find(project => project.id === input.projectId) : undefined,
    cycleId: input.cycleId === undefined ? row.cycleId : input.cycleId || undefined,
    cycleName: input.cycleId === undefined ? row.cycleName : data.cycles.find(cycle => cycle.id === input.cycleId)?.name,
    dueDate: input.dueDate === undefined ? row.dueDate : input.dueDate || undefined,
    labels: input.labelIds === undefined ? row.labels : input.labelIds.map(id => data.labels.find(label => label.id === id)).filter((label): label is NonNullable<typeof label> => Boolean(label)),
    sortOrder: input.sortOrder === undefined ? row.sortOrder : input.sortOrder,
    updatedAt: new Date().toISOString(),
  }
}

function replaceRow(groups: MyIssuesGroupData[], row: MyIssuesRowData) { return groups.map(group => ({ ...group, issues: group.issues.map(issue => issue.id === row.id ? row : issue) })) }
function reorderMyIssuesGroups(groups: MyIssuesGroupData[], row: MyIssuesRowData, grouping: MyIssuesDisplayOptions['grouping'], targetGroupId: string, targetIndex: number) {
  const replaced = groups.map(group => ({ ...group, issues: group.issues.filter(issue => issue.id !== row.id) }))
  const target = replaced.find(group => group.id === targetGroupId || false)
  if (target) {
    const issues = [...target.issues]
    issues.splice(Math.max(0, Math.min(targetIndex, issues.length)), 0, row)
    target.issues = issues
    return replaced
  }
  if (replaced[0]) replaced[0].issues = [...replaced[0].issues, row]
  return replaced
}
function withoutKey(map: Map<string, string>, key: string) { const next = new Map(map); next.delete(key); return next }
function withKey(map: Map<string, string>, key: string, value: string) { const next = new Map(map); next.set(key, value); return next }
function readInsights(key:string):Record<string,unknown>{try{const value=JSON.parse(localStorage.getItem(key)??'{}');return value&&typeof value==='object'&&!Array.isArray(value)?value:{}}catch{return {}}}


function dueDateOptions(): MyIssuesBulkActionOption[] {
  const date = new Date(), day = 86_400_000
  return [{ id: '', label: 'No due date' }, { id: isoDate(date), label: 'Today' }, { id: isoDate(new Date(date.getTime() + day)), label: 'Tomorrow' }, { id: isoDate(new Date(date.getTime() + day * 7)), label: 'In one week' }]
}
function stateIdForGroup(group: MyIssuesGroupData, data: BootstrapData) { return group.createContext?.stateId ?? (group.id === 'focus-active' ? data.states.find(state => state.type === 'started')?.id : data.states.find(state => state.id === group.id)?.id) }
function viewFromHref(href: string): MyIssuesView { return (href.split('/').at(-1) as MyIssuesView) ?? 'assigned' }
function issueUrl(workspaceSlug: string, identifier: string) { return `${location.origin}/${workspaceSlug}/issue/${identifier}` }
function clampPriority(value: number): 0 | 1 | 2 | 3 | 4 { return Math.max(0, Math.min(4, value)) as 0 | 1 | 2 | 3 | 4 }
function isoDate(date: Date) { return date.toISOString().slice(0, 10) }

import { useMemo, useRef, useState } from 'react'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import { MyIssuesBulkActionBar, type MyIssuesBulkAction, type MyIssuesBulkActionOption } from './my-issues-bulk-action-bar'
import { MyIssuesDetailsPane, type MyIssuesSummaryItem, type MyIssuesSummaryTab } from './my-issues-details-pane'
import { MyIssuesFilterBar, type MyIssuesAppliedFilter } from './my-issues-filter-bar'
import { MyIssuesList, type MyIssuesContextAction, type MyIssuesCreateContext, type MyIssuesEditableProperty, type MyIssuesGroupData, type MyIssuesRowData } from './my-issues-list'
import { defaultMyIssuesDisplayOptions } from './my-issues-display-defaults'
import { MyIssuesSurface, type MyIssuesDisplayOptions, type MyIssuesFilterKey, type MyIssuesFilterOption, type MyIssuesView } from './my-issues-surface'
import { useMyIssuesController } from './use-my-issues-controller'
import { applyExplorerFilters, explorerFilterOptions, explorerPropertyOptions, issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import { SavedViewInsightsPanel, type SavedViewInsightsConfig } from '@/components/issue-explorer/saved-view-panels'
import type { SavedView } from '@/types/flow'
import { setGroupedLabelSelected } from '@/lib/labels'
import { confirmAction } from '@/components/ui/action-dialog-service'

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
  onOpenIssue: (issue: Issue) => void
  onOpenSidebar?: () => void
  onPersistDisplay?: (view: MyIssuesView, options: MyIssuesDisplayOptions) => Promise<void>
  onPersistFilters?: (view: MyIssuesView, filters: MyIssuesAppliedFilter[]) => Promise<void>
  onUpdateIssue: (issueId: string, input: IssueUpdateInput) => Promise<Issue>
  onUpdateIssues: (issueIds: string[], input: IssueUpdateInput) => Promise<Issue[]>
}

const FILTER_LABELS: Partial<Record<MyIssuesFilterKey, string>> = { ai:'AI filter',advanced:'Advanced filter',status:'Status',assignee:'Assignee',agent:'Agent',agentSession:'Agent Session',creator:'Creator',priority:'Priority',labels:'Labels',relations:'Relations',suggestedLabel:'Suggested label',dates:'Dates',project:'Project',projectProperties:'Project properties',initiative:'Initiative',cycle:'Cycle',addedToCycle:'Added to cycle',releases:'Releases',subscribers:'Subscribers',externalSource:'External source',autoClosed:'Auto-closed',content:'Content',links:'Links',template:'Template' }

export function MyIssuesPage({ data, initialView = 'assigned', loading = false, error, workspaceSlug = data.workspace.urlKey, onClearError, onCreateIssue, onDeleteIssues, onNavigateView, onOpenIssue, onOpenSidebar, onPersistDisplay, onPersistFilters, onUpdateIssue, onUpdateIssues }: MyIssuesPageProps) {
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set())
  const [projectedView, setProjectedView] = useState(initialView)
  const [filterOpenSignal, setFilterOpenSignal] = useState(0)
  const [insightsOpen,setInsightsOpen]=useState(false)
  const [insightsConfig,setInsightsConfig]=useState<Record<string,unknown>>(()=>readInsights(`${workspaceSlug}:my-issues:${initialView}:insights`))
  const [mutationErrors, setMutationErrors] = useState<Map<string, string>>(new Map())
  const mutationSequence = useRef(new Map<string, number>())
  const mutationQueues = useRef(new Map<string, Promise<Issue>>())
  const retryUpdates = useRef(new Map<string, IssueUpdateInput>())
  const sourceIssues = useMemo(() => issuesForView(data, projectedView), [data, projectedView])
  const sourceIssueIds = useMemo(() => new Set(sourceIssues.map(issue => issue.id)), [sourceIssues])
  const hierarchyIssues = useMemo(() => issuesWithHierarchyContext(sourceIssues, data.issues), [data.issues, sourceIssues])
  const initialGroups = useMemo(() => groupIssues(hierarchyIssues, workspaceSlug, data, sourceIssueIds), [data, hierarchyIssues, sourceIssueIds, workspaceSlug])
  const issuesById = useMemo(() => new Map(data.issues.map(issue => [issue.id, issue])), [data.issues])
  const rowOptions = useMemo(() => explorerPropertyOptions(data, sourceIssues), [data, sourceIssues])

  const controller = useMyIssuesController({
    workspaceSlug,
    initialView,
    initialGroups,
    initialDisplay: defaultMyIssuesDisplayOptions,
    adapter: {
      navigate: href => onNavigateView?.(viewFromHref(href), href),
      persistDisplay: (view, options) => onPersistDisplay?.(view, options) ?? Promise.resolve(),
      persistFilters: onPersistFilters,
      executeBulk: async (action, ids, value) => (await executeBulkAction({ action, ids, value, data, issuesById, onUpdateIssue, onUpdateIssues }))?.map(issue => toRow(issue, workspaceSlug, data, issueMatchesView(issue, data, projectedView))),
    },
  })

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
  const summaryFilter = (tab: MyIssuesSummaryTab, item: MyIssuesSummaryItem) => {
    const field = tab === 'labels' ? 'labels' : tab === 'projects' ? 'project' : 'priority'
    addFilter(field, { id: item.id, label: item.label, color: item.color })
  }
  const insightRows=controller.visibleGroups.flatMap(group=>group.issues)
  const allInsightRows=useMemo(()=>applyExplorerFilters(issuesForView(data,projectedView,true),controller.filters,data).map(issue=>issueToExplorerRow(issue,workspaceSlug,data.issues,data)),[controller.filters,data,projectedView,workspaceSlug])
  const insightsView:SavedView={id:`my-issues-${controller.view}`,name:({assigned:'Assigned to me',created:'Created by me',subscribed:'Subscribed',activity:'Activity'} as const)[controller.view],description:'',resource:'issues',scope:'personal',ownerId:data.viewer.id,view:'all',filters:controller.filters,display:{},insights:insightsConfig,createdAt:'',updatedAt:''}

  return <>
    <MyIssuesSurface
      activeView={controller.view}
      detailsOpen={controller.detailsOpen}
      insightsOpen={insightsOpen}
      displayOptions={controller.display}
      filterOpenSignal={filterOpenSignal}
      filters={controller.filters}
      filterOptions={field => explorerFilterOptions(field, rowOptions)}
      viewCounts={controller.counts}
      viewHref={controller.viewHref}
      onDetailsOpenChange={open=>{controller.setDetailsOpen(open);if(open)setInsightsOpen(false)}}
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
      <MyIssuesList
        groups={controller.visibleGroups}
        loading={loading}
        error={error}
        selectedIds={controller.selectedIds}
        collapsedGroupIds={collapsedGroups}
        displayProperties={controller.display.properties}
        nestedSubIssues={controller.display.nestedSubIssues}
        propertyOptions={rowOptions}
        mutationErrors={mutationErrors}
        onClearError={onClearError}
        onCreateIssue={group => onCreateIssue?.(group.createContext ?? (stateIdForGroup(group, data) ? { stateId: stateIdForGroup(group, data) } : undefined))}
        onGroupCollapsedChange={(id, collapsed) => setCollapsedGroups(current => { const next = new Set(current); if (collapsed) next.add(id); else next.delete(id); return next })}
        onOpenIssue={row => { const issue = issuesById.get(row.id); if (issue) onOpenIssue(issue) }}
        onPropertyChange={changeProperty}
        onRetryMutation={row => { const input = retryUpdates.current.get(row.id); if (input) void updateOne(row, input).catch(() => undefined) }}
        onSelectIssue={controller.selectIssue}
        onContextAction={(row, action) => { void contextAction(row, action) }}
      />
      <MyIssuesDetailsPane open={controller.detailsOpen} width={controller.detailsWidth} onWidthChange={controller.setDetailsWidth} onClose={() => controller.setDetailsOpen(false)} summary={controller.summary} onSummaryItemSelect={summaryFilter}/>
      {insightsOpen && <SavedViewInsightsPanel
        allRows={allInsightRows}
        data={data}
        rows={insightRows}
        view={insightsView}
        onClose={() => setInsightsOpen(false)}
        onSave={async (config: SavedViewInsightsConfig) => {
          const value = config as unknown as Record<string, unknown>
          setInsightsConfig(value)
          try { localStorage.setItem(`${workspaceSlug}:my-issues:${controller.view}:insights`, JSON.stringify(value)) } catch {}
        }}
      />}
    </MyIssuesSurface>
    <MyIssuesBulkActionBar selectedIssues={controller.selectedIssues} loading={controller.bulkLoading} error={controller.bulkError} actionOptions={action => bulkOptions(action, rowOptions)} onAction={(action, _issues, value) => { void controller.executeBulk(action, value) }} onClear={controller.clearSelection}/>
  </>
}

function issuesForView(data: BootstrapData, view: MyIssuesView, includeArchived = false) {
  const active = data.issues.filter(issue => includeArchived || !issue.archivedAt)
  return active.filter(issue => issueMatchesView(issue, data, view))
}

function issueMatchesView(issue: Issue, data: BootstrapData, view: MyIssuesView) {
  if (view === 'created') return issue.creator.id === data.viewer.id
  if (view === 'subscribed') return issue.subscriberIds.includes(data.viewer.id)
  if (view === 'activity') return Boolean(data.activities[issue.id]?.some(activity => activity.actor.id === data.viewer.id))
  return issue.assignee?.id === data.viewer.id
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

async function executeBulkAction({ action, ids, value, data, issuesById, onUpdateIssue, onUpdateIssues }: { action: MyIssuesBulkAction; ids: string[]; value?: string; data: BootstrapData; issuesById: Map<string, Issue>; onUpdateIssue: (id: string, input: IssueUpdateInput) => Promise<Issue>; onUpdateIssues: (ids: string[], input: IssueUpdateInput) => Promise<Issue[]> }): Promise<Issue[] | void> {
  if (action.startsWith('copy')) { await copyIssues(action, ids, issuesById, data.workspace.urlKey); return }
  if (action === 'labels' && value != null) {
    const selected = !ids.every(id => issuesById.get(id)?.labels.some(label => label.id === value))
    return Promise.all(ids.map(id => { const issue = issuesById.get(id)!; return onUpdateIssue(id, { labelIds: setGroupedLabelSelected(issue.labels.map(label => label.id), value, data.labels, selected) }) }))
  }
  if (action === 'subscribers' && value != null) return Promise.all(ids.map(id => { const issue = issuesById.get(id)!; return onUpdateIssue(id, { subscriberIds: issue.subscriberIds.includes(value) ? issue.subscriberIds : [...issue.subscriberIds, value] }) }))
  if (action === 'removeSubscribers') return Promise.all(ids.map(id => onUpdateIssue(id, { subscriberIds: [] })))
  if (action === 'unassignMe') return onUpdateIssues(ids, { assigneeId: '' })
  const update = updateForAction(action, value)
  if (update) return onUpdateIssues(ids, update)
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
    dueDate: input.dueDate === undefined ? row.dueDate : input.dueDate || undefined,
    labels: input.labelIds === undefined ? row.labels : input.labelIds.map(id => data.labels.find(label => label.id === id)).filter((label): label is NonNullable<typeof label> => Boolean(label)),
    updatedAt: new Date().toISOString(),
  }
}

function replaceRow(groups: MyIssuesGroupData[], row: MyIssuesRowData) { return groups.map(group => ({ ...group, issues: group.issues.map(issue => issue.id === row.id ? row : issue) })) }
function withoutKey(map: Map<string, string>, key: string) { const next = new Map(map); next.delete(key); return next }
function withKey(map: Map<string, string>, key: string, value: string) { const next = new Map(map); next.set(key, value); return next }
function readInsights(key:string):Record<string,unknown>{try{const value=JSON.parse(localStorage.getItem(key)??'{}');return value&&typeof value==='object'&&!Array.isArray(value)?value:{}}catch{return {}}}

async function copyIssues(action: MyIssuesBulkAction, ids: string[], issuesById: Map<string, Issue>, workspaceSlug: string) {
  const issues = ids.map(id => issuesById.get(id)).filter(Boolean) as Issue[]
  const lines = issues.map(issue => {
    const url = issueUrl(workspaceSlug, issue.identifier)
    if (action === 'copyId') return issue.identifier
    if (action === 'copyUrl') return url
    if (action === 'copyTitle') return issue.title
    if (action === 'copyTitleLink') return `[${issue.title}](${url})`
    if (action === 'copyDescriptionMarkdown') return issue.description
    if (action === 'copyBranch') return `${issue.identifier.toLowerCase()}-${slug(issue.title)}`
    if (action === 'copyPrompt') return `${issue.identifier}: ${issue.title}\n\n${issue.description}`
    return `# ${issue.identifier}: ${issue.title}\n\n${issue.description}\n\n${url}`
  })
  await navigator.clipboard.writeText(lines.join('\n\n'))
}

function dueDateOptions(): MyIssuesBulkActionOption[] {
  const date = new Date(), day = 86_400_000
  return [{ id: '', label: 'No due date' }, { id: isoDate(date), label: 'Today' }, { id: isoDate(new Date(date.getTime() + day)), label: 'Tomorrow' }, { id: isoDate(new Date(date.getTime() + day * 7)), label: 'In one week' }]
}
function stateIdForGroup(group: MyIssuesGroupData, data: BootstrapData) { return group.id === 'other-active' ? data.states.find(state => state.type === 'started')?.id : group.id }
function viewFromHref(href: string): MyIssuesView { return (href.split('/').at(-1) as MyIssuesView) ?? 'assigned' }
function issueUrl(workspaceSlug: string, identifier: string) { return `${location.origin}/${workspaceSlug}/issue/${identifier}` }
function clampPriority(value: number): 0 | 1 | 2 | 3 | 4 { return Math.max(0, Math.min(4, value)) as 0 | 1 | 2 | 3 | 4 }
function isoDate(date: Date) { return date.toISOString().slice(0, 10) }
function slug(value: string) { return value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 50) }

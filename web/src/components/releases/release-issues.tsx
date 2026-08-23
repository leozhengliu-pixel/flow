import { useMemo, useState } from 'react'
import { Filter, Unlink } from 'lucide-react'
import { toast } from 'sonner'

import { IssueBoard } from '@/components/issue-explorer/issue-board'
import { MyIssuesDisplayMenu } from '@/components/my-issues/my-issues-display-menu'
import { MyIssuesFilterBar } from '@/components/my-issues/my-issues-filter-bar'
import { filterValues, toggleFilterOption, updateFilterOperator, updateFilterValues, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import { MyIssuesList, type MyIssuesContextAction, type MyIssuesEditableProperty, type MyIssuesGroupData, type MyIssuesRowData, type MyIssuesRowPropertyOptions } from '@/components/my-issues/my-issues-list'
import type { MyIssuesDisplayOptions, MyIssuesFilterKey, MyIssuesFilterOption, MyIssuesGrouping } from '@/components/my-issues/my-issues-surface'
import { labelsForResource } from '@/lib/labels'
import { issuePath } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue, IssueUpdateInput, Release } from '@/types/flow'

import { ReleaseStatusIcon } from './release-icons'

const FILTER_LABELS: Partial<Record<MyIssuesFilterKey, string>> = {
  status: 'Status', assignee: 'Assignee', priority: 'Priority', labels: 'Labels', project: 'Project',
}

const RELEASE_DISPLAY: MyIssuesDisplayOptions = {
  layout: 'list', grouping: 'status', groupOrder: 'asc', subGrouping: 'none', ordering: 'priority',
  completedWindow: 'all', orderCompletedByRecency: false, showSubIssues: true, showEmptyGroups: false,
  nestedSubIssues: false, hiddenGroupIds: [], properties: new Set(['id', 'assignee', 'priority', 'project', 'labels', 'created']),
}

export function ReleaseIssues({ data, release, onAddIssues, onDeleteIssues, onOpenIssue, onRemoveIssues, onUpdateIssue }: {
  data: BootstrapData
  release: Release
  onAddIssues: () => void
  onDeleteIssues: (ids: string[]) => Promise<void>
  onOpenIssue: (issue: Issue) => void
  onRemoveIssues: (ids: string[]) => Promise<void>
  onUpdateIssue: (id: string, input: IssueUpdateInput) => Promise<Issue>
}) {
  const { t } = useI18n()
  const [display, setDisplay] = useState<MyIssuesDisplayOptions>(() => ({ ...RELEASE_DISPLAY, properties: new Set(RELEASE_DISPLAY.properties) }))
  const [filters, setFilters] = useState<MyIssuesAppliedFilter[]>([])
  const [filterOpen, setFilterOpen] = useState(false)
  const [displayOpen, setDisplayOpen] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const releaseIssues = useMemo(() => data.issues.filter(issue => release.issueIds.includes(issue.id) && !issue.archivedAt), [data.issues, release.issueIds])
  const issueMap = useMemo(() => new Map(releaseIssues.map(issue => [issue.id, issue])), [releaseIssues])
  const states = useMemo(() => [...data.states].sort((left, right) => left.position - right.position), [data.states])
  const options = useMemo(() => propertyOptions(data, releaseIssues), [data, releaseIssues])
  const visible = useMemo(() => sortIssues(releaseIssues.filter(issue => matchesFilters(issue, filters)).filter(issue => display.showSubIssues || !issue.parentId).filter(issue => includeCompleted(issue, display.completedWindow, data)), display), [data, display, filters, releaseIssues])
  const groups = useMemo(() => groupIssues(visible, display, states, data), [data, display, states, visible])

  const changeProperty = async (row: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => {
    const input = updateForProperty(property, value)
    if (!input) return
    try { await onUpdateIssue(row.id, input) }
    catch (error) { toast.error(error instanceof Error ? error.message : 'Could not update issue') }
  }
  const contextAction = async (row: MyIssuesRowData, action: MyIssuesContextAction) => {
    const issue = issueMap.get(row.id)
    if (!issue) return
    if (action === 'copy') { await navigator.clipboard.writeText(`${location.origin}${issuePath(data.workspace.urlKey, issue)}`); return }
    if (action === 'delete') {
      if (window.confirm(`Delete ${issue.identifier}? This cannot be undone.`)) await onDeleteIssues([issue.id])
      return
    }
    onOpenIssue(issue)
  }
  const select = (id: string, checked: boolean) => setSelected(current => { const next = new Set(current); if (checked) next.add(id); else next.delete(id); return next })
  const move = (row: MyIssuesRowData, sourceGroupId: string, targetGroupId: string, targetIndex: number) => {
    const target = groups.find(group => group.id === targetGroupId)
    const before = target?.issues[targetIndex]
    const after = target?.issues[targetIndex - 1]
    const sortOrder = before && after ? ((before.sortOrder ?? 0) + (after.sortOrder ?? 0)) / 2 : before ? (before.sortOrder ?? 1) - 1 : (after?.sortOrder ?? 0) + 1
    const stateId = display.grouping === 'status' && sourceGroupId !== targetGroupId && states.some(state => state.id === targetGroupId) ? targetGroupId : undefined
    void onUpdateIssue(row.id, { sortOrder, ...(stateId ? { stateId } : {}) })
  }
  const toggleFilter = (field: MyIssuesFilterKey, option: MyIssuesFilterOption) => {
    const label = FILTER_LABELS[field]
    if (label) setFilters(current => toggleFilterOption(current, field, label, option))
  }
  const filterOptions = (field: MyIssuesFilterKey) => field in options ? options[field as keyof MyIssuesRowPropertyOptions] : undefined

  if (!releaseIssues.length) return <div className="flow-release-issues-empty"><ReleaseStatusIcon status={release.status}/><strong data-i18n-ignore>{release.name}</strong><span>{t('No issues in this release yet.')}</span><button onClick={onAddIssues}>{t('Add issues')}</button></div>

  return <div className="flow-release-issues-view" data-layout={display.layout}>
    <div className="flow-release-issues-tools">
      <MyIssuesFilterMenu availableFields={['status','assignee','priority','labels','project']} filters={filters} onOpenChange={setFilterOpen} onToggle={toggleFilter} open={filterOpen} options={filterOptions} trigger={<button className="flow-release-detail-tool" aria-label="Add filter" type="button"><Filter/></button>}/>
      <MyIssuesDisplayMenu availableGroupings={['none','status','priority','project','assignee','cycle','label','team']} hideSubGrouping hiddenProperties={['milestone','links','customers','customerRevenue','timeInStatus']} onChange={setDisplay} onOpenChange={setDisplayOpen} open={displayOpen} options={display}/>
    </div>
    <MyIssuesFilterBar filters={filters} filterOptions={filter => filterOptions(filter.field)} onAdd={() => setFilterOpen(true)} onClear={() => setFilters([])} onOperatorChange={(id, operator) => setFilters(current => updateFilterOperator(current, id, operator))} onRemove={id => setFilters(current => current.filter(filter => filter.id !== id))} onValuesChange={(id, values) => setFilters(current => updateFilterValues(current, id, values))}/>
    <div className="flow-release-issues-scroll">
      {groups.some(group => group.issues.length) ? display.layout === 'list' ? <MyIssuesList collapsedGroupIds={collapsed} createIssueLabel={t('Add issues to release')} displayProperties={display.properties} groups={groups} nestedSubIssues={display.nestedSubIssues} onContextAction={(row, action) => { void contextAction(row, action) }} onCreateIssue={onAddIssues} onGroupCollapsedChange={(id, isCollapsed) => setCollapsed(current => { const next = new Set(current); if (isCollapsed) next.add(id); else next.delete(id); return next })} onOpenIssue={row => { const issue = issueMap.get(row.id); if (issue) onOpenIssue(issue) }} onPropertyChange={changeProperty} onSelectIssue={select} propertyOptions={options} selectedIds={selected}/>
        : <IssueBoard createIssueLabel={t('Add issues')} groups={groups} onCreateIssue={onAddIssues} onMove={move} onOpenIssue={row => { const issue = issueMap.get(row.id); if (issue) onOpenIssue(issue) }} onSelectIssue={select} properties={display.properties} selectedIds={selected}/>
        : <div className="flow-release-issues-no-results"><strong>{t('No matching issues')}</strong><button onClick={() => setFilters([])}>{t('Clear filters')}</button></div>}
    </div>
    {selected.size > 0 && <div className="flow-release-issues-bulk"><span>{selected.size} {t('selected')}</span><button onClick={() => setSelected(new Set())}>{t('Clear')}</button><button onClick={() => void onRemoveIssues([...selected]).then(() => setSelected(new Set()))}><Unlink/>{t('Remove from release')}</button></div>}
  </div>
}

function propertyOptions(data: BootstrapData, issues: Issue[]): MyIssuesRowPropertyOptions {
  const count = (predicate: (issue: Issue) => boolean) => issues.filter(predicate).length
  const labels = labelsForResource(data.labels, 'issue')
  const groups = new Map(data.labelGroups.filter(group => group.resourceType === 'issue').map(group => [group.id, group.name]))
  return {
    status: [...data.states].sort((a,b) => a.position-b.position).map(state => ({ id: state.id, label: state.name, color: state.color, kind: 'status', stateType: state.type, issueCount: count(issue => issue.state.id === state.id) })),
    priority: ['No priority','Urgent','High','Medium','Low'].map((label, priority) => ({ id: String(priority), label, kind: 'priority', priority: priority as 0|1|2|3|4, issueCount: count(issue => issue.priority === priority) })),
    assignee: [{ id: '', label: 'No assignee', kind: 'assignee' }, ...data.users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, kind: 'assignee' as const }))],
    dueDate: dueDateOptions(),
    labels: labels.map(label => ({ id: label.id, label: label.name, color: label.color, description: label.description, issueCount: label.issueCount, scope: label.scope, groupId: label.groupId, groupLabel: label.groupId ? groups.get(label.groupId) : undefined, kind: 'labels' as const })),
    project: [{ id: '', label: 'No project', kind: 'project' }, ...data.projects.map(project => ({ id: project.id, label: project.name, color: project.color, kind: 'project' as const }))],
  }
}

function toRow(issue: Issue, data: BootstrapData): MyIssuesRowData {
  const sla = data.issueSlas.find(item => item.issueId === issue.id && item.status !== 'removed')
  const rule = sla ? data.slaRules.find(item => item.id === sla.ruleId) : undefined
  return { id: issue.id, identifier: issue.identifier, title: issue.title, href: issuePath(data.workspace.urlKey, issue), priority: clampPriority(issue.priority), state: issue.state, labels: issue.labels, project: issue.project, assignee: issue.assignee ? { id: issue.assignee.id, name: issue.assignee.displayName, avatarUrl: issue.assignee.avatarUrl } : undefined, estimate: issue.estimate, dueDate: issue.dueDate, sla: sla ? { ...sla, ruleName: rule?.name } : undefined, createdAt: issue.createdAt, updatedAt: issue.updatedAt, parentId: issue.parentId, sortOrder: issue.sortOrder }
}

function groupIssues(issues: Issue[], display: MyIssuesDisplayOptions, states: Issue['state'][], data: BootstrapData): MyIssuesGroupData[] {
  const grouping = display.grouping === 'focus' ? 'status' : display.grouping
  if (grouping === 'none') return [{ id: 'all', label: 'All issues', issues: issues.map(issue => toRow(issue, data)) }]
  const groups = new Map<string, MyIssuesGroupData>()
  for (const issue of issues) {
    const value = groupFor(issue, grouping, data)
    const current = groups.get(value.id) ?? { ...value, issues: [] }
    current.issues.push(toRow(issue, data)); groups.set(value.id, current)
  }
  if (grouping === 'status' && display.showEmptyGroups) for (const state of states) if (!groups.has(state.id)) groups.set(state.id, { id: state.id, label: state.name, stateType: state.type, issues: [] })
  let result = [...groups.values()]
  if (grouping === 'status') { const order = new Map(states.map((state,index) => [state.id,index])); result.sort((a,b) => (order.get(a.id) ?? 99) - (order.get(b.id) ?? 99)) }
  else result.sort((a,b) => a.label.localeCompare(b.label))
  return display.groupOrder === 'desc' ? result.reverse() : result
}

function groupFor(issue: Issue, grouping: MyIssuesGrouping, data: BootstrapData): Omit<MyIssuesGroupData,'issues'> {
  if (grouping === 'status') return { id: issue.state.id, label: issue.state.name, stateType: issue.state.type }
  if (grouping === 'priority') return { id: `priority-${issue.priority}`, label: ['No priority','Urgent','High','Medium','Low'][issue.priority] ?? 'No priority' }
  if (grouping === 'project') return { id: issue.project?.id ?? 'no-project', label: issue.project?.name ?? 'No project' }
  if (grouping === 'assignee') return { id: issue.assignee?.id ?? 'unassigned', label: issue.assignee?.displayName ?? 'No assignee' }
  if (grouping === 'label') { const label=issue.labels[0]; return { id: label?.id ?? 'no-label', label: label?.name ?? 'No label' } }
  if (grouping === 'team') return { id: issue.team.id, label: issue.team.name }
  if (grouping === 'cycle') { const cycle=data.cycles.find(item => item.id === issue.cycleId); return { id: cycle?.id ?? 'no-cycle', label: cycle?.name ?? 'No cycle' } }
  return { id: 'all', label: 'All issues' }
}

function sortIssues(issues: Issue[], display: MyIssuesDisplayOptions) { return [...issues].sort((a,b) => { if(display.orderCompletedByRecency&&['completed','canceled'].includes(a.state.type)&&['completed','canceled'].includes(b.state.type)){const aDate=a.completedAt??a.canceledAt??a.updatedAt,bDate=b.completedAt??b.canceledAt??b.updatedAt;return +new Date(bDate)-+new Date(aDate)}if (display.ordering === 'priority' || display.ordering === 'importance') return a.priority-b.priority || a.sortOrder-b.sortOrder; if (display.ordering === 'created') return +new Date(b.createdAt)-+new Date(a.createdAt); return +new Date(b.updatedAt)-+new Date(a.updatedAt) }) }
function matchesFilters(issue: Issue, filters: MyIssuesAppliedFilter[]) { return filters.every(filter => { const values=filterValues(filter).map(value=>value.value); let match=true; if(filter.field==='status')match=values.includes(issue.state.id);else if(filter.field==='priority')match=values.includes(String(issue.priority));else if(filter.field==='assignee')match=values.includes(issue.assignee?.id??'');else if(filter.field==='labels')match=issue.labels.some(label=>values.includes(label.id));else if(filter.field==='project')match=values.includes(issue.project?.id??'');return filter.operator==='is'?match:!match }) }
function includeCompleted(issue: Issue, window: MyIssuesDisplayOptions['completedWindow'], data: BootstrapData) { if(!['completed','canceled'].includes(issue.state.type))return true;if(window==='all')return true;if(window==='none')return false;if(window==='currentCycle')return data.cycles.some(cycle=>cycle.status==='current'&&cycle.id===issue.cycleId);const days=window==='pastDay'?1:window==='pastWeek'?7:30;return Date.now()-new Date(issue.completedAt??issue.canceledAt??issue.updatedAt).getTime()<=days*86_400_000 }
function updateForProperty(property: MyIssuesEditableProperty, value: string|string[]): IssueUpdateInput|undefined { if(property==='labels'&&Array.isArray(value))return{labelIds:value};if(Array.isArray(value))return;if(property==='status')return{stateId:value};if(property==='priority')return{priority:Number(value)};if(property==='assignee')return{assigneeId:value};if(property==='project')return{projectId:value};if(property==='dueDate')return{dueDate:value} }
function dueDateOptions(){const today=new Date(),day=86_400_000,iso=(date:Date)=>date.toISOString().slice(0,10);return[{id:'',label:'No due date',kind:'dueDate' as const},{id:iso(today),label:'Today',kind:'dueDate' as const},{id:iso(new Date(today.getTime()+day)),label:'Tomorrow',kind:'dueDate' as const},{id:iso(new Date(today.getTime()+day*7)),label:'In one week',kind:'dueDate' as const}]}
function clampPriority(value:number):0|1|2|3|4{return Math.max(0,Math.min(4,value)) as 0|1|2|3|4}

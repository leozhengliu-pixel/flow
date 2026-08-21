import { useMemo, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { Diamond, Filter, Plus, Trash2, X } from 'lucide-react'
import { toast } from 'sonner'
import { MyIssuesDisplayMenu } from '@/components/my-issues/my-issues-display-menu'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import { MyIssuesFilterBar } from '@/components/my-issues/my-issues-filter-bar'
import { filterValues, toggleFilterOption, updateFilterOperator, updateFilterValues, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import { MyIssuesList, type MyIssuesContextAction, type MyIssuesEditableProperty, type MyIssuesGroupData, type MyIssuesRowData, type MyIssuesRowPropertyOptions } from '@/components/my-issues/my-issues-list'
import type { MyIssuesDisplayOptions, MyIssuesFilterKey, MyIssuesFilterOption, MyIssuesGrouping, MyIssuesProperty } from '@/components/my-issues/my-issues-surface'
import { IssueBoard } from '@/components/issue-explorer/issue-board'
import { ViewIconPicker } from '@/components/views/view-icon-picker'
import type { Issue, ProjectMilestone } from '@/types/flow'
import type { ProjectDetailProps } from './project-detail-types'
import { PRIORITY_LABELS } from './project-detail-types'

export type ProjectIssueFilters = MyIssuesAppliedFilter[]
export type ProjectIssueProperty = MyIssuesProperty

export function ProjectIssueFilterMenu({ filters, issues, onChange }: { filters: ProjectIssueFilters; issues: Issue[]; onChange: (filters: ProjectIssueFilters) => void }) {
  const [open, setOpen] = useState(false)
  const options = useMemo(() => projectFilterOptions(issues), [issues])
  const toggle = (field: MyIssuesFilterKey, option: MyIssuesFilterOption) => {
    const label = FILTER_LABELS[field]
    if (label) onChange(toggleFilterOption(filters, field, label, option))
  }
  return <MyIssuesFilterMenu availableFields={['status','assignee','priority','labels']} filters={filters} onOpenChange={setOpen} onToggle={toggle} open={open} options={field => options[field]} trigger={<button aria-label="Add filter" className="project-detail-page__toolbar-button" data-active={filters.length > 0} type="button"><Filter size={14}/>{filters.length > 0 && <i>{filters.length}</i>}</button>}/>
}

export function ProjectIssueDisplayMenu({ display, onChange }: { display: MyIssuesDisplayOptions; onChange: (display: MyIssuesDisplayOptions) => void }) {
  const [open, setOpen] = useState(false)
  return <MyIssuesDisplayMenu hiddenProperties={['project']} onChange={onChange} onOpenChange={setOpen} open={open} options={display}/>
}

export function ProjectIssueFilterBar({ filters, issues, onChange }: { filters: ProjectIssueFilters; issues: Issue[]; onChange: (filters: ProjectIssueFilters) => void }) {
  const options = useMemo(() => projectFilterOptions(issues), [issues])
  return <MyIssuesFilterBar filters={filters} filterOptions={filter => options[filter.field]} onClear={() => onChange([])} onOperatorChange={(id, operator) => onChange(updateFilterOperator(filters, id, operator))} onRemove={id => onChange(filters.filter(filter => filter.id !== id))} onValuesChange={(id, values) => onChange(updateFilterValues(filters, id, values))}/>
}

export function ProjectNewView({ display, filters, onCreateSavedView, onDisplayChange, onFiltersChange, onTabChange, project, projectIssues, ...props }: ProjectDetailProps & { projectIssues: Issue[]; filters: ProjectIssueFilters; display: MyIssuesDisplayOptions; onFiltersChange: (filters: ProjectIssueFilters) => void; onDisplayChange: (display: MyIssuesDisplayOptions) => void }) {
  const [name, setName] = useState('')
  const [saving, setSaving] = useState(false)
  const [visual, setVisual] = useState({ icon: 'CustomView', color: '#8a8f98' })
  const save = async () => {
    if (saving) return
    setSaving(true)
    try {
      const projectValue = { value: project.id, valueLabel: project.name, color: project.color }
      await onCreateSavedView({ name: name.trim() || 'All issues', description: '', resource: 'issues', scope: 'workspace', view: 'all', ...visual, filters: [{ id: `project-${project.id}`, field: 'project', fieldLabel: 'Project', operator: 'is', ...projectValue, values: [projectValue] }, ...filters.map(filterSnapshot)], display: displaySnapshot(display) })
      toast.success('Project view saved'); onTabChange('issues')
    } catch (error) { toast.error('Could not save project view', { description: error instanceof Error ? error.message : undefined }) }
    finally { setSaving(false) }
  }
  return <div className="project-new-view">
    <div className="project-new-view__name"><ViewIconPicker color={visual.color} icon={visual.icon} onChange={setVisual} triggerClassName="project-new-view__icon"/><input autoFocus aria-label="View name" onChange={event => setName(event.target.value)} onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') void save() }} placeholder="All issues" value={name}/><button onClick={() => onTabChange('issues')} type="button">Cancel</button><button className="is-save" disabled={saving} onClick={() => void save()} type="button">{saving ? 'Saving…' : 'Save'}</button></div>
    <div className="project-new-view__tools"><ProjectIssueFilterMenu filters={filters} issues={projectIssues} onChange={onFiltersChange}/><ProjectIssueDisplayMenu display={display} onChange={onDisplayChange}/></div>
    <ProjectIssues {...props} project={project} projectIssues={projectIssues} filters={filters} display={display} onCreateSavedView={onCreateSavedView} onFiltersChange={onFiltersChange} onTabChange={onTabChange}/>
  </div>
}

export function ProjectIssues({ display, filters, issues, labels, labelGroups, milestoneScope, onClearMilestoneScope, onCreateIssue, onDeleteIssues, onFiltersChange, onOpenIssue, onUpdateIssue, project, projectIssues, users }: ProjectDetailProps & { projectIssues: Issue[]; filters: ProjectIssueFilters; display: MyIssuesDisplayOptions; milestoneScope?: ProjectMilestone; onClearMilestoneScope?: () => void; onFiltersChange: (filters: ProjectIssueFilters) => void }) {
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [deleteTarget, setDeleteTarget] = useState<Issue>()
  const visible = useMemo(() => sortIssues(projectIssues.filter(issue => matchesFilters(issue, filters)).filter(issue => display.showSubIssues || !issue.parentId).filter(issue => display.completedWindow !== 'none' || !['completed','canceled'].includes(issue.state.type)), display), [display, filters, projectIssues])
  const allStates = useMemo(() => uniqueById(issues.map(issue => issue.state)).sort((left, right) => left.position - right.position), [issues])
  const groups = useMemo(() => groupIssues(visible, display, allStates), [allStates, display, visible])
  const rowIssues = useMemo(() => new Map(projectIssues.map(issue => [issue.id, issue])), [projectIssues])
  const labelGroupNames = useMemo(() => new Map(labelGroups.filter(group => group.resourceType === 'issue').map(group => [group.id, group.name])), [labelGroups])
  const propertyOptions = useMemo<MyIssuesRowPropertyOptions>(() => ({
    status: allStates.map(state => ({ id: state.id, label: state.name, kind: 'status', stateType: state.type, color: state.color })),
    priority: [0,1,2,3,4].map(priority => ({ id: String(priority), label: PRIORITY_LABELS[priority], kind: 'priority' as const, priority: priority as 0|1|2|3|4 })),
    assignee: [{ id: '', label: 'No assignee', kind: 'assignee' as const }, ...users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, kind: 'assignee' as const }))],
    dueDate: dueDateOptions(), labels: labels.map(label => ({ id: label.id, label: label.name, kind: 'labels' as const, color: label.color, description: label.description, issueCount: label.issueCount, scope: label.scope, groupId: label.groupId, groupLabel: label.groupId ? labelGroupNames.get(label.groupId) : undefined })),
    project: [{ id: project.id, label: project.name, kind: 'project' as const, color: project.color }],
  }), [allStates, labelGroupNames, labels, project.color, project.id, project.name, users])
  const changeProperty = async (row: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => {
    if (property === 'priority') await onUpdateIssue(row.id, { priority: Number(value) })
    else if (property === 'status') await onUpdateIssue(row.id, { stateId: String(value) })
    else if (property === 'assignee') await onUpdateIssue(row.id, { assigneeId: String(value) })
    else if (property === 'labels') await onUpdateIssue(row.id, { labelIds: value as string[] })
    else if (property === 'dueDate') await onUpdateIssue(row.id, { dueDate: String(value) })
  }
  const contextAction = (row: MyIssuesRowData, action: MyIssuesContextAction) => { const issue = rowIssues.get(row.id); if (!issue) return; if (action === 'delete') setDeleteTarget(issue); else if (action === 'copy') void navigator.clipboard.writeText(`${location.origin}/${location.pathname.split('/')[1]}/issue/${issue.identifier}`); else if (action === 'openIn') onOpenIssue(issue) }
  const select = (issueId: string, isSelected: boolean) => setSelected(current => { const next = new Set(current); if (isSelected) next.add(issueId); else next.delete(issueId); return next })
  const move = (row: MyIssuesRowData, sourceGroupId: string, targetGroupId: string, targetIndex: number) => {
    const target = groups.find(group => group.id === targetGroupId)
    const before = target?.issues[targetIndex]
    const after = target?.issues[targetIndex - 1]
    const sortOrder = before && after ? ((before.sortOrder ?? 0) + (after.sortOrder ?? 0)) / 2 : before ? (before.sortOrder ?? 1) - 1 : (after?.sortOrder ?? 0) + 1
    const stateId = display.grouping === 'status' && sourceGroupId !== targetGroupId && allStates.some(state => state.id === targetGroupId) ? targetGroupId : undefined
    void onUpdateIssue(row.id, { sortOrder, ...(stateId ? { stateId } : {}) })
  }

  return <div className="project-issues" data-layout={display.layout}>
    {milestoneScope && <div className="project-issues__milestone-scope"><Diamond size={13}/><span data-i18n-ignore>{milestoneScope.name}</span><button aria-label="Clear milestone filter" onClick={onClearMilestoneScope} type="button"><X size={12}/></button></div>}
    <ProjectIssueFilterBar filters={filters} issues={projectIssues} onChange={onFiltersChange}/>
    {groups.length > 0 && (display.layout === 'list' ? <MyIssuesList collapsedGroupIds={collapsed} displayProperties={display.properties} groups={groups} nestedSubIssues={display.nestedSubIssues} propertyOptions={propertyOptions} selectedIds={selected} onContextAction={contextAction} onCreateIssue={() => onCreateIssue(project.id, milestoneScope?.id)} onGroupCollapsedChange={(groupId, isCollapsed) => setCollapsed(current => { const next = new Set(current); if (isCollapsed) next.add(groupId); else next.delete(groupId); return next })} onOpenIssue={row => { const issue = rowIssues.get(row.id); if (issue) onOpenIssue(issue) }} onPropertyChange={changeProperty} onSelectIssue={select}/> : <IssueBoard groups={groups} onCreateIssue={() => onCreateIssue(project.id, milestoneScope?.id)} onMove={move} onOpenIssue={row => { const issue = rowIssues.get(row.id); if (issue) onOpenIssue(issue) }} onSelectIssue={select} properties={display.properties} selectedIds={selected}/>) }
    {!groups.length && <div className="project-issues__empty"><strong>No matching issues</strong><span>Change the filters or create a new issue.</span><button onClick={() => onCreateIssue(project.id, milestoneScope?.id)} type="button"><Plus size={13}/>Create issue</button></div>}
    {selected.size > 0 && <div className="project-issues__bulk"><span>{selected.size} selected</span><button onClick={() => setSelected(new Set())} type="button">Clear</button><button className="is-danger" onClick={() => void onDeleteIssues([...selected]).then(() => setSelected(new Set()))} type="button"><Trash2 size={13}/>Delete</button></div>}
    <Dialog.Root onOpenChange={open => { if (!open) setDeleteTarget(undefined) }} open={Boolean(deleteTarget)}><Dialog.Portal><Dialog.Overlay className="project-detail-page__dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="project-detail-page__delete-dialog"><Dialog.Title>Delete {deleteTarget?.identifier}?</Dialog.Title><p>This issue will be permanently deleted.</p><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button autoFocus className="is-danger" onClick={() => deleteTarget && void onDeleteIssues([deleteTarget.id]).then(() => setDeleteTarget(undefined))} type="button">Delete</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
  </div>
}

const FILTER_LABELS: Partial<Record<MyIssuesFilterKey, string>> = { status: 'Status', assignee: 'Assignee', priority: 'Priority', labels: 'Labels', dates: 'Due date' }
function projectFilterOptions(issues: Issue[]): Partial<Record<MyIssuesFilterKey, MyIssuesFilterOption[]>> {
  const count = (predicate: (issue: Issue) => boolean) => issues.filter(predicate).length
  const statuses = uniqueById(issues.map(issue => issue.state)).map(state => ({ id: state.id, label: state.name, color: state.color, count: count(issue => issue.state.id === state.id) }))
  const assignees = uniqueById(issues.flatMap(issue => issue.assignee ? [issue.assignee] : [])).map(user => ({ id: user.id, label: user.displayName, count: count(issue => issue.assignee?.id === user.id) }))
  const labels = uniqueById(issues.flatMap(issue => issue.labels)).map(label => ({ id: label.id, label: label.name, color: label.color, count: count(issue => issue.labels.some(item => item.id === label.id)) }))
  return { status: statuses, priority: [0,1,2,3,4].map(priority => ({ id: String(priority), label: PRIORITY_LABELS[priority], count: count(issue => issue.priority === priority) })), assignee: [{ id: '', label: 'No assignee', count: count(issue => !issue.assignee) }, ...assignees], labels }
}
function matchesFilters(issue: Issue, filters: ProjectIssueFilters) { return filters.every(filter => { const values = filterValues(filter).map(value => value.value); let match = true; if (filter.field === 'priority') match = values.includes(String(issue.priority)); else if (filter.field === 'status') match = values.includes(issue.state.id); else if (filter.field === 'assignee') match = values.includes(issue.assignee?.id ?? ''); else if (filter.field === 'labels') match = issue.labels.some(label => values.includes(label.id)); return filter.operator === 'is' ? match : !match }) }
function groupIssues(issues: Issue[], display: MyIssuesDisplayOptions, allStates: Issue['state'][]): MyIssuesGroupData[] {
  const grouping = display.grouping === 'focus' ? 'status' : display.grouping
  if (grouping === 'none') return [{ id: 'all', label: 'All issues', issues: issues.map(toRowData) }]
  const groups = new Map<string, MyIssuesGroupData>()
  for (const issue of issues) { const group = groupFor(issue, grouping); const current = groups.get(group.id) ?? { ...group, issues: [] }; current.issues.push(toRowData(issue)); groups.set(group.id, current) }
  if (grouping === 'status' && display.showEmptyGroups) for (const state of allStates) if (!groups.has(state.id)) groups.set(state.id, { id: state.id, label: state.name, stateType: state.type, issues: [] })
  let result = [...groups.values()]
  if (grouping === 'status') { const order = new Map(allStates.map((state, index) => [state.id, index])); result.sort((left, right) => (order.get(left.id) ?? 99) - (order.get(right.id) ?? 99)) } else result.sort((left, right) => left.label.localeCompare(right.label))
  return display.groupOrder === 'desc' ? result.reverse() : result
}
function groupFor(issue: Issue, grouping: MyIssuesGrouping): Omit<MyIssuesGroupData, 'issues'> { if (grouping === 'status') return { id: issue.state.id, label: issue.state.name, stateType: issue.state.type }; if (grouping === 'priority') return { id: `priority-${issue.priority}`, label: PRIORITY_LABELS[issue.priority] }; if (grouping === 'assignee') return { id: issue.assignee?.id ?? 'unassigned', label: issue.assignee?.displayName ?? 'No assignee' }; if (grouping === 'label') { const label = issue.labels[0]; return { id: label?.id ?? 'no-label', label: label?.name ?? 'No label' } } return { id: 'project', label: issue.project?.name ?? 'Project' } }
function sortIssues(issues: Issue[], display: MyIssuesDisplayOptions) { return [...issues].sort((left, right) => { if (display.ordering === 'priority' || display.ordering === 'importance') return left.priority - right.priority || left.sortOrder - right.sortOrder; if (display.ordering === 'created') return +new Date(right.createdAt) - +new Date(left.createdAt); return +new Date(right.updatedAt) - +new Date(left.updatedAt) }) }
function displaySnapshot(display: MyIssuesDisplayOptions) { return { ...display, properties: [...display.properties] } }
function filterSnapshot(filter: MyIssuesAppliedFilter): MyIssuesAppliedFilter { const values = filterValues(filter); return { ...filter, ...values[0], values } }
function dueDateOptions() { const today = new Date(); const iso = (date: Date) => date.toISOString().slice(0,10); return [{ id: '', label: 'No due date', kind: 'dueDate' as const }, { id: iso(today), label: 'Today', kind: 'dueDate' as const }, { id: iso(new Date(today.getTime() + 86_400_000)), label: 'Tomorrow', kind: 'dueDate' as const }] }
function uniqueById<T extends { id: string }>(items: T[]) { return [...new Map(items.map(item => [item.id, item])).values()] }
function toRowData(issue: Issue): MyIssuesRowData { return { id: issue.id, identifier: issue.identifier, title: issue.title, href: `/${location.pathname.split('/')[1]}/issue/${issue.identifier}`, priority: issue.priority as 0|1|2|3|4, state: issue.state, labels: issue.labels, project: issue.project, assignee: issue.assignee ? { id: issue.assignee.id, name: issue.assignee.displayName, avatarUrl: issue.assignee.avatarUrl } : undefined, estimate: issue.estimate, dueDate: issue.dueDate, createdAt: issue.createdAt, updatedAt: issue.updatedAt, parentId: issue.parentId, sortOrder: issue.sortOrder } }

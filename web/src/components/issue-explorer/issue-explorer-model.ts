import { issuePath } from '@/lib/app-routes'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import type { MyIssuesBulkAction, MyIssuesBulkActionOption } from '@/components/my-issues/my-issues-bulk-action-bar'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-bar'
import type { MyIssuesContextAction, MyIssuesContextOption, MyIssuesEditableProperty, MyIssuesGroupData, MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import type { MyIssuesDisplayOptions, MyIssuesGrouping } from '@/components/my-issues/my-issues-surface'
import type { TeamIssuesRouteView } from '@/lib/app-routes'
import { filterValues } from '@/components/my-issues/my-issues-filter-types'
import { labelsForResource } from '@/lib/labels'

export const ISSUE_FILTER_LABELS: Partial<Record<MyIssuesFilterKey, string>> = {
  ai:'AI filter',advanced:'Advanced filter',status:'Status',assignee:'Assignee',agent:'Agent',agentSession:'Agent Session',creator:'Creator',priority:'Priority',labels:'Labels',relations:'Relations',suggestedLabel:'Suggested label',dates:'Dates',project:'Project',projectProperties:'Project properties',initiative:'Initiative',cycle:'Cycle',addedToCycle:'Added to cycle',releases:'Releases',subscribers:'Subscribers',externalSource:'External source',autoClosed:'Auto-closed',content:'Content',links:'Links',template:'Template',
}

const PRIORITIES: MyIssuesContextOption[] = ['No priority', 'Urgent', 'High', 'Medium', 'Low'].map((label, id) => ({
  id: String(id), label, kind: 'priority', priority: id as 0 | 1 | 2 | 3 | 4,
}))

export function issueToExplorerRow(issue: Issue, workspaceSlug: string, issues: Issue[] = [], data?: BootstrapData): MyIssuesRowData {
  const fullProject = data?.projects.find(project => project.id === issue.project?.id)
  const issueReleases=data?.releases?.filter(release=>release.issueIds?.includes(issue.id))??[]
  return {
    id: issue.id,
    identifier: issue.identifier,
    title: issue.title,
    description: issue.description,
    href: issuePath(workspaceSlug, issue),
    priority: clampPriority(issue.priority),
    state: issue.state,
    labels: issue.labels??[],
    project: issue.project,
    assignee: issue.assignee ? { id: issue.assignee.id, name: issue.assignee.displayName, avatarUrl: issue.assignee.avatarUrl } : undefined,
    delegate: issue.delegate ? { id: issue.delegate.id, name: issue.delegate.displayName, avatarUrl: issue.delegate.avatarUrl } : undefined,
    creatorId: issue.creator.id,
    creatorName: issue.creator.displayName,
    isAssignedToViewer:issue.assignee?.id===data?.viewer.id,
    cycleId: issue.cycleId,
    addedToCycle:issue.addedToCycle,
    agentSessionId:issue.agentSessionId,
    suggestedLabelIds:issue.suggestedLabelIds??[],
    externalSource:issue.externalSource,
    autoClosed:issue.autoClosed,
    autoClosedAt:issue.autoClosedAt,
    triagedAt:issue.triagedAt,
    templateId:issue.templateId,
    initiativeIds:fullProject?.initiatives??[],
    projectStatusId:fullProject?.status?.id,
    projectStatusType:fullProject?.status?.type,
    projectPriority:fullProject?.priority,
    projectLabelIds:fullProject?.labelIds??[],
    projectLeadId:fullProject?.lead?.id,
    projectMilestoneNames:fullProject?.milestones?.map(milestone=>milestone.name)??[],
    releaseIds:issueReleases.map(release=>release.id),releasePipelineIds:issueReleases.map(release=>release.pipelineId).filter((id):id is string=>Boolean(id)),releaseStages:issueReleases.map(release=>release.stage).filter((stage):stage is string=>Boolean(stage)),releaseStatuses:issueReleases.map(release=>release.status),hasReleasedRelease:issueReleases.some(release=>Boolean(release.releasedAt)),
    subscriberIds: issue.subscriberIds??[],
    relationTypes: issue.relations?.map(relation => relation.type)??[],
    hasLinks: (issue.attachments?.length??0) > 0,
    hasContent: Boolean(issue.title?.trim() || issue.description?.trim()),
    estimate: issue.estimate,
    dueDate: issue.dueDate,
    createdAt: issue.createdAt,
    updatedAt: issue.updatedAt,
    completedAt: issue.completedAt,
    startedAt: issue.startedAt,
    statusChangedAt: issue.statusChangedAt,
    statusIntervals: issueStatusIntervals(issue, data),
    canceledAt: issue.canceledAt,
    archivedAt: issue.archivedAt,
    parentId: issue.parentId,
    ...issueHierarchyFields(issue, issues),
    sortOrder: issue.sortOrder,
  }
}

function issueStatusIntervals(issue: Issue, data?: BootstrapData): NonNullable<MyIssuesRowData['statusIntervals']> {
  const events = (data?.activities?.[issue.id]??[]).filter(event=>event.metadata?.stateId||event.metadata?.state).sort((a,b)=>Date.parse(a.createdAt)-Date.parse(b.createdAt))
  const stateId = (id?:string,name?:string) => id || data?.states.find(state=>state.name===name)?.id || ''
  const stateType = (id:string) => data?.states.find(state=>state.id===id)?.type
  if (!events.length) return [{stateId:issue.state.id,stateType:issue.state.type,enteredAt:issue.statusChangedAt??issue.startedAt??issue.createdAt}]
  const intervals:NonNullable<MyIssuesRowData['statusIntervals']>=[]
  const first=events[0],previous=stateId(first.metadata.stateBeforeId,first.metadata.stateBefore)
  if(previous)intervals.push({stateId:previous,stateType:stateType(previous),enteredAt:issue.createdAt,exitedAt:first.createdAt})
  for(let index=0;index<events.length;index++){
    const event=events[index],id=stateId(event.metadata.stateId,event.metadata.state)
    if(id)intervals.push({stateId:id,stateType:stateType(id),enteredAt:event.createdAt,exitedAt:events[index+1]?.createdAt})
  }
  const last=intervals.at(-1)
  if(!last||last.stateId!==issue.state.id)intervals.push({stateId:issue.state.id,stateType:issue.state.type,enteredAt:issue.statusChangedAt??events.at(-1)?.createdAt??issue.createdAt})
  return intervals
}

export function issueHierarchyFields(issue: Issue, issues: Issue[]): Pick<MyIssuesRowData,'parent'|'ancestors'|'subIssueProgress'|'subIssues'> {
  const byId = new Map(issues.map(item => [item.id,item]))
  const ancestors: NonNullable<MyIssuesRowData['ancestors']> = []
  const seen = new Set<string>([issue.id])
  let parentId = issue.parentId
  while (parentId && !seen.has(parentId)) {
    const parent = byId.get(parentId)
    if (!parent) break
    seen.add(parentId)
    ancestors.push({id:parent.id,identifier:parent.identifier,title:parent.title})
    parentId = parent.parentId
  }
  const children = (issue.subIssueIds??[]).map(id=>byId.get(id)).filter((item):item is Issue=>Boolean(item)&&!item!.archivedAt)
  return {
    parent: ancestors[0],
    ancestors,
    subIssueProgress: children.length ? {completed:children.filter(child=>child.state.type==='completed'||child.state.type==='canceled').length,total:children.length} : undefined,
    subIssues:children.map(child=>({id:child.id,identifier:child.identifier,title:child.title,priority:clampPriority(child.priority),state:child.state,labels:child.labels,project:child.project,assignee:child.assignee?{id:child.assignee.id,name:child.assignee.displayName,avatarUrl:child.assignee.avatarUrl}:undefined,createdAt:child.createdAt,updatedAt:child.updatedAt,parentId:child.parentId})),
  }
}

export function explorerPropertyOptions(data: BootstrapData, issues = data.issues) {
  const count = (predicate: (issue: Issue) => boolean) => issues.filter(predicate).length
  const issueLabels = labelsForResource(data.labels, 'issue')
  const labelGroupNames = new Map(data.labelGroups.filter(group => group.resourceType === 'issue').map(group => [group.id, group.name]))
  return {
    status: [...data.states].sort((a, b) => a.position - b.position).map(state => ({ id: state.id, label: state.name, color: state.color, count: count(issue => issue.state.id === state.id), kind: 'status' as const, stateType: state.type })),
    priority: PRIORITIES.map(priority => ({ ...priority, count: count(issue => String(issue.priority) === priority.id) })),
    assignee: [{ id: '', label: 'No assignee', count: count(issue => !issue.assignee), kind: 'assignee' as const }, ...data.users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, count: count(issue => issue.assignee?.id === user.id), kind: 'assignee' as const }))],
    creator: data.users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, count: count(issue => issue.creator.id === user.id), kind: 'creator' as const })),
    agent: [{ id: '', label: 'No agent', count: count(issue => !issue.delegate) }, { id: '*', label: 'Any agent', count: count(issue => Boolean(issue.delegate)) }, ...data.users.filter(user => user.active && issues.some(issue => issue.delegate?.id === user.id)).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, count: count(issue => issue.delegate?.id === user.id) }))],
    agentSession:[{id:'',label:'No agent session',count:count(issue=>!issue.agentSessionId)},{id:'*',label:'Any agent session',count:count(issue=>Boolean(issue.agentSessionId))}],
    dueDate: explorerDueDateOptions().map(option => ({ ...option, kind: 'dueDate' as const })),
    dates: dateFilterCategories(issues),
    labels: issueLabels.map(label => ({ id: label.id, label: label.name, color: label.color, description: label.description, issueCount: label.issueCount, scope: label.scope, groupId: label.groupId, groupLabel: label.groupId ? labelGroupNames.get(label.groupId) : undefined, count: count(issue => issue.labels?.some(item => item.id === label.id)??false), kind: 'labels' as const })),
    project: [{ id: '', label: 'No project', count: count(issue => !issue.project), kind: 'project' as const }, ...data.projects.map(project => ({ id: project.id, label: project.name, color: project.color, count: count(issue => issue.project?.id === project.id), kind: 'project' as const }))],
    projectProperties:projectPropertyFilterOptions(data,issues),
    initiative:[{id:'',label:'No initiative',count:count(issue=>{const project=data.projects.find(project=>project.id===issue.project?.id);return !project?.initiatives?.length})},...data.initiatives.map(initiative=>({id:initiative.id,label:initiative.name,count:count(issue=>data.projects.find(project=>project.id===issue.project?.id)?.initiatives?.includes(initiative.id)??false)}))],
    cycle: [{ id: '', label: 'No cycle', count: count(issue => !issue.cycleId) }, ...data.cycles.map(cycle => ({ id: cycle.id, label: cycle.name, count: count(issue => issue.cycleId === cycle.id) }))],
    addedToCycle:[{id:'planned',label:'Planned',count:count(issue=>issue.addedToCycle==='planned')},{id:'during',label:'During cycle',count:count(issue=>issue.addedToCycle==='during')},{id:'after',label:'After cycle',count:count(issue=>issue.addedToCycle==='after')}],
    releases: releaseFilterCategories(data,issues),
    subscribers: [{ id: '', label: 'No subscribers', count: count(issue => !issue.subscriberIds?.length), kind: 'subscribers' as const }, ...data.users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, count: count(issue => issue.subscriberIds?.includes(user.id)??false), kind: 'subscribers' as const }))],
    externalSource:[{id:'',label:'No external source',count:count(issue=>!issue.externalSource)},...uniqueStrings(issues.map(issue=>issue.externalSource)).map(source=>({id:source,label:source,count:count(issue=>issue.externalSource===source)}))],
    autoClosed:[{id:'true',label:'Auto-closed',count:count(issue=>Boolean(issue.autoClosed))},{id:'false',label:'Not auto-closed',count:count(issue=>!issue.autoClosed)}],
    template:[{id:'',label:'No template',count:count(issue=>!issue.templateId)},...data.issueTemplates.map(template=>({id:template.id,label:template.name,count:count(issue=>issue.templateId===template.id)}))],
    suggestedLabel:[{id:'',label:'No suggested label',count:count(issue=>!issue.suggestedLabelIds?.length)},...issueLabels.map(label=>({id:label.id,label:label.name,color:label.color,count:count(issue=>issue.suggestedLabelIds?.includes(label.id)??false)}))],
    relations: relationFilterOptions(issues),
    links: [{ id: 'has-links', label: 'Has links', count: count(issue => Boolean(issue.attachments?.length)), kind: 'links' as const }, { id: 'no-links', label: 'No links', count: count(issue => !issue.attachments?.length), kind: 'links' as const }],
    content: [{ id: 'content-prompt', label: 'Filter by content…' }],
  }
}

export type ExplorerPropertyOptions = ReturnType<typeof explorerPropertyOptions>

export function explorerFilterOptions(field: MyIssuesFilterKey, options: ExplorerPropertyOptions): MyIssuesFilterOption[] | undefined {
  if (field === 'ai') return [{id:'assigned-to-me',label:'assigned to me'},{id:'completed-last-month',label:'completed in the last month'},{id:'due-next-two-weeks',label:'due in the next 2 weeks'}]
  if (field === 'advanced') return [{id:'new-group',label:'Add filter group',children:[
    {id:'advanced-status',label:'Status',children:options.status.map(option=>({...option,id:`status:${option.id}`}))},
    {id:'advanced-assignee',label:'Assignee',children:options.assignee.map(option=>({...option,id:`assignee:${option.id}`}))},
    {id:'advanced-priority',label:'Priority',children:options.priority.map(option=>({...option,id:`priority:${option.id}`}))},
    {id:'advanced-labels',label:'Labels',children:options.labels.map(option=>({...option,id:`labels:${option.id}`}))},
    {id:'advanced-project',label:'Project',children:options.project.map(option=>({...option,id:`project:${option.id}`}))},
  ]}]
  if (field === 'status'||field==='assignee'||field==='agent'||field==='agentSession'||field==='creator'||field==='priority'||field==='labels'||field==='relations'||field==='suggestedLabel'||field==='dates'||field==='project'||field==='projectProperties'||field==='initiative'||field==='cycle'||field==='addedToCycle'||field==='releases'||field==='subscribers'||field==='externalSource'||field==='autoClosed'||field==='content'||field==='links'||field==='template') return options[field]
}

export function explorerBulkOptions(action: MyIssuesBulkAction, options: ExplorerPropertyOptions): MyIssuesBulkActionOption[] | undefined {
  if (action === 'status') return options.status
  if (action === 'priority') return options.priority
  if (action === 'assign') return options.assignee
  if (action === 'project') return options.project
  if (action === 'labels') return options.labels
  if (action === 'dueDate') return explorerDueDateOptions()
  if (action === 'subscribers') return options.assignee.filter(option => option.id)
}

export async function executeExplorerBulkAction({ action, ids, value, data, issuesById, onUpdateIssue, onUpdateIssues }: {
  action: MyIssuesBulkAction
  ids: string[]
  value?: string
  data: BootstrapData
  issuesById: Map<string, Issue>
  onUpdateIssue: (id: string, input: IssueUpdateInput) => Promise<Issue>
  onUpdateIssues: (ids: string[], input: IssueUpdateInput) => Promise<Issue[]>
}): Promise<Issue[] | void> {
  if (action.startsWith('copy')) { await copyIssues(action, ids, issuesById, data.workspace.urlKey); return }
  if (action === 'labels' && value != null) return Promise.all(ids.map(id => { const issue = issuesById.get(id)!; return onUpdateIssue(id, { labelIds: issue.labels.some(label => label.id === value) ? issue.labels.map(label => label.id) : [...issue.labels.map(label => label.id), value] }) }))
  if (action === 'subscribers' && value != null) return Promise.all(ids.map(id => { const issue = issuesById.get(id)!; return onUpdateIssue(id, { subscriberIds: issue.subscriberIds.includes(value) ? issue.subscriberIds : [...issue.subscriberIds, value] }) }))
  if (action === 'removeSubscribers') return Promise.all(ids.map(id => onUpdateIssue(id, { subscriberIds: [] })))
  if (action === 'unassignMe') return onUpdateIssues(ids, { assigneeId: '' })
  const update = explorerUpdateForAction(action, value)
  if (update) return onUpdateIssues(ids, update)
}

export function explorerUpdateForAction(action: MyIssuesBulkAction | MyIssuesContextAction, value?: string): IssueUpdateInput | undefined {
  if (value == null) return
  if (action === 'status') return { stateId: value }
  if (action === 'priority') return { priority: Number(value) }
  if (action === 'assign' || action === 'assignee') return { assigneeId: value }
  if (action === 'project') return { projectId: value }
  if (action === 'dueDate') return { dueDate: value }
}

export function explorerUpdateForProperty(property: MyIssuesEditableProperty, value: string | string[]): IssueUpdateInput | undefined {
  if (property === 'labels' && Array.isArray(value)) return { labelIds: value }
  if (Array.isArray(value)) return
  if (property === 'status') return { stateId: value }
  if (property === 'priority') return { priority: Number(value) }
  if (property === 'assignee') return { assigneeId: value }
  if (property === 'project') return { projectId: value }
  if (property === 'dueDate') return { dueDate: value }
}

export function optimisticExplorerRow(row: MyIssuesRowData, input: IssueUpdateInput, data: BootstrapData): MyIssuesRowData {
  return {
    ...row,
    state: input.stateId === undefined ? row.state : data.states.find(state => state.id === input.stateId) ?? row.state,
    priority: input.priority === undefined ? row.priority : clampPriority(input.priority),
    assignee: input.assigneeId === undefined ? row.assignee : input.assigneeId ? (() => { const user = data.users.find(item => item.id === input.assigneeId); return user ? { id: user.id, name: user.displayName, avatarUrl: user.avatarUrl } : row.assignee })() : undefined,
    project: input.projectId === undefined ? row.project : input.projectId ? data.projects.find(project => project.id === input.projectId) : undefined,
    dueDate: input.dueDate === undefined ? row.dueDate : input.dueDate || undefined,
    labels: input.labelIds === undefined ? row.labels : input.labelIds.map(id => data.labels.find(label => label.id === id)).filter((label): label is NonNullable<typeof label> => Boolean(label)),
    updatedAt: new Date().toISOString(),
    sortOrder: input.sortOrder === undefined ? row.sortOrder : input.sortOrder,
  }
}

export function replaceExplorerRow(groups: MyIssuesGroupData[], row: MyIssuesRowData) {
  return groups.map(group => ({ ...group, issues: group.issues.map(issue => issue.id === row.id ? row : issue) }))
}

export function applyExplorerFilters(issues: Issue[], filters: MyIssuesAppliedFilter[], data?: BootstrapData) {
  const workspaceSlug = data?.workspace.urlKey ?? ''
  const allIssues = data?.issues ?? issues
  return issues.filter(issue => filters.every(filter => matchesExplorerFilter(issueToExplorerRow(issue, workspaceSlug, allIssues, data), filter)))
}

export function matchesExplorerFilter(issue: MyIssuesRowData, filter: MyIssuesAppliedFilter) {
  const values = filterValues(filter).map(value => value.value)
  let matched = true
  if (filter.field === 'priority') matched = values.includes(String(issue.priority))
  else if (filter.field === 'status') matched = values.includes(issue.state.id) || values.includes(issue.state.type)
  else if (filter.field === 'assignee') matched = values.includes(issue.assignee?.id ?? '')
  else if (filter.field === 'agent') matched = values.includes('*') ? Boolean(issue.delegate) : values.includes(issue.delegate?.id ?? '')
  else if (filter.field === 'agentSession') matched = values.includes('*') ? Boolean(issue.agentSessionId) : values.includes(issue.agentSessionId ?? '')
  else if (filter.field === 'creator') matched = values.includes(issue.creatorId ?? '')
  else if (filter.field === 'labels') matched = Boolean(issue.labels?.some(label => values.includes(label.id)))
  else if (filter.field === 'suggestedLabel') matched = values.includes('') ? !issue.suggestedLabelIds?.length : Boolean(issue.suggestedLabelIds?.some(id => values.includes(id)))
  else if (filter.field === 'project') matched = values.includes(issue.project?.id ?? '')
  else if (filter.field === 'projectProperties') matched = matchesProjectProperties(issue, values)
  else if (filter.field === 'initiative') matched = values.includes('') ? !issue.initiativeIds?.length : Boolean(issue.initiativeIds?.some(id => values.includes(id)))
  else if (filter.field === 'cycle') matched = values.includes(issue.cycleId ?? '')
  else if (filter.field === 'addedToCycle') matched = values.includes(issue.addedToCycle ?? '')
  else if (filter.field === 'releases') matched = matchesReleaseFilter(issue, values)
  else if (filter.field === 'dates') matched = values.some(value => matchesDateFilter(issue, value))
  else if (filter.field === 'subscribers') matched = values.includes('') ? !issue.subscriberIds?.length : Boolean(issue.subscriberIds?.some(id => values.includes(id)))
  else if (filter.field === 'relations') matched = values.includes('') ? !issue.relationTypes?.length : Boolean(issue.relationTypes?.some(type => values.includes(type)))
  else if (filter.field === 'links') matched = values.includes(issue.hasLinks ? 'has-links' : 'no-links')
  else if (filter.field === 'content') matched = values.some(value => value.startsWith('query:') && `${issue.title} ${issue.description ?? ''}`.toLocaleLowerCase().includes(value.slice(6).toLocaleLowerCase()))
  else if (filter.field === 'externalSource') matched = values.includes(issue.externalSource ?? '')
  else if (filter.field === 'autoClosed') matched = values.includes(String(Boolean(issue.autoClosed)))
  else if (filter.field === 'template') matched = values.includes(issue.templateId ?? '')
  else if (filter.field === 'ai') matched = matchesAIFilter(issue, values)
  else if (filter.field === 'advanced') matched = values.every(value => matchesAdvancedFilter(issue, value))
  return filter.operator === 'is' ? matched : !matched
}

function matchesProjectProperties(issue: MyIssuesRowData, values: string[]) {
  return values.some(value => value.startsWith('project-status:') ? issue.projectStatusId === value.slice(15) : value.startsWith('project-status-type:') ? issue.projectStatusType === value.slice(20) : value.startsWith('project-priority:') ? String(issue.projectPriority) === value.slice(17) : value.startsWith('project-label:') ? issue.projectLabelIds?.includes(value.slice(14)) : value === 'project-lead:' ? !issue.projectLeadId : value.startsWith('project-lead:') ? issue.projectLeadId === value.slice(13) : value.startsWith('project-milestone-name-contains:') ? issue.projectMilestoneNames?.some(name => name.toLocaleLowerCase().includes(value.slice(32).toLocaleLowerCase())) : false)
}
function matchesReleaseFilter(issue: MyIssuesRowData, values: string[]) {
  return values.some(value => value === 'no-releases' ? !issue.releaseIds?.length : value === 'released-any' ? Boolean(issue.hasReleasedRelease) : value.startsWith('release:') ? issue.releaseIds?.includes(value.slice(8)) : value.startsWith('release-pipeline:') ? issue.releasePipelineIds?.includes(value.slice(17)) : value.startsWith('release-stage:') ? issue.releaseStages?.includes(value.slice(14)) : value.startsWith('release-stage-type:') ? issue.releaseStatuses?.includes(value.slice(19)) : false)
}
function matchesDateFilter(issue: MyIssuesRowData, value: string) {
  const now = Date.now(); const age = (input: string | undefined, days: number) => Boolean(input && Date.parse(input) >= now - days * 86_400_000)
  if (value === 'created-past-day' || value === 'created-past-week' || value === 'created-past-month') return age(issue.createdAt, value.endsWith('day') ? 1 : value.endsWith('week') ? 7 : 30)
  if (value === 'updated-past-day' || value === 'updated-past-week' || value === 'updated-past-month') return age(issue.updatedAt, value.endsWith('day') ? 1 : value.endsWith('week') ? 7 : 30)
  if (value === 'started-any') return Boolean(issue.startedAt)
  if (value === 'completed-any') return Boolean(issue.completedAt)
  if (value === 'auto-closed-any') return Boolean(issue.autoClosedAt)
  if (value === 'triaged-any') return Boolean(issue.triagedAt)
  if (value === 'status-over-week') return Boolean(issue.statusChangedAt && Date.parse(issue.statusChangedAt) < now - 7 * 86_400_000)
  if (value === 'has-due-date') return Boolean(issue.dueDate)
  if (value === 'no-due-date') return !issue.dueDate
  if (!issue.dueDate) return false
  const due = Date.parse(`${issue.dueDate.slice(0, 10)}T00:00:00`); const today = new Date(); today.setHours(0, 0, 0, 0)
  if (value === 'overdue') return due < today.getTime()
  if (value === 'today') return due === today.getTime()
  if (value === 'next-week') return due >= today.getTime() && due <= today.getTime() + 7 * 86_400_000
  return false
}
function matchesAIFilter(issue: MyIssuesRowData, values: string[]) {
  return values.some(value => value === 'assigned-to-me' ? Boolean(issue.isAssignedToViewer) : value === 'completed-last-month' ? Boolean(issue.completedAt && Date.parse(issue.completedAt) >= Date.now() - 30 * 86_400_000) : value === 'due-next-two-weeks' ? Boolean(issue.dueDate && Date.parse(`${issue.dueDate.slice(0, 10)}T00:00:00`) <= Date.now() + 14 * 86_400_000) : value.startsWith('query:') ? `${issue.title} ${issue.description ?? ''}`.toLocaleLowerCase().includes(value.slice(6).toLocaleLowerCase()) : false)
}
function matchesAdvancedFilter(issue: MyIssuesRowData, value: string) {
  const separator = value.indexOf(':'); if (separator < 0) return true
  const field = value.slice(0, separator), expected = value.slice(separator + 1)
  return field === 'status' ? issue.state.id === expected || issue.state.type === expected : field === 'assignee' ? issue.assignee?.id === expected : field === 'priority' ? String(issue.priority) === expected : field === 'labels' ? Boolean(issue.labels?.some(label => label.id === expected)) : field === 'project' ? issue.project?.id === expected : true
}

export function buildExplorerIssueGroups(issues: MyIssuesRowData[], display: MyIssuesDisplayOptions, data: BootstrapData, view: TeamIssuesRouteView = 'all', manualOrder: string[] = []): MyIssuesGroupData[] {
  let projected = issues.filter(issue => display.showSubIssues || !issue.parentId)
  if (view !== 'all') projected = projected.filter(issue => issue.state.type !== 'completed' && issue.state.type !== 'canceled')
  else if (display.completedWindow === 'none') projected = projected.filter(issue => issue.state.type !== 'completed' && issue.state.type !== 'canceled')
  projected = [...projected].sort(issueComparator(display.ordering, manualOrder))
  const nested = display.nestedSubIssues ? nestedIssueProjection(projected) : undefined
  if (nested) projected = nested.rows
  if (display.grouping === 'none') return [{ id: 'all-issues', label: 'All issues', issues: projected }]
  const groups = new Map<string, MyIssuesGroupData>()
  for (const issue of projected) {
    const descriptor = groupForIssue(nested?.roots.get(issue.id) ?? issue, display.grouping)
    const group = groups.get(descriptor.id) ?? { ...descriptor, issues: [] }
    group.issues.push(issue)
    groups.set(group.id, group)
  }
  if (display.layout === 'board' && display.grouping === 'status' && display.showEmptyGroups) {
    for (const state of data.states.filter(state => stateVisibleInView(state.type, view, display.completedWindow))) {
      if (!groups.has(state.id)) groups.set(state.id, { id: state.id, label: state.name, stateType: state.type, state, issues: [] })
    }
  }
  const stateOrder = new Map(data.states.map((state, index) => [state.id, index]))
  const ordered = [...groups.values()].sort((left, right) => {
    if (display.grouping !== 'status' && display.grouping !== 'focus') return left.label.localeCompare(right.label)
    return (stateOrder.get(left.id) ?? 99) - (stateOrder.get(right.id) ?? 99)
  })
  return display.groupOrder === 'desc' ? ordered.reverse() : ordered
}

export function nestedIssueProjection(rows: MyIssuesRowData[]) {
  const byId = new Map(rows.map(row => [row.id, row]))
  const children = new Map<string, MyIssuesRowData[]>()
  for (const row of rows) {
    if (!row.parentId || !byId.has(row.parentId)) continue
    const siblings = children.get(row.parentId) ?? []
    siblings.push(row)
    children.set(row.parentId, siblings)
  }
  const ordered: MyIssuesRowData[] = []
  const roots = new Map<string, MyIssuesRowData>()
  const seen = new Set<string>()
  const visit = (row: MyIssuesRowData, root: MyIssuesRowData) => {
    if (seen.has(row.id)) return
    seen.add(row.id)
    ordered.push(row)
    roots.set(row.id, root)
    for (const child of children.get(row.id) ?? []) visit(child, root)
  }
  for (const row of rows) if (!row.parentId || !byId.has(row.parentId)) visit(row, row)
  for (const row of rows) visit(row, roots.get(row.id) ?? row)
  return { rows: ordered, roots }
}

function stateVisibleInView(type: string, view: TeamIssuesRouteView, completedWindow: MyIssuesDisplayOptions['completedWindow']) {
  if (view === 'active') return type === 'unstarted' || type === 'started'
  if (view === 'backlog') return type === 'backlog'
  return completedWindow !== 'none' || (type !== 'completed' && type !== 'canceled')
}

function groupForIssue(issue: MyIssuesRowData, grouping: MyIssuesGrouping): Omit<MyIssuesGroupData, 'issues'> {
  if (grouping === 'status' || grouping === 'focus') return { id: issue.state.id, label: issue.state.name, stateType: issue.state.type, state: issue.state }
  if (grouping === 'priority') return { id: `priority-${issue.priority}`, label: ['No priority', 'Urgent', 'High', 'Medium', 'Low'][issue.priority] }
  if (grouping === 'project') return { id: `project-${issue.project?.id ?? 'none'}`, label: issue.project?.name ?? 'No project' }
  if (grouping === 'assignee') return { id: `assignee-${issue.assignee?.id ?? 'none'}`, label: issue.assignee?.name ?? 'No assignee' }
  if (grouping === 'label') { const label = issue.labels?.[0]; return { id: `label-${label?.id ?? 'none'}`, label: label?.name ?? 'No label' } }
  return { id: `${grouping}-none`, label: grouping[0].toUpperCase() + grouping.slice(1) }
}

function issueComparator(ordering: MyIssuesDisplayOptions['ordering'], manualOrder: string[]) {
  const manual = new Map(manualOrder.map((id, index) => [id, index]))
  return (left: MyIssuesRowData, right: MyIssuesRowData) => {
    if (ordering === 'importance' && (manual.has(left.id) || manual.has(right.id))) return (manual.get(left.id) ?? 999999) - (manual.get(right.id) ?? 999999)
    if (ordering === 'created') return Date.parse(right.createdAt) - Date.parse(left.createdAt)
    if (ordering === 'updated') return Date.parse(right.updatedAt) - Date.parse(left.updatedAt)
    if (ordering === 'priority') return left.priority - right.priority || (left.sortOrder ?? 0) - (right.sortOrder ?? 0)
    return (left.sortOrder ?? 0) - (right.sortOrder ?? 0)
  }
}

export function explorerDueDateOptions(): MyIssuesBulkActionOption[] {
  const date = new Date(), day = 86_400_000
  return [{ id: '', label: 'No due date' }, { id: isoDate(date), label: 'Today' }, { id: isoDate(new Date(date.getTime() + day)), label: 'Tomorrow' }, { id: isoDate(new Date(date.getTime() + day * 7)), label: 'In one week' }]
}

function issueDateFilterOptions(issues: Issue[]): MyIssuesFilterOption[] {
  const definitions = [
    { id: 'overdue', label: 'Overdue' }, { id: 'today', label: 'Due today' }, { id: 'next-week', label: 'Due in the next week' },
    { id: 'has-due-date', label: 'Has due date' }, { id: 'no-due-date', label: 'No due date' },
  ]
  return definitions.map(option => ({ ...option, count: issues.filter(issue => matchesDateFilter(issueToExplorerRow(issue, ''), option.id)).length }))
}
function dateFilterCategories(issues:Issue[]):MyIssuesFilterOption[]{const simple=issueDateFilterOptions(issues);const common=[{id:'overdue',label:'Overdue'},{id:'today',label:'Due today'},{id:'next-week',label:'Due in the next week'},{id:'has-due-date',label:'Has due date'},{id:'no-due-date',label:'No due date'}].map(item=>simple.find(option=>option.id===item.id)??item);return[
  {id:'due-date',label:'Due date',children:common},
  {id:'created-date',label:'Created date',children:[{id:'created-past-day',label:'Past day'},{id:'created-past-week',label:'Past week'},{id:'created-past-month',label:'Past month'}]},
  {id:'updated-date',label:'Updated date',children:[{id:'updated-past-day',label:'Past day'},{id:'updated-past-week',label:'Past week'},{id:'updated-past-month',label:'Past month'}]},
  {id:'started-date',label:'Started date',children:[{id:'started-any',label:'Has started date'}]},
  {id:'completed-date',label:'Completed date',children:[{id:'completed-any',label:'Has completed date'}]},
  {id:'auto-closed-date',label:'Auto-closed date',children:[{id:'auto-closed-any',label:'Has auto-closed date'}]},
  {id:'released-date',label:'Released date',children:[{id:'released-any',label:'Has released date'}]},
  {id:'triaged-date',label:'Triaged date',children:[{id:'triaged-any',label:'Has triaged date'}]},
  {id:'time-current-status',label:'Time in current status',children:[{id:'status-over-week',label:'More than one week'}]},
]}
function releaseFilterCategories(data:BootstrapData,issues:Issue[]):MyIssuesFilterOption[]{const count=(predicate:(issue:Issue)=>boolean)=>issues.filter(predicate).length;return[
  {id:'release',label:'Release',children:data.releases.map(release=>({id:`release:${release.id}`,label:release.name,count:count(issue=>release.issueIds.includes(issue.id))}))},
  {id:'release-pipeline',label:'Release pipeline',children:data.releasePipelines.map(pipeline=>({id:`release-pipeline:${pipeline.id}`,label:pipeline.name,count:count(issue=>data.releases.some(release=>release.pipelineId===pipeline.id&&release.issueIds.includes(issue.id)))}))},
  {id:'release-stage',label:'Release stage',children:uniqueStrings(data.releases.map(release=>release.stage)).map(stage=>({id:`release-stage:${stage}`,label:stage,count:count(issue=>data.releases.some(release=>release.stage===stage&&release.issueIds.includes(issue.id)))}))},
  {id:'release-stage-type',label:'Release stage type',children:['planned','inProgress','released','canceled'].map(status=>({id:`release-stage-type:${status}`,label:status,count:count(issue=>data.releases.some(release=>release.status===status&&release.issueIds.includes(issue.id)))}))},
  {id:'released-date',label:'Released date',children:[{id:'released-any',label:'Has released date'}]},
  {id:'no-releases',label:'No releases',count:count(issue=>!data.releases.some(release=>release.issueIds.includes(issue.id)))},
]}


function uniqueStrings(values:(string|undefined)[]){return [...new Set(values.filter((value):value is string=>Boolean(value)))]}
function relationFilterOptions(issues:Issue[]):MyIssuesFilterOption[]{const count=(predicate:(issue:Issue)=>boolean)=>issues.filter(predicate).length;return[
  {id:'parent_of',label:'Parent issues',count:count(issue=>issue.relations.some(relation=>relation.type==='parent_of'))},
  {id:'sub_issue_of',label:'Sub-issues',count:count(issue=>Boolean(issue.parentId))},
  {id:'blocked_by',label:'Blocked issues',count:count(issue=>issue.relations.some(relation=>relation.type==='blocked_by'))},
  {id:'blocks',label:'Blocking issues',count:count(issue=>issue.relations.some(relation=>relation.type==='blocks'))},
  {id:'recurring',label:'Recurring issues',children:[{id:'recurring-any',label:'Any recurring issue'},{id:'recurring-none',label:'Not recurring'}]},
  {id:'has-relations',label:'Issues with relations',count:count(issue=>issue.relations.length>0)},
  {id:'duplicate',label:'Duplicates',count:count(issue=>issue.relations.some(relation=>relation.type==='duplicate'))},
]}
function projectPropertyFilterOptions(data:BootstrapData,issues:Issue[]):MyIssuesFilterOption[]{const projects=data.projects;const issueProject=(issue:Issue)=>projects.find(project=>project.id===issue.project?.id);const count=(predicate:(issue:Issue)=>boolean)=>issues.filter(predicate).length;return[
  {id:'project-status',label:'Project status',kind:'projectStatusCategory',children:data.projectStatuses.map(status=>({id:`project-status:${status.id}`,label:status.name,color:status.color,projectType:status.type,kind:'projectStatus',filterLabel:'Project status',count:count(issue=>issueProject(issue)?.status.id===status.id)}))},
  {id:'project-status-type',label:'Project status type',kind:'projectStatusTypeCategory',children:uniqueStrings(projects.map(project=>project.status.type)).map(type=>({id:`project-status-type:${type}`,label:projectStatusTypeLabel(type),projectType:type,kind:'projectStatusType',filterLabel:'Project status type',count:count(issue=>issueProject(issue)?.status.type===type)}))},
  {id:'project-priority',label:'Project priority',kind:'projectPriorityCategory',children:['0','1','2','3','4'].map(value=>{const priority=Number(value) as 0|1|2|3|4;return{id:`project-priority:${value}`,label:['No priority','Urgent','High','Medium','Low'][priority],priority,kind:'projectPriority',filterLabel:'Project priority',count:count(issue=>issueProject(issue)?.priority===priority)}})},
  {id:'project-labels',label:'Project labels',kind:'projectLabels',children:labelsForResource(data.labels,'project').map(label=>({id:`project-label:${label.id}`,label:label.name,color:label.color,kind:'projectLabels',filterLabel:'Project labels',count:count(issue=>issueProject(issue)?.labelIds?.includes(label.id)??false)}))},
  {id:'project-lead',label:'Project lead',kind:'projectLeadCategory',children:[{id:'project-lead:',label:'No lead',kind:'projectLead',filterLabel:'Project lead',count:count(issue=>Boolean(issueProject(issue)&&!issueProject(issue)?.lead))},{id:`project-lead:${data.viewer.id}`,label:'Current user',kind:'projectLead',filterLabel:'Project lead',avatarUrl:data.viewer.avatarUrl,count:count(issue=>issueProject(issue)?.lead?.id===data.viewer.id)},...data.users.filter(user=>user.active&&user.id!==data.viewer.id).map(user=>({id:`project-lead:${user.id}`,label:user.displayName,kind:'projectLead',filterLabel:'Project lead',avatarUrl:user.avatarUrl,count:count(issue=>issueProject(issue)?.lead?.id===user.id)}))]},
  {id:'project-milestone',label:'Project milestone name',kind:'projectMilestoneCategory',children:[{id:'project-milestone-name-contains',label:'Milestone name contains…',kind:'textCondition',filterLabel:'Project milestone name',operatorLabel:'contains',negativeOperatorLabel:'does not contain',textConditionPrefix:'project-milestone-name-contains:'}]},
]}
function projectStatusTypeLabel(type:string){return({backlog:'Backlog',planned:'Planned',started:'In Progress',completed:'Completed',canceled:'Canceled'} as Record<string,string>)[type]??type}

export function stateIdForExplorerGroup(group: MyIssuesGroupData, data: BootstrapData) {
  return group.id === 'other-active' ? data.states.find(state => state.type === 'started')?.id : data.states.find(state => state.id === group.id)?.id
}

export function withoutMapKey(map: Map<string, string>, key: string) { const next = new Map(map); next.delete(key); return next }
export function withMapKey(map: Map<string, string>, key: string, value: string) { const next = new Map(map); next.set(key, value); return next }

async function copyIssues(action: MyIssuesBulkAction, ids: string[], issuesById: Map<string, Issue>, workspaceSlug: string) {
  const issues = ids.map(id => issuesById.get(id)).filter(Boolean) as Issue[]
  const lines = issues.map(issue => {
    const url = `${location.origin}/${workspaceSlug}/issue/${issue.identifier}`
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

function clampPriority(value: number): 0 | 1 | 2 | 3 | 4 { return Math.max(0, Math.min(4, value)) as 0 | 1 | 2 | 3 | 4 }
function isoDate(date: Date) { return date.toISOString().slice(0, 10) }
function slug(value: string) { return value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 50) }

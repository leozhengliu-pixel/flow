import { issuePath } from '@/lib/app-routes'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import type { MyIssuesBulkAction, MyIssuesBulkActionOption } from '@/components/my-issues/my-issues-bulk-action-bar'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-bar'
import type { MyIssuesContextAction, MyIssuesContextOption, MyIssuesEditableProperty, MyIssuesGroupData, MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import type { MyIssuesDisplayOptions, MyIssuesGrouping } from '@/components/my-issues/my-issues-surface'
import type { TeamIssuesRouteView } from '@/lib/app-routes'
import { filterValues } from '@/components/my-issues/my-issues-filter-types'
import { labelsForResource, setGroupedLabelSelected, toggleGroupedLabelIds } from '@/lib/labels'
import { milestoneIssueProgress } from '@/components/issue/milestone-progress'

export const ISSUE_FILTER_LABELS: Partial<Record<MyIssuesFilterKey, string>> = {
  ai:'AI filter',advanced:'Advanced filter',status:'Status',assignee:'Assignee',agent:'Agent',agentSession:'Agent Session',creator:'Creator',priority:'Priority',labels:'Labels',relations:'Relations',suggestedLabel:'Suggested label',dates:'Dates',projectMilestone:'Project milestone',project:'Project',projectProperties:'Project properties',initiative:'Initiative',cycle:'Cycle',addedToCycle:'Added to cycle',releases:'Releases',customers:'Customers',subscribers:'Subscribers',externalSource:'External source',autoClosed:'Auto-closed',content:'Content',links:'Links',template:'Template',
}

const PRIORITIES: MyIssuesContextOption[] = ['No priority', 'Urgent', 'High', 'Medium', 'Low'].map((label, id) => ({
  id: String(id), label, kind: 'priority', priority: id as 0 | 1 | 2 | 3 | 4,
}))

const ISSUE_INDEX_CACHE = new WeakMap<Issue[], Map<string, Issue>>()
const DATA_INDEX_CACHE = new WeakMap<BootstrapData, ReturnType<typeof buildExplorerDataIndex>>()

export function issueToExplorerRow(issue: Issue, workspaceSlug: string, issues: Issue[] = [], data?: BootstrapData): MyIssuesRowData {
  const issuesById = issueIndex(issues)
  const index = data ? explorerDataIndex(data) : undefined
  const fullProject = index?.projectsById.get(issue.project?.id ?? '')
  const issueReleases=index?.releasesByIssueId.get(issue.id)??[]
  const issuePullRequests=index?.reviewsByIssueId.get(issue.id)??[]
  const issueSla=index?.slaByIssueId.get(issue.id)
  const slaRule=issueSla ? index?.slaRulesById.get(issueSla.ruleId) : undefined
  const myActivityAt=data?.viewer?.id ? data.activities?.[issue.id]?.filter(event=>event.actor.id===data.viewer.id).sort((a,b)=>Date.parse(b.createdAt)-Date.parse(a.createdAt))[0]?.createdAt : undefined
  const statusIntervals = issueStatusIntervals(issue, data)
  return {
    id: issue.id,
    teamId: issue.team.id,
    teamName: issue.team.name,
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
    cycleName: issue.cycleId ? index?.cyclesById.get(issue.cycleId)?.name : undefined,
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
    projectMilestoneId: issue.projectMilestoneId,
    projectMilestoneNames:fullProject?.milestones?.map(milestone=>milestone.name)??[],
    milestoneProgress: issue.projectMilestoneId && fullProject ? milestoneIssueProgress(data?.issues ?? issues, fullProject.id, issue.projectMilestoneId) : undefined,
    rawMilestoneDate: fullProject?.milestones?.find(milestone => milestone.id === issue.projectMilestoneId)?.targetDate,
    ...issueCustomerFields(issue.id, index),
    releaseIds:issueReleases.map(release=>release.id),releasePipelineIds:issueReleases.map(release=>release.pipelineId).filter((id):id is string=>Boolean(id)),releaseStages:issueReleases.map(release=>release.stage).filter((stage):stage is string=>Boolean(stage)),releaseStatuses:issueReleases.map(release=>release.status),hasReleasedRelease:issueReleases.some(release=>Boolean(release.releasedAt)),
    releaseCount: issueReleases.length,
    sla: issueSla ? { ...issueSla, ruleName: slaRule?.name } : undefined,
    subscriberIds: issue.subscriberIds??[],
    relationTypes: issue.relations?.map(relation => relation.type)??[],
    hasLinks: (issue.attachments?.length??0) > 0,
    linkCount: issue.attachments?.length ?? 0,
    pullRequestCount: issuePullRequests.length,
    hasContent: Boolean(issue.title?.trim() || issue.description?.trim()),
    estimate: issue.estimate,
    dueDate: issue.dueDate,
    createdAt: issue.createdAt,
    updatedAt: issue.updatedAt,
    completedAt: issue.completedAt,
    startedAt: issue.startedAt,
    statusChangedAt: issue.statusChangedAt,
    statusIntervals,
    timeInStatusMinutes: timeInStatusMinutes(statusIntervals),
    myActivityAt,
    canceledAt: issue.canceledAt,
    archivedAt: issue.archivedAt,
    parentId: issue.parentId,
    ...issueHierarchyFields(issue, issues, issuesById),
    sortOrder: issue.sortOrder,
  }
}

function issueIndex(issues: Issue[]) {
  const cached = ISSUE_INDEX_CACHE.get(issues)
  if (cached) return cached
  const index = new Map(issues.map(issue => [issue.id, issue]))
  ISSUE_INDEX_CACHE.set(issues, index)
  return index
}

function explorerDataIndex(data: BootstrapData) {
  const cached = DATA_INDEX_CACHE.get(data)
  if (cached) return cached
  const index = buildExplorerDataIndex(data)
  DATA_INDEX_CACHE.set(data, index)
  return index
}

function buildExplorerDataIndex(data: BootstrapData) {
  const releasesByIssueId = new Map<string, BootstrapData['releases']>()
  for (const release of data.releases ?? []) for (const issueId of release.issueIds ?? []) pushToArrayMap(releasesByIssueId, issueId, release)
  const reviewsByIssueId = new Map<string, BootstrapData['reviews']>()
  for (const review of data.reviews ?? []) for (const issueId of review.issueIds ?? []) pushToArrayMap(reviewsByIssueId, issueId, review)
  return {
    projectsById: new Map(data.projects.map(project => [project.id, project])),
    cyclesById: new Map(data.cycles.map(cycle => [cycle.id, cycle])),
    releasesByIssueId,
    reviewsByIssueId,
    slaByIssueId: new Map((data.issueSlas ?? []).filter(sla => sla.status !== 'removed').map(sla => [sla.issueId, sla])),
    slaRulesById: new Map((data.slaRules ?? []).map(rule => [rule.id, rule])),
    ...indexIssueCustomers(data),
  }
}

function indexIssueCustomers(data: BootstrapData) {
  const customersById = new Map((data.customers ?? []).map(customer => [customer.id, customer]))
  const customersByIssueId = new Map<string, NonNullable<BootstrapData['customers']>>()
  const unknownCustomerIssueIds = new Set<string>()
  for (const request of data.customerRequests ?? []) {
    if (!request.issueId || request.archivedAt) continue
    const customer = customersById.get(request.customerId)
    if (!customer) { unknownCustomerIssueIds.add(request.issueId); continue }
    const current = customersByIssueId.get(request.issueId) ?? []
    if (!current.some(item => item.id === customer.id)) current.push(customer)
    customersByIssueId.set(request.issueId, current)
  }
  return { customersByIssueId, unknownCustomerIssueIds }
}

function issueCustomerFields(issueId: string, index?: ReturnType<typeof buildExplorerDataIndex>) {
  const customers = index?.customersByIssueId.get(issueId) ?? []
  return {
    customerIds: customers.map(customer => customer.id),
    customerNames: customers.map(customer => customer.name),
    hasUnknownCustomer: index?.unknownCustomerIssueIds.has(issueId) ?? false,
    customerOwnerIds: customers.map(customer => customer.ownerId ?? ''),
    customerStatuses: customers.map(customer => customer.status).filter(Boolean),
    customerTiers: customers.map(customer => customer.tier ?? '').filter(Boolean),
    customerRevenues: customers.map(customer => customer.annualRevenue ?? 0),
    customerSizes: customers.map(customer => customer.size ?? 0),
  }
}

function pushToArrayMap<T>(values: Map<string, T[]>, key: string, value: T) {
  const current = values.get(key) ?? []
  current.push(value)
  values.set(key, current)
}

function timeInStatusMinutes(intervals: NonNullable<MyIssuesRowData['statusIntervals']>) {
  const current = intervals.at(-1)
  if (!current?.enteredAt || current.exitedAt) return undefined
  const elapsed = Math.floor((Date.now() - Date.parse(current.enteredAt)) / 60000)
  return Number.isFinite(elapsed) && elapsed >= 0 ? elapsed : undefined
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

export function issueHierarchyFields(issue: Issue, issues: Issue[], byId = issueIndex(issues)): Pick<MyIssuesRowData,'parent'|'ancestors'|'subIssueProgress'|'subIssues'> {
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
  const issueLabels = labelsForResource(data.labels, 'issue', data.labelGroups)
  const labelGroupNames = new Map(data.labelGroups.filter(group => group.resourceType === 'issue').map(group => [group.id, group.name]))
  const issueTeamIds = [...new Set(issues.map(issue => issue.team.id))]
  const scopedStates = issueTeamIds.length === 1 && data.states.some(state => state.teamId === issueTeamIds[0]) ? data.states.filter(state => state.teamId === issueTeamIds[0]) : data.states
  const projectsById = new Map(data.projects.map(project => [project.id, project]))
  const statusCounts = new Map<string, number>(), priorityCounts = new Map<string, number>(), assigneeCounts = new Map<string, number>(), creatorCounts = new Map<string, number>(), agentCounts = new Map<string, number>(), labelCounts = new Map<string, number>(), projectCounts = new Map<string, number>(), initiativeCounts = new Map<string, number>(), cycleCounts = new Map<string, number>(), addedToCycleCounts = new Map<string, number>(), subscriberCounts = new Map<string, number>(), externalSourceCounts = new Map<string, number>(), templateCounts = new Map<string, number>(), suggestedLabelCounts = new Map<string, number>()
  let noAssignee = 0, noAgent = 0, anyAgent = 0, noAgentSession = 0, anyAgentSession = 0, noProject = 0, noInitiative = 0, noCycle = 0, noSubscribers = 0, noExternalSource = 0, autoClosed = 0, notAutoClosed = 0, noTemplate = 0, noSuggestedLabel = 0, withLinks = 0
  for (const issue of issues) {
    incrementCount(statusCounts, issue.state.id)
    incrementCount(priorityCounts, String(issue.priority))
    if (issue.assignee) incrementCount(assigneeCounts, issue.assignee.id); else noAssignee += 1
    incrementCount(creatorCounts, issue.creator.id)
    if (issue.delegate) { incrementCount(agentCounts, issue.delegate.id); anyAgent += 1 } else noAgent += 1
    if (issue.agentSessionId) anyAgentSession += 1; else noAgentSession += 1
    for (const label of issue.labels ?? []) incrementCount(labelCounts, label.id)
    const project = projectsById.get(issue.project?.id ?? '')
    if (issue.project) incrementCount(projectCounts, issue.project.id); else noProject += 1
    if (project?.initiatives?.length) for (const initiativeId of project.initiatives) incrementCount(initiativeCounts, initiativeId); else noInitiative += 1
    if (issue.cycleId) incrementCount(cycleCounts, issue.cycleId); else noCycle += 1
    if (issue.addedToCycle) incrementCount(addedToCycleCounts, issue.addedToCycle)
    if (issue.subscriberIds?.length) for (const subscriberId of issue.subscriberIds) incrementCount(subscriberCounts, subscriberId); else noSubscribers += 1
    if (issue.externalSource) incrementCount(externalSourceCounts, issue.externalSource); else noExternalSource += 1
    if (issue.autoClosed) autoClosed += 1; else notAutoClosed += 1
    if (issue.templateId) incrementCount(templateCounts, issue.templateId); else noTemplate += 1
    if (issue.suggestedLabelIds?.length) for (const labelId of issue.suggestedLabelIds) incrementCount(suggestedLabelCounts, labelId); else noSuggestedLabel += 1
    if (issue.attachments?.length) withLinks += 1
  }
  return {
    status: [...scopedStates].sort((a, b) => (a.position??0) - (b.position??0)).map(state => ({ id: state.id, teamId: state.teamId, label: state.name, color: state.color, count: statusCounts.get(state.id) ?? 0, kind: 'status' as const, stateType: state.type })),
    priority: PRIORITIES.map(priority => ({ ...priority, count: priorityCounts.get(priority.id) ?? 0 })),
    assignee: [{ id: '', label: 'No assignee', count: noAssignee, kind: 'assignee' as const }, ...data.users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, count: assigneeCounts.get(user.id) ?? 0, kind: 'assignee' as const }))],
    creator: data.users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, count: creatorCounts.get(user.id) ?? 0, kind: 'creator' as const })),
    agent: [{ id: '', label: 'No agent', count: noAgent }, { id: '*', label: 'Any agent', count: anyAgent }, ...data.users.filter(user => user.active && agentCounts.has(user.id)).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, count: agentCounts.get(user.id) ?? 0 }))],
    agentSession:[{id:'',label:'No agent session',count:noAgentSession},{id:'*',label:'Any agent session',count:anyAgentSession}],
    dueDate: explorerDueDateOptions().map(option => ({ ...option, kind: 'dueDate' as const })),
    dates: dateFilterCategories(issues),
    labels: issueLabels.map(label => ({ id: label.id, label: label.name, color: label.color, description: label.description, issueCount: label.issueCount, scope: label.scope, resourceType: label.resourceType, groupId: label.groupId, groupLabel: label.groupId ? labelGroupNames.get(label.groupId) : undefined, count: labelCounts.get(label.id) ?? 0, kind: 'labels' as const })),
    project: [{ id: '', label: 'No project', count: noProject, kind: 'project' as const }, ...data.projects.map(project => ({ id: project.id, label: project.name, color: project.color, count: projectCounts.get(project.id) ?? 0, kind: 'project' as const }))],
    projectMilestone: projectMilestoneFilterOptions(data, issues),
    projectProperties:projectPropertyFilterOptions(data,issues),
    initiative:[{id:'',label:'No initiative',count:noInitiative},...data.initiatives.map(initiative=>({id:initiative.id,label:initiative.name,count:initiativeCounts.get(initiative.id) ?? 0}))],
    cycle: [{ id: '', label: 'No cycle', count: noCycle, kind: 'cycle' as const }, ...data.cycles.map(cycle => ({ id: cycle.id, teamId: cycle.teamId, label: cycle.name, count: cycleCounts.get(cycle.id) ?? 0, kind: 'cycle' as const }))],
    addedToCycle:[{id:'planned',label:'Planned',count:addedToCycleCounts.get('planned') ?? 0},{id:'during',label:'During cycle',count:addedToCycleCounts.get('during') ?? 0},{id:'after',label:'After cycle',count:addedToCycleCounts.get('after') ?? 0}],
    releases: releaseFilterCategories(data,issues),
    customers: customerFilterOptions(data, issues),
    subscribers: [{ id: '', label: 'No subscribers', count: noSubscribers, kind: 'subscribers' as const }, ...data.users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, count: subscriberCounts.get(user.id) ?? 0, kind: 'subscribers' as const }))],
    externalSource:[{id:'',label:'No external source',count:noExternalSource},...[...externalSourceCounts].map(([source,count])=>({id:source,label:source,count}))],
    autoClosed:[{id:'true',label:'Auto-closed',count:autoClosed},{id:'false',label:'Not auto-closed',count:notAutoClosed}],
    template:[{id:'',label:'No template',count:noTemplate},...data.issueTemplates.map(template=>({id:template.id,label:template.name,count:templateCounts.get(template.id) ?? 0}))],
    suggestedLabel:[{id:'',label:'No suggested label',count:noSuggestedLabel},...issueLabels.map(label=>({id:label.id,label:label.name,color:label.color,count:suggestedLabelCounts.get(label.id) ?? 0}))],
    relations: relationFilterOptions(issues),
    links: [{ id: 'has-links', label: 'Has links', count: withLinks, kind: 'links' as const }, { id: 'no-links', label: 'No links', count: issues.length - withLinks, kind: 'links' as const }],
    content: [{ id: 'content-prompt', label: 'Filter by content…' }],
  }
}

function incrementCount(counts: Map<string, number>, key: string, amount = 1) {
  counts.set(key, (counts.get(key) ?? 0) + amount)
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
  if (field === 'labels') return [{ id: '', label: 'No labels', kind: 'labels' as const }, ...options.labels]
  if (field === 'status'||field==='assignee'||field==='agent'||field==='agentSession'||field==='creator'||field==='priority'||field==='relations'||field==='suggestedLabel'||field==='dates'||field==='projectMilestone'||field==='project'||field==='projectProperties'||field==='initiative'||field==='cycle'||field==='addedToCycle'||field==='releases'||field==='customers'||field==='subscribers'||field==='externalSource'||field==='autoClosed'||field==='content'||field==='links'||field==='template') return options[field]
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
  if (action === 'labels' && value != null) {
    const selected = !ids.every(id => issuesById.get(id)?.labels.some(label => label.id === value))
    return Promise.all(ids.map(id => { const issue = issuesById.get(id)!; return onUpdateIssue(id, { labelIds: setGroupedLabelSelected(issue.labels.map(label => label.id), value, data.labels, selected) }) }))
  }
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
  if (property === 'cycle') return { cycleId: value }
}

/** Build the persisted property change when a card is moved between board groups. */
export function explorerBoardGroupUpdate(row: MyIssuesRowData, grouping: MyIssuesGrouping, targetGroupId: string, data: BootstrapData): IssueUpdateInput {
  if ((grouping === 'status' || grouping === 'focus') && data.states.some(state => state.id === targetGroupId)) return { stateId: targetGroupId }
  if (grouping === 'priority' && targetGroupId.startsWith('priority-')) return { priority: Number(targetGroupId.slice('priority-'.length)) }
  if (grouping === 'project' && targetGroupId.startsWith('project-')) return { projectId: targetGroupId === 'project-none' ? '' : targetGroupId.slice('project-'.length) }
  if (grouping === 'assignee' && targetGroupId.startsWith('assignee-')) return { assigneeId: targetGroupId === 'assignee-none' ? '' : targetGroupId.slice('assignee-'.length) }
  if (grouping === 'label' && targetGroupId.startsWith('label-')) {
    if (targetGroupId === 'label-none') return { labelIds: [] }
    const labelId = targetGroupId.slice('label-'.length)
    return { labelIds: toggleGroupedLabelIds((row.labels ?? []).map(label => label.id), labelId, data.labels) }
  }
  if (grouping === 'cycle' && targetGroupId.startsWith('cycle-')) return { cycleId: targetGroupId === 'cycle-none' ? '' : targetGroupId.slice('cycle-'.length) }
  return {}
}

export function optimisticExplorerRow(row: MyIssuesRowData, input: IssueUpdateInput, data: BootstrapData): MyIssuesRowData {
  return {
    ...row,
    state: input.stateId === undefined ? row.state : data.states.find(state => state.id === input.stateId) ?? row.state,
    priority: input.priority === undefined ? row.priority : clampPriority(input.priority),
    assignee: input.assigneeId === undefined ? row.assignee : input.assigneeId ? (() => { const user = data.users.find(item => item.id === input.assigneeId); return user ? { id: user.id, name: user.displayName, avatarUrl: user.avatarUrl } : row.assignee })() : undefined,
    project: input.projectId === undefined ? row.project : input.projectId ? data.projects.find(project => project.id === input.projectId) : undefined,
    dueDate: input.dueDate === undefined ? row.dueDate : input.dueDate || undefined,
    cycleId: input.cycleId === undefined ? row.cycleId : input.cycleId || undefined,
    cycleName: input.cycleId === undefined ? row.cycleName : data.cycles.find(cycle => cycle.id === input.cycleId)?.name,
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
  else if (filter.field === 'projectMilestone') matched = values.includes(issue.projectMilestoneId ?? '')
  else if (filter.field === 'projectProperties') matched = matchesProjectProperties(issue, values)
  else if (filter.field === 'initiative') matched = values.includes('') ? !issue.initiativeIds?.length : Boolean(issue.initiativeIds?.some(id => values.includes(id)))
  else if (filter.field === 'cycle') matched = values.includes(issue.cycleId ?? '')
  else if (filter.field === 'addedToCycle') matched = values.includes(issue.addedToCycle ?? '')
  else if (filter.field === 'releases') matched = matchesReleaseFilter(issue, values)
  else if (filter.field === 'customers') matched = matchesCustomerFilter(issue, values)
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
function matchesCustomerFilter(issue: MyIssuesRowData, values: string[]) {
  return values.some(value => {
    if (value === 'customer:') return Boolean(issue.hasUnknownCustomer)
    if (value.startsWith('customer:')) return issue.customerIds?.includes(value.slice(9))
    if (value === 'customer-count:0') return !issue.customerIds?.length && !issue.hasUnknownCustomer
    if (value === 'customer-count:1') return (issue.customerIds?.length ?? 0) + (issue.hasUnknownCustomer ? 1 : 0) === 1
    if (value === 'customer-count:2+') return (issue.customerIds?.length ?? 0) + (issue.hasUnknownCustomer ? 1 : 0) >= 2
    if (value === 'customer-owner:') return Boolean(issue.customerIds?.length) && !(issue.customerOwnerIds ?? []).some(Boolean)
    if (value.startsWith('customer-owner:')) return issue.customerOwnerIds?.includes(value.slice(15))
    if (value.startsWith('customer-status:')) return issue.customerStatuses?.includes(value.slice(16))
    if (value.startsWith('customer-tier:')) return issue.customerTiers?.includes(value.slice(14))
    if (value === 'customer-revenue:') return Boolean(issue.customerIds?.length) && !(issue.customerRevenues ?? []).some(amount => amount > 0)
    if (value === 'customer-revenue:any') return (issue.customerRevenues ?? []).some(amount => amount > 0)
    if (value === 'customer-size:') return Boolean(issue.customerIds?.length) && !(issue.customerSizes ?? []).some(size => size > 0)
    if (value === 'customer-size:any') return (issue.customerSizes ?? []).some(size => size > 0)
    return false
  })
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
      if (!groups.has(state.id)) groups.set(state.id, { id: state.id, label: state.name, stateType: state.type, state, createContext: { stateId: state.id }, issues: [] })
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
  if (grouping === 'status' || grouping === 'focus') return { id: issue.state.id, label: issue.state.name, stateType: issue.state.type, state: issue.state, createContext: { stateId: issue.state.id } }
  if (grouping === 'priority') return { id: `priority-${issue.priority}`, label: ['No priority', 'Urgent', 'High', 'Medium', 'Low'][issue.priority], createContext: { priority: issue.priority } }
  if (grouping === 'project') return { id: `project-${issue.project?.id ?? 'none'}`, label: issue.project?.name ?? 'No project', createContext: { projectId: issue.project?.id ?? '' } }
  if (grouping === 'assignee') return { id: `assignee-${issue.assignee?.id ?? 'none'}`, label: issue.assignee?.name ?? 'No assignee', createContext: { assigneeId: issue.assignee?.id ?? '' } }
  if (grouping === 'label') { const label = issue.labels?.[0]; return { id: `label-${label?.id ?? 'none'}`, label: label?.name ?? 'No label', createContext: { labelIds: label ? [label.id] : [] } } }
  if (grouping === 'cycle') return { id: `cycle-${issue.cycleId ?? 'none'}`, label: issue.cycleName ?? 'No cycle', createContext: { cycleId: issue.cycleId ?? '' } }
  if (grouping === 'team') return { id: `team-${issue.teamId ?? 'none'}`, label: issue.teamName ?? 'No team', createContext: { teamId: issue.teamId } }
  if (grouping === 'agent') return { id: `agent-${issue.agentSessionId ?? 'none'}`, label: issue.agentSessionId ? 'Agent session' : 'No agent' }
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
function releaseFilterCategories(data:BootstrapData,issues:Issue[]):MyIssuesFilterOption[]{const issueIds=new Set(issues.map(issue=>issue.id)),releaseCounts=new Map<string,number>(),pipelineIssues=new Map<string,Set<string>>(),stageIssues=new Map<string,Set<string>>(),statusIssues=new Map<string,Set<string>>(),releasedIssueIds=new Set<string>();for(const release of data.releases){for(const issueId of release.issueIds){if(!issueIds.has(issueId))continue;incrementCount(releaseCounts,release.id);releasedIssueIds.add(issueId);addToSetMap(pipelineIssues,release.pipelineId,issueId);addToSetMap(stageIssues,release.stage,issueId);addToSetMap(statusIssues,release.status,issueId)}}return[
  {id:'release',label:'Release',children:data.releases.map(release=>({id:`release:${release.id}`,label:release.name,count:releaseCounts.get(release.id)??0}))},
  {id:'release-pipeline',label:'Release pipeline',children:data.releasePipelines.map(pipeline=>({id:`release-pipeline:${pipeline.id}`,label:pipeline.name,count:pipelineIssues.get(pipeline.id)?.size??0}))},
  {id:'release-stage',label:'Release stage',children:uniqueStrings(data.releases.map(release=>release.stage)).map(stage=>({id:`release-stage:${stage}`,label:stage,count:stageIssues.get(stage)?.size??0}))},
  {id:'release-stage-type',label:'Release stage type',children:['planned','inProgress','released','canceled'].map(status=>({id:`release-stage-type:${status}`,label:status,count:statusIssues.get(status)?.size??0}))},
  {id:'released-date',label:'Released date',children:[{id:'released-any',label:'Has released date'}]},
  {id:'no-releases',label:'No releases',count:issues.length-releasedIssueIds.size},
]}


function projectMilestoneFilterOptions(data: BootstrapData, issues: Issue[]): MyIssuesFilterOption[] {
  const counts = new Map<string, number>()
  let noMilestone = 0
  for (const issue of issues) {
    if (issue.projectMilestoneId) incrementCount(counts, issue.projectMilestoneId)
    else noMilestone += 1
  }
  const projectIds = new Set(issues.map(issue => issue.project?.id).filter((id): id is string => Boolean(id)))
  const milestones = data.projects
    .filter(project => !projectIds.size || projectIds.has(project.id))
    .flatMap(project => project.milestones ?? [])
  const seen = new Set<string>()
  const options: MyIssuesFilterOption[] = [{ id: '', label: 'No milestone', count: noMilestone, kind: 'projectMilestone' }]
  for (const milestone of milestones) {
    if (seen.has(milestone.id)) continue
    seen.add(milestone.id)
    options.push({ id: milestone.id, label: milestone.name, count: counts.get(milestone.id) ?? 0, kind: 'projectMilestone' })
  }
  return options
}
function customerFilterOptions(data: BootstrapData, issues: Issue[]): MyIssuesFilterOption[] {
  const index = explorerDataIndex(data)
  const rows = issues.map(issue => issueCustomerFields(issue.id, index))
  const count = (predicate: (row: ReturnType<typeof issueCustomerFields>) => boolean) => rows.filter(predicate).length
  const statusCatalog = data.customerStatuses?.length
    ? data.customerStatuses.filter(status => !status.archivedAt)
    : uniqueStrings((data.customers ?? []).map(customer => customer.status)).map(status => ({ id: status, name: status, color: undefined as string | undefined }))
  const tierCatalog = data.customerTiers?.length
    ? data.customerTiers.filter(tier => !tier.archivedAt)
    : uniqueStrings((data.customers ?? []).map(customer => customer.tier)).map(tier => ({ id: tier, name: tier, color: undefined as string | undefined }))
  return [
    { id: 'customer-name', label: 'Customer name', kind: 'customerNameCategory', children: [
      { id: 'customer:', label: 'Unknown customer', kind: 'customerName', filterLabel: 'Customer name', count: count(row => row.hasUnknownCustomer) },
      ...(data.customers ?? []).map(customer => ({ id: `customer:${customer.id}`, label: customer.name, kind: 'customerName' as const, filterLabel: 'Customer name', count: count(row => row.customerIds.includes(customer.id)) })),
    ]},
    { id: 'customer-count', label: 'Customer count', kind: 'customerCountCategory', children: [
      { id: 'customer-count:0', label: 'No customers', kind: 'customerCountCategory', filterLabel: 'Customer count', count: count(row => !row.customerIds.length && !row.hasUnknownCustomer) },
      { id: 'customer-count:1', label: '1 customer', kind: 'customerCountCategory', filterLabel: 'Customer count', count: count(row => row.customerIds.length + (row.hasUnknownCustomer ? 1 : 0) === 1) },
      { id: 'customer-count:2+', label: '2+ customers', kind: 'customerCountCategory', filterLabel: 'Customer count', count: count(row => row.customerIds.length + (row.hasUnknownCustomer ? 1 : 0) >= 2) },
    ]},
    { id: 'customer-owner', label: 'Customer owner', kind: 'customerOwnerCategory', children: [
      { id: 'customer-owner:', label: 'No owner', kind: 'customerOwner', filterLabel: 'Customer owner', count: count(row => row.customerIds.length > 0 && !row.customerOwnerIds.some(Boolean)) },
      { id: `customer-owner:${data.viewer.id}`, label: 'Current user', kind: 'customerOwner', filterLabel: 'Customer owner', avatarUrl: data.viewer.avatarUrl, count: count(row => row.customerOwnerIds.includes(data.viewer.id)) },
      ...data.users.filter(user => user.active && user.id !== data.viewer.id).map(user => ({ id: `customer-owner:${user.id}`, label: user.displayName, kind: 'customerOwner' as const, filterLabel: 'Customer owner', avatarUrl: user.avatarUrl, count: count(row => row.customerOwnerIds.includes(user.id)) })),
    ]},
    { id: 'customer-status', label: 'Customer status', kind: 'customerStatusCategory', children: statusCatalog.map(status => ({ id: `customer-status:${status.id}`, label: status.name, color: status.color, kind: 'customerStatus' as const, filterLabel: 'Customer status', count: count(row => row.customerStatuses.includes(status.id) || row.customerStatuses.includes(status.name)) })) },
    { id: 'customer-tier', label: 'Customer tier', kind: 'customerTierCategory', children: tierCatalog.map(tier => ({ id: `customer-tier:${tier.id}`, label: tier.name, color: tier.color, kind: 'customerTier' as const, filterLabel: 'Customer tier', count: count(row => row.customerTiers.includes(tier.id) || row.customerTiers.includes(tier.name)) })) },
    { id: 'customer-revenue', label: 'Customer revenue', kind: 'customerRevenueCategory', children: [
      { id: 'customer-revenue:', label: 'No revenue', kind: 'customerRevenueCategory', filterLabel: 'Customer revenue', count: count(row => row.customerIds.length > 0 && !row.customerRevenues.some(amount => amount > 0)) },
      { id: 'customer-revenue:any', label: 'Has revenue', kind: 'customerRevenueCategory', filterLabel: 'Customer revenue', count: count(row => row.customerRevenues.some(amount => amount > 0)) },
    ]},
    { id: 'customer-size', label: 'Customer size', kind: 'customerSizeCategory', children: [
      { id: 'customer-size:', label: 'No size', kind: 'customerSizeCategory', filterLabel: 'Customer size', count: count(row => row.customerIds.length > 0 && !row.customerSizes.some(size => size > 0)) },
      { id: 'customer-size:any', label: 'Has size', kind: 'customerSizeCategory', filterLabel: 'Customer size', count: count(row => row.customerSizes.some(size => size > 0)) },
    ]},
  ]
}
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
function projectPropertyFilterOptions(data:BootstrapData,issues:Issue[]):MyIssuesFilterOption[]{const projects=data.projects,projectsById=new Map(projects.map(project=>[project.id,project])),statusCounts=new Map<string,number>(),statusTypeCounts=new Map<string,number>(),priorityCounts=new Map<string,number>(),labelCounts=new Map<string,number>(),leadCounts=new Map<string,number>();let noLead=0;for(const issue of issues){const project=projectsById.get(issue.project?.id??'');if(!project)continue;incrementCount(statusCounts,project.status.id);incrementCount(statusTypeCounts,project.status.type);incrementCount(priorityCounts,String(project.priority));for(const labelId of project.labelIds??[])incrementCount(labelCounts,labelId);if(project.lead)incrementCount(leadCounts,project.lead.id);else noLead+=1}return[
  {id:'project-status',label:'Project status',kind:'projectStatusCategory',children:data.projectStatuses.map(status=>({id:`project-status:${status.id}`,label:status.name,color:status.color,projectType:status.type,kind:'projectStatus',filterLabel:'Project status',count:statusCounts.get(status.id)??0}))},
  {id:'project-status-type',label:'Project status type',kind:'projectStatusTypeCategory',children:uniqueStrings(projects.map(project=>project.status.type)).map(type=>({id:`project-status-type:${type}`,label:projectStatusTypeLabel(type),projectType:type,kind:'projectStatusType',filterLabel:'Project status type',count:statusTypeCounts.get(type)??0}))},
  {id:'project-priority',label:'Project priority',kind:'projectPriorityCategory',children:['0','1','2','3','4'].map(value=>{const priority=Number(value) as 0|1|2|3|4;return{id:`project-priority:${value}`,label:['No priority','Urgent','High','Medium','Low'][priority],priority,kind:'projectPriority',filterLabel:'Project priority',count:priorityCounts.get(value)??0}})},
  {id:'project-labels',label:'Project labels',kind:'projectLabels',children:labelsForResource(data.labels,'project',data.labelGroups).map(label=>({id:`project-label:${label.id}`,label:label.name,color:label.color,kind:'projectLabels',filterLabel:'Project labels',count:labelCounts.get(label.id)??0}))},
  {id:'project-lead',label:'Project lead',kind:'projectLeadCategory',children:[{id:'project-lead:',label:'No lead',kind:'projectLead',filterLabel:'Project lead',count:noLead},{id:`project-lead:${data.viewer.id}`,label:'Current user',kind:'projectLead',filterLabel:'Project lead',avatarUrl:data.viewer.avatarUrl,count:leadCounts.get(data.viewer.id)??0},...data.users.filter(user=>user.active&&user.id!==data.viewer.id).map(user=>({id:`project-lead:${user.id}`,label:user.displayName,kind:'projectLead',filterLabel:'Project lead',avatarUrl:user.avatarUrl,count:leadCounts.get(user.id)??0}))]},
  {id:'project-milestone',label:'Project milestone name',kind:'projectMilestoneCategory',children:[{id:'project-milestone-name-contains',label:'Milestone name contains…',kind:'textCondition',filterLabel:'Project milestone name',operatorLabel:'contains',negativeOperatorLabel:'does not contain',textConditionPrefix:'project-milestone-name-contains:'}]},
]}
function addToSetMap(values:Map<string,Set<string>>,key:string|undefined,value:string){if(!key)return;const current=values.get(key)??new Set<string>();current.add(value);values.set(key,current)}
function projectStatusTypeLabel(type:string){return({backlog:'Backlog',planned:'Planned',started:'In Progress',completed:'Completed',canceled:'Canceled'} as Record<string,string>)[type]??type}

export function stateIdForExplorerGroup(group: MyIssuesGroupData, data: BootstrapData) {
  if (group.state?.id && data.states.some(state => state.id === group.state?.id)) return group.state.id
  if (group.id === 'other-active') return data.states.find(state => state.type === 'started')?.id
  if (group.stateType === 'backlog' || group.label === 'Backlog' || group.label === '待规划') return data.states.find(state => state.type === 'backlog' || state.id === 'state_backlog')?.id
  return data.states.find(state => state.id === group.id)?.id ?? data.states.find(state => state.name === group.label)?.id
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

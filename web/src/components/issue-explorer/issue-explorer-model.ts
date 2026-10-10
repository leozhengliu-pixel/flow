import { issuePath } from '@/lib/app-routes'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import type { MyIssuesBulkAction, MyIssuesBulkActionOption } from '@/components/my-issues/my-issues-bulk-action-bar'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-bar'
import type { MyIssuesContextAction, MyIssuesContextOption, MyIssuesEditableProperty, MyIssuesGroupData, MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import type { MyIssuesDisplayOptions, MyIssuesGrouping } from '@/components/my-issues/my-issues-surface'
import type { TeamIssuesRouteView } from '@/lib/app-routes'
import { defaultDateOperator, filterValues, isComparableDateValue, type AdvancedFilterGroup } from '@/components/my-issues/my-issues-filter-types'
import { compareDateFilter, DATE_FILTER_FIELDS, dateFilterMenu, parseDateFilterValue } from './issue-date-filter'
import { advancedFilterTree, conditionAsFilter, isAdvancedGroup } from './advanced-filter'
import { labelsForResource, setGroupedLabelSelected, toggleGroupedLabelIds } from '@/lib/labels'
import { milestoneIssueProgress } from '@/components/issue/milestone-progress'
import { buildIssueGroups, groupMoveUpdate, nestIssueRows } from './issue-grouping'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { AGENT_OPTION_GROUP, AGENT_SESSION_STATE_FILTERS, agentSessionStatesFor, assigneeCandidates, assigneeUpdate, isAssignableAgent } from '@/lib/agent-members'
import { CUSTOMER_NUMBER_PREFIXES, isCustomerNumberComparison, matchesCustomerNumberValues, matchesCustomerValues } from './customer-filter'
import { countTriageSuggestions, emptyTriageCounts, matchesTriageIntelligence, triageIntelligenceFilterOptions } from './triage-intelligence-filter'

export const ISSUE_FILTER_LABELS: Partial<Record<MyIssuesFilterKey, string>> = {
  ai:'AI filter',advanced:'Advanced filter',status:'Status',assignee:'Assignee',agent:'Agent',agentSession:'Agent Session',creator:'Creator',owner:'Owner',priority:'Priority',labels:'Labels',relations:'Relations',triageIntelligence:'Triage Intelligence',suggestedLabel:'Suggested label',dates:'Dates',projectMilestone:'Project milestone',project:'Project',projectProperties:'Project properties',initiative:'Initiative',cycle:'Cycle',addedToCycle:'Added to cycle',releases:'Releases',customers:'Customers',subscribers:'Subscribers',externalSource:'External source',autoClosed:'Auto-closed',content:'Content',links:'Links',template:'Template',
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
    agentSessionState:issue.agentSessionState,
    suggestedLabelIds:issue.suggestedLabelIds??[],
    suggestedAssigneeIds:issue.suggestedAssigneeIds??[],
    suggestedProjectIds:issue.suggestedProjectIds??[],
    suggestedTeamIds:issue.suggestedTeamIds??[],
    suggestedDuplicateIds:issue.suggestedDuplicateIds??[],
    suggestedRelatedIds:issue.suggestedRelatedIds??[],
    externalSource:issue.externalSource,
    autoClosed:issue.autoClosed,
    autoClosedAt:issue.autoClosedAt,
    triagedAt:issue.triagedAt,
    triage: Boolean(data?.teamSettings?.[issue.team.id]?.triageEnabled && issue.state.type === 'backlog' && !issue.triagedAt),
    templateId:issue.templateId,
    initiativeIds:fullProject?.initiatives??[],
    projectStatusId:fullProject?.status?.id,
    projectStatusType:fullProject?.status?.type,
    projectPriority:fullProject?.priority,
    projectLabelIds:fullProject?.labelIds??[],
    projectLeadId:fullProject?.lead?.id,
    projectMilestoneId: issue.projectMilestoneId,
    projectMilestoneNames:fullProject?.milestones?.map(milestone=>milestone.name)??[],
    milestoneName: fullProject?.milestones?.find(milestone => milestone.id === issue.projectMilestoneId)?.name,
    milestoneProgress: issue.projectMilestoneId && fullProject ? milestoneIssueProgress(data?.issues ?? issues, fullProject.id, issue.projectMilestoneId) : undefined,
    rawMilestoneDate: fullProject?.milestones?.find(milestone => milestone.id === issue.projectMilestoneId)?.targetDate,
    ...issueCustomerFields(issue.id, index),
    customerRevenueSettings: data?.workspaceSettings?.featureSettings,
    releaseIds:issueReleases.map(release=>release.id),releasePipelineIds:issueReleases.map(release=>release.pipelineId).filter((id):id is string=>Boolean(id)),releaseStages:issueReleases.map(release=>release.stage).filter((stage):stage is string=>Boolean(stage)),releaseStatuses:issueReleases.map(release=>release.status),hasReleasedRelease:issueReleases.some(release=>Boolean(release.releasedAt)),
    releaseCount: issueReleases.length,
    releases: issueReleases.map(release => ({ id: release.id, name: release.name, status: release.status, pipelineName: index?.pipelinesById.get(release.pipelineId ?? '')?.name })),
    sla: issueSla ? { ...issueSla, ruleName: slaRule?.name } : undefined,
    subscriberIds: issue.subscriberIds??[],
    relationTypes: issue.relations?.map(relation => relation.type)??[],
    hasLinks: (issue.attachments?.length??0) > 0,
    linkCount: issue.attachments?.length ?? 0,
    pullRequestCount: issuePullRequests.length,
    pullRequestLifecycle: resolveExplorerPullRequestLifecycle(issuePullRequests),
    blockedByCount: (issue.relations ?? []).filter(relation => relation.type === 'blocked_by').length,
    blockingCount: (issue.relations ?? []).filter(relation => relation.type === 'blocks').length,
    hasContent: Boolean(issue.title?.trim() || issue.description?.trim()),
    estimate: issue.estimate,
    dueDate: issue.dueDate,
    recurrence: issue.recurrence || undefined,
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


function resolveExplorerPullRequestLifecycle(
  reviews: Array<{ status: string; draft?: boolean }>,
): MyIssuesRowData['pullRequestLifecycle'] {
  if (!reviews.length) return undefined
  const statuses = reviews.map(review =>
    review.draft && review.status === 'open' ? 'draft' : review.status,
  ) as NonNullable<MyIssuesRowData['pullRequestLifecycle']>[]
  for (const status of ['merged', 'closed', 'approved', 'inReview', 'open', 'draft'] as const) {
    if (statuses.includes(status)) return status
  }
  return statuses[0]
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
    pipelinesById: new Map((data.releasePipelines ?? []).map(pipeline => [pipeline.id, pipeline])),
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
  const importantCustomersByIssueId = new Map<string, Set<string>>()
  for (const request of data.customerRequests ?? []) {
    if (!request.issueId || request.archivedAt) continue
    const customer = customersById.get(request.customerId)
    if (!customer) { unknownCustomerIssueIds.add(request.issueId); continue }
    const current = customersByIssueId.get(request.issueId) ?? []
    if (!current.some(item => item.id === customer.id)) current.push(customer)
    customersByIssueId.set(request.issueId, current)
    if (request.priority) {
      const important = importantCustomersByIssueId.get(request.issueId) ?? new Set<string>()
      important.add(customer.id)
      importantCustomersByIssueId.set(request.issueId, important)
    }
  }
  return { customersByIssueId, unknownCustomerIssueIds, importantCustomersByIssueId }
}

function issueCustomerFields(issueId: string, index?: ReturnType<typeof buildExplorerDataIndex>) {
  const customers = index?.customersByIssueId.get(issueId) ?? []
  const hasUnknownCustomer = index?.unknownCustomerIssueIds.has(issueId) ?? false
  return {
    customers: customers.map(customer => ({ id: customer.id, name: customer.name, logoUrl: customer.logoUrl, annualRevenue: customer.annualRevenue })),
    importantCustomerIds: [...(index?.importantCustomersByIssueId.get(issueId) ?? [])],
    customerCount: customers.length + (hasUnknownCustomer ? 1 : 0),
    customerIds: customers.map(customer => customer.id),
    customerNames: customers.map(customer => customer.name),
    hasUnknownCustomer,
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
  const agentSessionStateCounts = new Map<string, number>(), triageCounts = emptyTriageCounts()
  let noAssignee = 0, noAgent = 0, anyAgent = 0, noProject = 0, noInitiative = 0, noCycle = 0, noSubscribers = 0, noExternalSource = 0, autoClosed = 0, notAutoClosed = 0, noTemplate = 0, noSuggestedLabel = 0, withLinks = 0
  for (const issue of issues) {
    incrementCount(statusCounts, issue.state.id)
    incrementCount(priorityCounts, String(issue.priority))
    if (issue.assignee) incrementCount(assigneeCounts, issue.assignee.id); else noAssignee += 1
    incrementCount(creatorCounts, issue.creator.id)
    if (issue.delegate) { incrementCount(agentCounts, issue.delegate.id); anyAgent += 1 } else noAgent += 1
    if (issue.agentSessionId && issue.agentSessionState) incrementCount(agentSessionStateCounts, issue.agentSessionState)
    countTriageSuggestions(triageCounts, issue)
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
    // People, then the agents that accept delegation in their own section with the "Agent" pill (Linear).
    assignee: [{ id: '', label: 'No assignee', count: noAssignee, kind: 'assignee' as const }, ...assigneeCandidates(data.users.filter(user => user.active)).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, count: (user.app ? agentCounts : assigneeCounts).get(user.id) ?? 0, kind: 'assignee' as const, ...(user.app ? { agent: true, ...AGENT_OPTION_GROUP } : {}) }))],
    creator: data.users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, count: creatorCounts.get(user.id) ?? 0, kind: 'creator' as const })),
    // Every agent that can take work, plus agents still delegated on loaded issues.
    agent: [{ id: '', label: 'No agent', count: noAgent }, { id: '*', label: 'Any agent', count: anyAgent }, ...data.users.filter(user => user.app && (isAssignableAgent(user) || agentCounts.has(user.id))).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, count: agentCounts.get(user.id) ?? 0, kind: 'assignee', agent: true }))],
    // Linear offers exactly the four session states (no "No/Any agent session" rows).
    agentSession:AGENT_SESSION_STATE_FILTERS.map(option=>({id:option.id,label:option.label,count:option.states.reduce((sum,state)=>sum+(agentSessionStateCounts.get(state)??0),0)})),
    triageIntelligence: triageIntelligenceFilterOptions(data, issueLabels, triageCounts),
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
    customers: data.workspaceSettings?.featureFlags?.['customer-requests'] === false ? [] : customerFilterOptions(data),
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
  if (field === 'labels') return [{ id: '', label: 'No labels', kind: 'labels' as const }, ...options.labels]
  if (field === 'status'||field==='assignee'||field==='agent'||field==='agentSession'||field==='creator'||field==='priority'||field==='relations'||field==='triageIntelligence'||field==='suggestedLabel'||field==='dates'||field==='projectMilestone'||field==='project'||field==='projectProperties'||field==='initiative'||field==='cycle'||field==='addedToCycle'||field==='releases'||field==='customers'||field==='subscribers'||field==='externalSource'||field==='autoClosed'||field==='content'||field==='links'||field==='template') return options[field]
}

export function explorerBulkOptions(action: MyIssuesBulkAction, options: ExplorerPropertyOptions): MyIssuesBulkActionOption[] | undefined {
  if (action === 'status') return options.status
  if (action === 'priority') return options.priority
  if (action === 'assign') return options.assignee
  if (action === 'project') return options.project
  if (action === 'labels') return options.labels
  if (action === 'dueDate') return explorerDueDateOptions()
  if (action === 'subscribers') return options.assignee.filter(option => option.id && !('agent' in option && option.agent))
}

export async function executeExplorerBulkAction({ action, ids, value, data, issuesById, onUpdateIssue, onUpdateIssues, onDeleteIssues }: {
  action: MyIssuesBulkAction
  ids: string[]
  value?: string
  data: BootstrapData
  issuesById: Map<string, Issue>
  onUpdateIssue: (id: string, input: IssueUpdateInput) => Promise<Issue>
  onUpdateIssues: (ids: string[], input: IssueUpdateInput) => Promise<Issue[]>
  onDeleteIssues?: (ids: string[]) => Promise<void>
}): Promise<Issue[] | void> {
  if (action === 'archive') return onUpdateIssues(ids, { archived: true })
  if (action === 'delete') {
    if (onDeleteIssues && await confirmAction(ids.length === 1 ? `Delete ${issuesById.get(ids[0])?.identifier ?? 'issue'}?` : `Delete ${ids.length} issues?`, { description: 'This cannot be undone.', confirmLabel: 'Delete' })) await onDeleteIssues(ids)
    return
  }
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
  if (action === 'assign' || action === 'assignee') return assigneeUpdate(value)
  if (action === 'project') return { projectId: value }
  if (action === 'dueDate') return { dueDate: value }
}

export function explorerUpdateForProperty(property: MyIssuesEditableProperty, value: string | string[]): IssueUpdateInput | undefined {
  if (property === 'labels' && Array.isArray(value)) return { labelIds: value }
  if (Array.isArray(value)) return
  if (property === 'status') return { stateId: value }
  if (property === 'priority') return { priority: Number(value) }
  if (property === 'assignee') return assigneeUpdate(value)
  if (property === 'project') return { projectId: value }
  if (property === 'dueDate') return { dueDate: value }
  if (property === 'cycle') return { cycleId: value }
}

/** Build the persisted property change when a card is moved between board groups. */
export function explorerBoardGroupUpdate(row: MyIssuesRowData, grouping: MyIssuesGrouping, targetGroupId: string, data: BootstrapData, labelGroupId?: string): IssueUpdateInput {
  const groupId = targetGroupId.split('::')[0]
  if (grouping === 'label' && groupId.startsWith('label-') && groupId !== 'label-none') {
    const labelId = groupId.slice('label-'.length)
    const current = (row.labels ?? []).map(label => label.id)
    return current.includes(labelId) ? {} : { labelIds: toggleGroupedLabelIds(current, labelId, data.labels) }
  }
  return groupMoveUpdate(row, grouping, groupId, { data, labelGroupId }) ?? {}
}

export function optimisticExplorerRow(row: MyIssuesRowData, input: IssueUpdateInput, data: BootstrapData): MyIssuesRowData {
  return {
    ...row,
    state: input.stateId === undefined ? row.state : data.states.find(state => state.id === input.stateId) ?? row.state,
    priority: input.priority === undefined ? row.priority : clampPriority(input.priority),
    assignee: input.assigneeId === undefined ? row.assignee : input.assigneeId ? (() => { const user = data.users.find(item => item.id === input.assigneeId); return user ? { id: user.id, name: user.displayName, avatarUrl: user.avatarUrl } : row.assignee })() : undefined,
    delegate: input.delegateId === undefined ? row.delegate : input.delegateId ? (() => { const user = data.users.find(item => item.id === input.delegateId); return user ? { id: user.id, name: user.displayName, avatarUrl: user.avatarUrl } : row.delegate })() : undefined,
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

export function matchesExplorerFilter(issue: MyIssuesRowData, filter: MyIssuesAppliedFilter, now = Date.now()): boolean {
  if (filter.field === 'advanced') return matchesAdvancedGroup(issue, advancedFilterTree(filter), now)
  const values = filterValues(filter).map(value => value.value)
  // "include all of" / "exclude if all": every value must match on its own.
  if (filter.operator === 'includesAll' || filter.operator === 'excludesAll') {
    const all = values.length > 0 && values.every(value => matchesExplorerFilter(issue, { ...filter, operator: 'is', value, values: [{ value, valueLabel: value }] }, now))
    return filter.operator === 'includesAll' ? all : !all
  }
  const comparison = filter.operator === 'before' || filter.operator === 'after' ? filter.operator : undefined
  let matched = true
  if (filter.field === 'priority') matched = values.includes(String(issue.priority))
  else if (filter.field === 'status') matched = values.includes(issue.state.id) || values.includes(issue.state.type)
  // An agent picked under Assignee matches the issues delegated to it (agents are never assignees).
  else if (filter.field === 'assignee') matched = values.includes(issue.assignee?.id ?? '') || Boolean(issue.delegate && values.includes(issue.delegate.id))
  else if (filter.field === 'agent') matched = values.includes('*') ? Boolean(issue.delegate) : values.includes(issue.delegate?.id ?? '')
  else if (filter.field === 'agentSession') matched = matchesAgentSessionFilter(issue, values)
  else if (filter.field === 'triageIntelligence') matched = values.some(value => matchesTriageIntelligence(issue, value))
  else if (filter.field === 'creator') matched = values.includes(issue.creatorId ?? '')
  else if (filter.field === 'labels') matched = (values.includes('') && !issue.labels?.length) || Boolean(issue.labels?.some(label => values.includes(label.id)))
  else if (filter.field === 'suggestedLabel') matched = values.includes('') ? !issue.suggestedLabelIds?.length : Boolean(issue.suggestedLabelIds?.some(id => values.includes(id)))
  else if (filter.field === 'project') matched = values.includes(issue.project?.id ?? '')
  else if (filter.field === 'projectMilestone') matched = values.includes(issue.projectMilestoneId ?? '')
  else if (filter.field === 'projectProperties') matched = matchesProjectProperties(issue, values)
  else if (filter.field === 'initiative') matched = values.includes('') ? !issue.initiativeIds?.length : Boolean(issue.initiativeIds?.some(id => values.includes(id)))
  else if (filter.field === 'cycle') matched = values.includes(issue.cycleId ?? '')
  else if (filter.field === 'addedToCycle') matched = values.includes(issue.addedToCycle ?? '')
  else if (filter.field === 'releases') matched = matchesReleaseFilter(issue, values)
  else if (filter.field === 'customers') {
    // Number filters compare with the chip's operator (Customer count ≥ 3).
    if (isCustomerNumberComparison(filter.operator)) return matchesCustomerNumberValues(issue, values, filter.operator)
    matched = matchesCustomerValues(issue, values)
  }
  else if (filter.field === 'dates') matched = values.some(value => matchesDateFilter(issue, value, comparison, now))
  else if (filter.field === 'subscribers') matched = values.includes('') ? !issue.subscriberIds?.length : Boolean(issue.subscriberIds?.some(id => values.includes(id)))
  else if (filter.field === 'relations') matched = values.includes('') ? !issue.relationTypes?.length : Boolean(issue.relationTypes?.some(type => values.includes(type)))
  else if (filter.field === 'links') matched = values.includes(issue.hasLinks ? 'has-links' : 'no-links')
  else if (filter.field === 'content') matched = values.some(value => value.startsWith('query:') && `${issue.title} ${issue.description ?? ''}`.toLocaleLowerCase().includes(value.slice(6).toLocaleLowerCase()))
  else if (filter.field === 'externalSource') matched = values.includes(issue.externalSource ?? '')
  else if (filter.field === 'autoClosed') matched = values.includes(String(Boolean(issue.autoClosed)))
  else if (filter.field === 'template') matched = values.includes(issue.templateId ?? '')
  else if (filter.field === 'ai') matched = matchesAIFilter(issue, values)
  return filter.operator === 'isNot' ? !matched : matched
}

/** AND / OR over the tree; empty groups and value-less conditions do not constrain. */
export function matchesAdvancedGroup(issue: MyIssuesRowData, group: AdvancedFilterGroup, now = Date.now()): boolean {
  const results = group.items.flatMap(item => isAdvancedGroup(item)
    ? (item.items.length ? [matchesAdvancedGroup(issue, item, now)] : [])
    : (item.values.length ? [matchesExplorerFilter(issue, conditionAsFilter(item), now)] : []))
  if (!results.length) return true
  return group.conjunction === 'or' ? results.some(Boolean) : results.every(Boolean)
}

function matchesProjectProperties(issue: MyIssuesRowData, values: string[]) {
  return values.some(value => value.startsWith('project-status:') ? issue.projectStatusId === value.slice(15) : value.startsWith('project-status-type:') ? issue.projectStatusType === value.slice(20) : value.startsWith('project-priority:') ? String(issue.projectPriority) === value.slice(17) : value.startsWith('project-label:') ? issue.projectLabelIds?.includes(value.slice(14)) : value === 'project-lead:' ? !issue.projectLeadId : value.startsWith('project-lead:') ? issue.projectLeadId === value.slice(13) : value.startsWith('project-milestone-name-contains:') ? issue.projectMilestoneNames?.some(name => name.toLocaleLowerCase().includes(value.slice(32).toLocaleLowerCase())) : false)
}
function matchesReleaseFilter(issue: MyIssuesRowData, values: string[]) {
  return values.some(value => value === 'no-releases' ? !issue.releaseIds?.length : value === 'released-any' ? Boolean(issue.hasReleasedRelease) : value.startsWith('release:') ? issue.releaseIds?.includes(value.slice(8)) : value.startsWith('release-pipeline:') ? issue.releasePipelineIds?.includes(value.slice(17)) : value.startsWith('release-stage:') ? issue.releaseStages?.includes(value.slice(14)) : value.startsWith('release-stage-type:') ? issue.releaseStatuses?.includes(value.slice(19)) : false)
}
function matchesDateFilter(issue: MyIssuesRowData, value: string, comparison?: 'before' | 'after', now = Date.now()) {
  const parsed = parseDateFilterValue(value)
  if (parsed && isComparableDateValue(value)) return compareDateFilter(issue[DATE_FILTER_FIELDS[parsed.kind]], parsed, comparison ?? defaultDateOperator(value), now)
  const age = (input: string | undefined, days: number) => Boolean(input && Date.parse(input) >= now - days * 86_400_000)
  if (value === 'created-past-day' || value === 'created-past-week' || value === 'created-past-month') return age(issue.createdAt, value.endsWith('day') ? 1 : value.endsWith('week') ? 7 : 30)
  if (value === 'updated-past-day' || value === 'updated-past-week' || value === 'updated-past-month') return age(issue.updatedAt, value.endsWith('day') ? 1 : value.endsWith('week') ? 7 : 30)
  if (value === 'started-any') return Boolean(issue.startedAt)
  if (value === 'completed-any') return Boolean(issue.completedAt)
  if (value === 'auto-closed-any') return Boolean(issue.autoClosed)
  if (value === 'triaged-any') return Boolean(issue.triagedAt)
  if (value === 'status-over-week') return Boolean(issue.statusChangedAt && Date.parse(issue.statusChangedAt) < now - 7 * 86_400_000)
  if (value === 'has-due-date') return Boolean(issue.dueDate)
  if (value === 'no-due-date') return !issue.dueDate
  if (!issue.dueDate) return false
  const due = Date.parse(`${issue.dueDate.slice(0, 10)}T00:00:00`); const today = new Date(now); today.setHours(0, 0, 0, 0)
  if (value === 'overdue') return due < today.getTime()
  if (value === 'today') return due === today.getTime()
  if (value === 'next-week') return due >= today.getTime() && due <= today.getTime() + 7 * 86_400_000
  return false
}
function matchesAIFilter(issue: MyIssuesRowData, values: string[]) {
  return values.some(value => value === 'assigned-to-me' ? Boolean(issue.isAssignedToViewer) : value === 'completed-last-month' ? Boolean(issue.completedAt && Date.parse(issue.completedAt) >= Date.now() - 30 * 86_400_000) : value === 'due-next-two-weeks' ? Boolean(issue.dueDate && Date.parse(`${issue.dueDate.slice(0, 10)}T00:00:00`) <= Date.now() + 14 * 86_400_000) : value.startsWith('query:') ? `${issue.title} ${issue.description ?? ''}`.toLocaleLowerCase().includes(value.slice(6).toLocaleLowerCase()) : false)
}
export function buildExplorerIssueGroups(issues: MyIssuesRowData[], display: MyIssuesDisplayOptions, data: BootstrapData, view: TeamIssuesRouteView = 'all', manualOrder: string[] = []): MyIssuesGroupData[] {
  // Active / Backlog tabs already exclude closed issues; the completed window only applies to "All".
  const scoped = view === 'all' ? issues : issues.filter(issue => issue.state.type !== 'completed' && issue.state.type !== 'canceled')
  const states = data.states.filter(state => stateVisibleInView(state.type, view, display.completedWindow))
  return buildIssueGroups(scoped, display, { data, manualOrder, states, skipCompletedWindow: view !== 'all' })
}

export const nestedIssueProjection = nestIssueRows

function stateVisibleInView(type: string, view: TeamIssuesRouteView, completedWindow: MyIssuesDisplayOptions['completedWindow']) {
  if (view === 'active') return type === 'unstarted' || type === 'started'
  if (view === 'backlog') return type === 'backlog'
  return completedWindow !== 'none' || (type !== 'completed' && type !== 'canceled')
}

export function explorerDueDateOptions(): MyIssuesBulkActionOption[] {
  const date = new Date(), day = 86_400_000
  return [{ id: '', label: 'No due date' }, { id: isoDate(date), label: 'Today' }, { id: isoDate(new Date(date.getTime() + day)), label: 'Tomorrow' }, { id: isoDate(new Date(date.getTime() + day * 7)), label: 'In one week' }]
}

function dateFilterCategories(_issues: Issue[]): MyIssuesFilterOption[] { return dateFilterMenu() }
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
/**
 * Linear's Customers filter sub-menu: Customer name, Customer count, Important customer count,
 * Customer owner, Customer status, Customer tier, Customer revenue, Customer size. Every block hides
 * its match count; the four number blocks open a number input.
 */
export function customerFilterOptions(data: Pick<BootstrapData, 'viewer' | 'users'> & Partial<Pick<BootstrapData, 'customers' | 'customerStatuses' | 'customerTiers' | 'workspaceSettings'>>): MyIssuesFilterOption[] {
  const statusCatalog = data.customerStatuses?.length
    ? [...data.customerStatuses].filter(status => !status.archivedAt).sort((a, b) => (a.position ?? 0) - (b.position ?? 0))
    : uniqueStrings((data.customers ?? []).map(customer => customer.status)).map(status => ({ id: status, name: status, color: undefined as string | undefined }))
  const tierCatalog = data.customerTiers?.length
    ? [...data.customerTiers].filter(tier => !tier.archivedAt).sort((a, b) => (a.position ?? 0) - (b.position ?? 0))
    : uniqueStrings((data.customers ?? []).map(customer => customer.tier)).map(tier => ({ id: tier, name: tier, color: undefined as string | undefined }))
  const settings = data.workspaceSettings?.featureSettings
  const revenue = { currency: /^[A-Z]{3}$/.test(settings?.customerRevenueCurrency ?? '') ? settings!.customerRevenueCurrency : 'USD', monthly: settings?.customerRevenueFormat === 'monthly' }
  const customers = [...(data.customers ?? [])].sort((a, b) => a.name.localeCompare(b.name))
  const hidden = { hideCount: true } as const
  return [
    { id: 'customer-name', label: 'Customer name', kind: 'customerNameCategory', children: [
      { id: 'customer:', label: 'Unknown customer', kind: 'customerName', filterLabel: 'Customer name', ...hidden },
      ...customers.map(customer => ({ id: `customer:${customer.id}`, label: customer.name, kind: 'customerName' as const, filterLabel: 'Customer name', avatarUrl: customer.logoUrl, ...hidden })),
    ]},
    { id: 'customer-count', label: 'Customer count', kind: 'customerCountCategory', numberInput: { prefix: CUSTOMER_NUMBER_PREFIXES.count, placeholder: 'Enter customer count…' } },
    { id: 'customer-important-count', label: 'Important customer count', kind: 'customerImportantCountCategory', numberInput: { prefix: CUSTOMER_NUMBER_PREFIXES.importantCount, placeholder: 'Enter important customer count…' } },
    { id: 'customer-owner', label: 'Customer owner', kind: 'customerOwnerCategory', children: [
      { id: 'customer-owner:', label: 'No owner', kind: 'customerOwner', filterLabel: 'Customer owner', ...hidden },
      { id: `customer-owner:${data.viewer.id}`, label: 'Current user', kind: 'customerOwner', filterLabel: 'Customer owner', avatarUrl: data.viewer.avatarUrl, ...hidden },
      ...data.users.filter(user => user.active && user.id !== data.viewer.id && !user.app).map(user => ({ id: `customer-owner:${user.id}`, label: user.displayName, kind: 'customerOwner' as const, filterLabel: 'Customer owner', avatarUrl: user.avatarUrl, ...hidden })),
    ]},
    { id: 'customer-status', label: 'Customer status', kind: 'customerStatusCategory', children: statusCatalog.map(status => ({ id: `customer-status:${status.id}`, label: status.name, color: status.color, kind: 'customerStatus' as const, filterLabel: 'Customer status', ...hidden })) },
    { id: 'customer-tier', label: 'Customer tier', kind: 'customerTierCategory', children: tierCatalog.map(tier => ({ id: `customer-tier:${tier.id}`, label: tier.name, kind: 'customerTier' as const, filterLabel: 'Customer tier', ...hidden })) },
    { id: 'customer-revenue', label: 'Customer revenue', kind: 'customerRevenueCategory', numberInput: { prefix: CUSTOMER_NUMBER_PREFIXES.revenue, placeholder: 'Enter customer revenue…', revenue } },
    { id: 'customer-size', label: 'Customer size', kind: 'customerSizeCategory', numberInput: { prefix: CUSTOMER_NUMBER_PREFIXES.size, placeholder: 'Enter customer size…' } },
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

/**
 * Agent Session filter: `state:<id>` is Linear's Active, Error, Dismissed or Merged (legacy saved state values map
 * onto them). "" (none) and "*" (any) are no longer offered but still match for saved views.
 */
export function matchesAgentSessionFilter(issue: Pick<MyIssuesRowData, 'agentSessionId' | 'agentSessionState'>, values: string[]) {
  return values.some(value => value === '*' ? Boolean(issue.agentSessionId)
    : value === '' ? !issue.agentSessionId
      : value.startsWith('state:') ? Boolean(issue.agentSessionId && issue.agentSessionState && agentSessionStatesFor(value).includes(issue.agentSessionState as never))
        : issue.agentSessionId === value)
}

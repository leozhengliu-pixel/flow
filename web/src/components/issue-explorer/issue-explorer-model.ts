import { issuePath } from '@/lib/app-routes'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import type { MyIssuesBulkAction, MyIssuesBulkActionOption } from '@/components/my-issues/my-issues-bulk-action-bar'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-bar'
import type { MyIssuesContextAction, MyIssuesContextOption, MyIssuesEditableProperty, MyIssuesGroupData, MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import { filterValues } from '@/components/my-issues/my-issues-filter-types'
import { labelsForResource } from '@/lib/labels'

export const ISSUE_FILTER_LABELS: Partial<Record<MyIssuesFilterKey, string>> = {
  status: 'Status', assignee: 'Assignee', creator: 'Creator', priority: 'Priority', labels: 'Labels', project: 'Project',
  dates: 'Dates', subscribers: 'Subscribers', relations: 'Relations', links: 'Links',
}

const PRIORITIES: MyIssuesContextOption[] = ['No priority', 'Urgent', 'High', 'Medium', 'Low'].map((label, id) => ({
  id: String(id), label, kind: 'priority', priority: id as 0 | 1 | 2 | 3 | 4,
}))

export function issueToExplorerRow(issue: Issue, workspaceSlug: string): MyIssuesRowData {
  return {
    id: issue.id,
    identifier: issue.identifier,
    title: issue.title,
    href: issuePath(workspaceSlug, issue),
    priority: clampPriority(issue.priority),
    state: issue.state,
    labels: issue.labels,
    project: issue.project,
    assignee: issue.assignee ? { id: issue.assignee.id, name: issue.assignee.displayName, avatarUrl: issue.assignee.avatarUrl } : undefined,
    estimate: issue.estimate,
    dueDate: issue.dueDate,
    createdAt: issue.createdAt,
    updatedAt: issue.updatedAt,
    parentId: issue.parentId,
    sortOrder: issue.sortOrder,
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
    dueDate: explorerDueDateOptions().map(option => ({ ...option, kind: 'dueDate' as const })),
    dates: issueDateFilterOptions(issues),
    labels: issueLabels.map(label => ({ id: label.id, label: label.name, color: label.color, description: label.description, issueCount: label.issueCount, scope: label.scope, groupId: label.groupId, groupLabel: label.groupId ? labelGroupNames.get(label.groupId) : undefined, count: count(issue => issue.labels.some(item => item.id === label.id)), kind: 'labels' as const })),
    project: [{ id: '', label: 'No project', count: count(issue => !issue.project), kind: 'project' as const }, ...data.projects.map(project => ({ id: project.id, label: project.name, color: project.color, count: count(issue => issue.project?.id === project.id), kind: 'project' as const }))],
    subscribers: [{ id: '', label: 'No subscribers', count: count(issue => !issue.subscriberIds.length), kind: 'subscribers' as const }, ...data.users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, count: count(issue => issue.subscriberIds.includes(user.id)), kind: 'subscribers' as const }))],
    relations: [
      { id: '', label: 'No relations', count: count(issue => !issue.relations.length), kind: 'relations' as const },
      ...(['related', 'blocks', 'blocked_by', 'duplicate', 'parent_of', 'sub_issue_of'] as const).map(type => ({ id: type, label: relationLabel(type), count: count(issue => issue.relations.some(relation => relation.type === type)), kind: 'relations' as const })),
    ],
    links: [{ id: 'has-links', label: 'Has links', count: count(issue => issue.attachments.length > 0), kind: 'links' as const }, { id: 'no-links', label: 'No links', count: count(issue => issue.attachments.length === 0), kind: 'links' as const }],
  }
}

export type ExplorerPropertyOptions = ReturnType<typeof explorerPropertyOptions>

export function explorerFilterOptions(field: MyIssuesFilterKey, options: ExplorerPropertyOptions): MyIssuesFilterOption[] | undefined {
  if (field === 'status' || field === 'assignee' || field === 'creator' || field === 'priority' || field === 'labels' || field === 'project' || field === 'dates' || field === 'subscribers' || field === 'relations' || field === 'links') return options[field]
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

export function applyExplorerFilters(issues: Issue[], filters: MyIssuesAppliedFilter[]) {
  return issues.filter(issue => filters.every(filter => {
    const values = filterValues(filter).map(value => value.value)
    let matched = true
    if (filter.field === 'priority') matched = values.includes(String(issue.priority))
    else if (filter.field === 'status') matched = values.includes(issue.state.id) || values.includes(issue.state.type)
    else if (filter.field === 'assignee') matched = values.includes(issue.assignee?.id ?? '')
    else if (filter.field === 'creator') matched = values.includes(issue.creator.id)
    else if (filter.field === 'labels') matched = issue.labels.some(label => values.includes(label.id))
    else if (filter.field === 'project') matched = values.includes(issue.project?.id ?? '')
    else if (filter.field === 'dates') matched = values.some(value => issueMatchesDateFilter(issue, value))
    else if (filter.field === 'subscribers') matched = values.includes('') ? !issue.subscriberIds.length : issue.subscriberIds.some(id => values.includes(id))
    else if (filter.field === 'relations') matched = values.includes('') ? !issue.relations.length : issue.relations.some(relation => values.includes(relation.type))
    else if (filter.field === 'links') matched = values.includes(issue.attachments.length ? 'has-links' : 'no-links')
    return filter.operator === 'is' ? matched : !matched
  }))
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
  return definitions.map(option => ({ ...option, count: issues.filter(issue => issueMatchesDateFilter(issue, option.id)).length }))
}

function issueMatchesDateFilter(issue: Issue, value: string) {
  if (value === 'has-due-date') return Boolean(issue.dueDate)
  if (value === 'no-due-date') return !issue.dueDate
  if (!issue.dueDate) return false
  const due = new Date(`${issue.dueDate.slice(0, 10)}T00:00:00`).getTime()
  const today = new Date(); today.setHours(0, 0, 0, 0)
  if (value === 'overdue') return due < today.getTime()
  if (value === 'today') return due === today.getTime()
  if (value === 'next-week') return due >= today.getTime() && due <= today.getTime() + 7 * 86_400_000
  return false
}

function relationLabel(type: Issue['relations'][number]['type']) {
  return ({ related: 'Related', blocks: 'Blocks', blocked_by: 'Blocked by', duplicate: 'Duplicate', parent_of: 'Parent of', sub_issue_of: 'Sub-issue of' })[type]
}

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

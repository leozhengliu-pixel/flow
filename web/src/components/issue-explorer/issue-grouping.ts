import type { BootstrapData, IssueUpdateInput } from '@/types/flow'
import type { MyIssuesGroupData, MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { MyIssuesDisplayOptions, MyIssuesGrouping, MyIssuesOrdering } from '@/components/my-issues/my-issues-surface'

/**
 * Single grouping / ordering engine for every issue surface (Linear `ViewPreferences` + list grouping).
 * Surfaces pass rows and display options; they never build groups themselves.
 */

export const PRIORITY_LABELS = ['No priority', 'Urgent', 'High', 'Medium', 'Low'] as const
/** Linear order: Urgent → Low, then No priority. */
const PRIORITY_RANK = [4, 0, 1, 2, 3]
const STATE_TYPE_RANK: Record<string, number> = { triage: 0, backlog: 1, unstarted: 2, started: 3, completed: 4, canceled: 5 }
const COMPLETED_TYPES = new Set(['completed', 'canceled'])
const DAY = 86_400_000

export interface IssueGroupingContext {
  /** Workspace data when available; grouping degrades to row-only information without it. */
  data?: Pick<BootstrapData, 'states' | 'cycles' | 'projects' | 'users' | 'teams' | 'releases'> & Partial<Pick<BootstrapData, 'customers'>>
  /** States a status grouping should always show (for empty groups / board columns). */
  states?: MyIssuesRowData['state'][]
  /** Manual order override (drag-and-drop) keyed by issue id. */
  manualOrder?: string[]
  now?: number
}

export type GroupDescriptor = Omit<MyIssuesGroupData, 'issues'> & { rank: number | string }

export const GROUPING_LABELS: Record<MyIssuesGrouping, string> = {
  none: 'No grouping', focus: 'Focus', status: 'Status', assignee: 'Assignee', agent: 'Agent', project: 'Project',
  milestone: 'Milestone', priority: 'Priority', cycle: 'Cycle', label: 'Label', team: 'Team', customer: 'Customer',
  parent: 'Parent issue', sla: 'SLA status', release: 'Release', activityDate: 'Activity date',
}

export const ORDERING_LABELS: Record<MyIssuesOrdering, string> = {
  importance: 'Manual', title: 'Title', status: 'Status', assignee: 'Assignee', priority: 'Priority', estimate: 'Estimate',
  created: 'Created', updated: 'Updated', myActivity: 'My activity date', dueDate: 'Due date', linkCount: 'Link count',
  customerCount: 'Customer count', customerRevenue: 'Customer revenue', timeInStatus: 'Time in status',
}

/** The direction each ordering uses when the view has not chosen one (Linear defaults). */
export function defaultOrderDirection(ordering: MyIssuesOrdering): 'asc' | 'desc' {
  return ordering === 'created' || ordering === 'updated' || ordering === 'myActivity' || ordering === 'estimate' || ordering === 'linkCount' || ordering === 'customerCount' || ordering === 'customerRevenue' || ordering === 'timeInStatus' ? 'desc' : 'asc'
}

export function isCompleted(row: Pick<MyIssuesRowData, 'state'>) { return COMPLETED_TYPES.has(row.state.type) }

/** Every group an issue belongs to. Label, customer and release groupings are multi-membership like Linear. */
export function groupDescriptors(row: MyIssuesRowData, grouping: MyIssuesGrouping, context: IssueGroupingContext = {}): GroupDescriptor[] {
  const data = context.data
  switch (grouping) {
    case 'none': return [{ id: 'all-issues', label: 'All issues', rank: 0 }]
    case 'status': return [statusDescriptor(row.state, context)]
    case 'focus': return [focusDescriptor(row, context)]
    case 'priority': return [{ id: `priority-${row.priority}`, label: PRIORITY_LABELS[row.priority], rank: PRIORITY_RANK[row.priority], createContext: { priority: row.priority } }]
    case 'assignee': return [row.assignee
      ? { id: `assignee-${row.assignee.id}`, label: row.assignee.name, rank: `0${row.assignee.name.toLocaleLowerCase()}`, createContext: { assigneeId: row.assignee.id } }
      : { id: 'assignee-none', label: 'No assignee', rank: '1', createContext: { assigneeId: '' } }]
    case 'agent': return [row.delegate
      ? { id: `agent-${row.delegate.id}`, label: row.delegate.name, rank: `0${row.delegate.name.toLocaleLowerCase()}` }
      : { id: 'agent-none', label: 'No agent', rank: '1' }]
    case 'project': return [row.project
      ? { id: `project-${row.project.id}`, label: row.project.name, rank: `0${row.project.name.toLocaleLowerCase()}`, createContext: { projectId: row.project.id } }
      : { id: 'project-none', label: 'No project', rank: '1', createContext: { projectId: '' } }]
    case 'milestone': {
      const project = row.project ? data?.projects.find(item => item.id === row.project!.id) : undefined
      const milestones = project?.milestones ?? []
      const index = milestones.findIndex(item => item.id === row.projectMilestoneId)
      if (index < 0) return [{ id: 'milestone-none', label: 'No milestone', rank: 'z', createContext: { projectMilestoneId: '' } }]
      const milestone = milestones[index]
      return [{ id: `milestone-${milestone.id}`, label: milestone.name, rank: `${milestone.targetDate ?? '9999'}${String(index).padStart(4, '0')}`, createContext: { projectId: row.project?.id, projectMilestoneId: milestone.id } }]
    }
    case 'cycle': {
      const cycle = row.cycleId ? data?.cycles.find(item => item.id === row.cycleId) : undefined
      if (!row.cycleId) return [{ id: 'cycle-none', label: 'No cycle', rank: 'z', createContext: { cycleId: '' } }]
      return [{ id: `cycle-${row.cycleId}`, label: cycle?.name ?? row.cycleName ?? 'Cycle', rank: cycle?.startsAt ?? row.cycleName ?? '', createContext: { cycleId: row.cycleId } }]
    }
    case 'label': {
      if (!row.labels?.length) return [{ id: 'label-none', label: 'No label', rank: '1', createContext: { labelIds: [] } }]
      return row.labels.map(label => ({ id: `label-${label.id}`, label: label.name, rank: `0${label.name.toLocaleLowerCase()}`, createContext: { labelIds: [label.id] } }))
    }
    case 'team': return [{ id: `team-${row.teamId ?? 'none'}`, label: row.teamName ?? 'No team', rank: (row.teamName ?? '~').toLocaleLowerCase(), createContext: row.teamId ? { teamId: row.teamId } : undefined }]
    case 'customer': {
      const ids = row.customerIds ?? []
      if (!ids.length) return [{ id: 'customer-none', label: 'No customer', rank: '1' }]
      return ids.map((id, index) => { const name = row.customerNames?.[index] ?? 'Customer'; return { id: `customer-${id}`, label: name, rank: `0${name.toLocaleLowerCase()}` } })
    }
    case 'parent': {
      const root = row.ancestors?.[0] ?? row.parent
      if (!root) return [{ id: 'parent-none', label: 'No parent issue', rank: '1' }]
      return [{ id: `parent-${root.id}`, label: `${root.identifier} ${root.title}`, rank: `0${root.identifier}` }]
    }
    case 'sla': return [slaDescriptor(row, context.now ?? Date.now())]
    case 'release': {
      const ids = row.releaseIds ?? []
      if (!ids.length) return [{ id: 'release-none', label: 'No release', rank: 'z' }]
      return ids.map(id => { const release = data?.releases?.find(item => item.id === id); return { id: `release-${id}`, label: release?.name ?? 'Release', rank: release?.releasedAt ?? release?.targetDate ?? release?.name ?? id } })
    }
    case 'activityDate': return [activityDescriptor(row.myActivityAt ?? row.updatedAt, context.now ?? Date.now())]
  }
}

function statusDescriptor(state: MyIssuesRowData['state'], context: IssueGroupingContext): GroupDescriptor {
  const order = (context.states ?? context.data?.states ?? []).findIndex(item => item.id === state.id)
  return { id: state.id, label: state.name, stateType: state.type, state, rank: (STATE_TYPE_RANK[state.type] ?? 9) * 1000 + (order < 0 ? 999 : order), createContext: { stateId: state.id } }
}

/**
 * Linear "Focus" buckets (IssueListGroupRow): agents waiting, urgent, SLA risk, current cycle,
 * blocking, other active, backlog, triage, completed, canceled.
 */
export function focusDescriptor(row: MyIssuesRowData, context: IssueGroupingContext = {}): GroupDescriptor {
  const now = context.now ?? Date.now()
  const type = row.state.type
  if (type === 'completed') return { id: 'focus-completed', label: 'Completed', stateType: 'completed', rank: 90 }
  if (type === 'canceled') return { id: 'focus-canceled', label: 'Canceled', stateType: 'canceled', rank: 91 }
  if (isTriage(row)) return { id: 'focus-triage', label: 'Triage', rank: 80 }
  if (row.delegate && row.agentSessionId && type !== 'backlog') return { id: 'focus-agents', label: 'Agents waiting for input', rank: 0 }
  if (row.priority === 1) return { id: 'focus-urgent', label: 'Urgent issues', rank: 10, createContext: { priority: 1 } }
  const sla = slaDescriptor(row, now)
  if (sla.id !== 'sla-none' && sla.id !== 'sla-met') return { ...sla, id: `focus-${sla.id}`, rank: 20 + Number(sla.rank) }
  const cycle = row.cycleId ? context.data?.cycles.find(item => item.id === row.cycleId) : undefined
  if (cycle && cycle.status === 'current' && type !== 'backlog') return { id: `focus-cycle-${cycle.id}`, label: cycle.name, rank: 30, createContext: { cycleId: cycle.id } }
  if ((row.blockingCount ?? 0) > 0) return { id: 'focus-blocking', label: 'Blocking issues', rank: 40 }
  if (type === 'backlog') return { id: 'focus-backlog', label: 'Backlog', stateType: 'backlog', rank: 70 }
  return { id: 'focus-active', label: 'Other active', stateType: 'started', rank: 50 }
}

/** Triage: backlog issues that were never accepted (Flow has no dedicated triage state type). */
export function isTriage(row: Pick<MyIssuesRowData, 'state' | 'triagedAt' | 'triage'>) {
  return row.triage ?? ((row.state.type as string) === 'triage')
}

function slaDescriptor(row: MyIssuesRowData, now: number): GroupDescriptor {
  const sla = row.sla
  if (!sla || sla.status === 'removed') return { id: 'sla-none', label: 'No SLA', rank: 9 }
  if (sla.status === 'completed') return { id: 'sla-met', label: 'SLA met', rank: 8 }
  if (sla.status === 'breached' || sla.breachedAt) return { id: 'sla-breached', label: 'SLA breached', rank: 0 }
  const remaining = sla.status === 'paused' ? sla.remainingMinutes : (Date.parse(sla.dueAt) - now) / 60_000
  if (remaining <= 60 * 24) return { id: 'sla-high', label: 'High risk', rank: 1 }
  if (remaining <= 60 * 24 * 3) return { id: 'sla-medium', label: 'Medium risk', rank: 2 }
  return { id: 'sla-low', label: 'Low risk', rank: 3 }
}

function activityDescriptor(value: string, now: number): GroupDescriptor {
  const date = Date.parse(value)
  const start = new Date(now); start.setHours(0, 0, 0, 0)
  const today = start.getTime()
  if (date >= today) return { id: 'activity-today', label: 'Today', rank: 0 }
  if (date >= today - DAY) return { id: 'activity-yesterday', label: 'Yesterday', rank: 1 }
  if (date >= today - 7 * DAY) return { id: 'activity-week', label: 'Past week', rank: 2 }
  if (date >= today - 30 * DAY) return { id: 'activity-month', label: 'Past month', rank: 3 }
  return { id: 'activity-older', label: 'Older', rank: 4 }
}

/** Issue comparator honoring Linear ordering fields and direction. "No priority" always sorts last. */
export function issueComparator(ordering: MyIssuesOrdering, direction: 'asc' | 'desc' = defaultOrderDirection(ordering), options: { manualOrder?: string[]; completedByRecency?: boolean; states?: MyIssuesRowData['state'][] } = {}) {
  const manual = new Map((options.manualOrder ?? []).map((id, index) => [id, index]))
  const stateOrder = new Map((options.states ?? []).map((state, index) => [state.id, index]))
  const sign = direction === 'desc' ? -1 : 1
  const value = (row: MyIssuesRowData): number | string | undefined => {
    switch (ordering) {
      case 'importance': return manual.has(row.id) ? manual.get(row.id)! - 1e12 : row.sortOrder ?? 0
      case 'title': return row.title.toLocaleLowerCase()
      case 'status': return (STATE_TYPE_RANK[row.state.type] ?? 9) * 1000 + (stateOrder.get(row.state.id) ?? 999)
      case 'assignee': return row.assignee?.name.toLocaleLowerCase()
      case 'priority': return row.priority === 0 ? undefined : row.priority
      case 'estimate': return row.estimate
      case 'created': return Date.parse(row.createdAt)
      case 'updated': return Date.parse(row.updatedAt)
      case 'myActivity': return row.myActivityAt ? Date.parse(row.myActivityAt) : undefined
      case 'dueDate': return row.dueDate ? Date.parse(row.dueDate) : undefined
      case 'linkCount': return row.linkCount ?? 0
      case 'customerCount': return row.customerIds?.length ?? 0
      case 'customerRevenue': return (row.customerRevenues ?? []).reduce((sum, item) => sum + item, 0)
      case 'timeInStatus': return row.timeInStatusMinutes
    }
  }
  return (left: MyIssuesRowData, right: MyIssuesRowData) => {
    if (options.completedByRecency && isCompleted(left) && isCompleted(right)) return completedTime(right) - completedTime(left)
    const a = value(left), b = value(right)
    // Missing values (no priority, no due date, unassigned…) sort last in either direction.
    if (a === undefined || b === undefined) {
      if (a !== b) return a === undefined ? 1 : -1
    } else if (a !== b) return (typeof a === 'string' && typeof b === 'string' ? a.localeCompare(b) : Number(a) - Number(b)) * sign
    return (left.sortOrder ?? 0) - (right.sortOrder ?? 0) || left.identifier.localeCompare(right.identifier, undefined, { numeric: true })
  }
}

function completedTime(row: MyIssuesRowData) { return Date.parse(row.completedAt ?? row.canceledAt ?? row.updatedAt) }

/** Linear `showCompletedIssues` windows. `currentCycle` keeps issues completed inside the team's current cycle. */
export function withinCompletedWindow(row: MyIssuesRowData, window: MyIssuesDisplayOptions['completedWindow'], context: IssueGroupingContext = {}) {
  if (!isCompleted(row) || window === 'all') return true
  if (window === 'none') return false
  const completed = completedTime(row)
  if (window === 'currentCycle') {
    const cycle = context.data?.cycles.find(item => item.status === 'current' && (!row.teamId || item.teamId === row.teamId))
    return cycle ? completed >= Date.parse(cycle.startsAt) : completed >= (context.now ?? Date.now()) - 14 * DAY
  }
  const days = window === 'pastDay' ? 1 : window === 'pastWeek' ? 7 : 30
  return completed >= (context.now ?? Date.now()) - days * DAY
}

export interface IssueGroupingOptions extends IssueGroupingContext {
  /** Surfaces that already applied view membership (for example Active / Backlog tabs) can skip the completed window. */
  skipCompletedWindow?: boolean
}

/**
 * Filter by display toggles, sort, nest, then group (with optional sub-grouping).
 * Sub-groups are emitted as flat groups carrying `parentGroupId` / `parentLabel` so both the list and
 * board swimlanes can render them.
 */
export function buildIssueGroups(rows: MyIssuesRowData[], display: MyIssuesDisplayOptions, options: IssueGroupingOptions = {}): MyIssuesGroupData[] {
  let projected = rows.filter(row =>
    (display.showSubIssues || !row.parentId)
    && (display.showArchived || !row.archivedAt)
    && (display.showTriageIssues !== false || !isTriage(row))
    && (options.skipCompletedWindow || withinCompletedWindow(row, display.completedWindow, options)))
  projected = [...projected].sort(issueComparator(display.ordering, display.orderDirection, { manualOrder: options.manualOrder, completedByRecency: display.orderCompletedByRecency, states: options.states ?? options.data?.states }))
  const nested = display.nestedSubIssues ? nestIssueRows(projected) : undefined
  if (nested) projected = nested.rows
  const rootOf = (row: MyIssuesRowData) => nested?.roots.get(row.id) ?? row
  const grouping = display.grouping
  const primary = collect(projected, row => groupDescriptors(rootOf(row), grouping, options))
  if (display.showEmptyGroups && grouping === 'status') {
    for (const state of options.states ?? options.data?.states ?? []) {
      if (!primary.has(state.id) && withinCompletedWindow({ state } as MyIssuesRowData, display.completedWindow === 'none' ? 'none' : 'all')) primary.set(state.id, { descriptor: statusDescriptor(state, options), issues: [] })
    }
  }
  if (display.showEmptyGroups && grouping === 'priority') for (const priority of [1, 2, 3, 4, 0] as const) {
    const [descriptor] = groupDescriptors({ priority } as MyIssuesRowData, 'priority')
    if (!primary.has(descriptor.id)) primary.set(descriptor.id, { descriptor, issues: [] })
  }
  const ordered = sortGroups([...primary.values()], display.groupOrder)
  const subGrouping = display.subGrouping && display.subGrouping !== 'none' && display.subGrouping !== grouping && grouping !== 'none' ? display.subGrouping : undefined
  return ordered.flatMap(({ descriptor, issues }) => {
    const group = toGroup(descriptor, issues)
    if (!subGrouping) return [group]
    const children = sortGroups([...collect(issues, row => groupDescriptors(rootOf(row), subGrouping, options)).values()], 'asc')
    return children.map(child => ({ ...toGroup(child.descriptor, child.issues), id: `${descriptor.id}::${child.descriptor.id}`, parentGroupId: descriptor.id, parentLabel: descriptor.label, createContext: { ...descriptor.createContext, ...child.descriptor.createContext } }))
  })
}

function collect(rows: MyIssuesRowData[], descriptorsFor: (row: MyIssuesRowData) => GroupDescriptor[]) {
  const groups = new Map<string, { descriptor: GroupDescriptor; issues: MyIssuesRowData[] }>()
  for (const row of rows) for (const descriptor of descriptorsFor(row)) {
    const current = groups.get(descriptor.id) ?? { descriptor, issues: [] }
    current.issues.push(row)
    groups.set(descriptor.id, current)
  }
  return groups
}

function sortGroups<T extends { descriptor: GroupDescriptor }>(groups: T[], order: 'asc' | 'desc') {
  const sorted = [...groups].sort((left, right) => {
    const a = left.descriptor.rank, b = right.descriptor.rank
    return typeof a === 'number' && typeof b === 'number' ? a - b : String(a).localeCompare(String(b))
  })
  return order === 'desc' ? sorted.reverse() : sorted
}

function toGroup(descriptor: GroupDescriptor, issues: MyIssuesRowData[]): MyIssuesGroupData {
  const { rank: _rank, ...group } = descriptor
  return { ...group, issues }
}

/** Parent-before-children projection used by nested sub-issues. */
export function nestIssueRows(rows: MyIssuesRowData[]) {
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

/**
 * The issue update implied by dropping a card into a group (board DnD, drag between list groups).
 * Returns undefined when the target group does not map to an editable property (the move is order-only).
 */
export function groupMoveUpdate(row: MyIssuesRowData, grouping: MyIssuesGrouping, targetGroupId: string, context: IssueGroupingContext = {}): IssueUpdateInput | undefined {
  const id = targetGroupId.includes('::') ? targetGroupId.split('::')[0] : targetGroupId
  switch (grouping) {
    case 'status': return (context.states ?? context.data?.states ?? []).some(state => state.id === id) || !context.data ? { stateId: id } : undefined
    case 'priority': return id.startsWith('priority-') ? { priority: Number(id.slice(9)) } : undefined
    case 'assignee': return id.startsWith('assignee-') ? { assigneeId: id === 'assignee-none' ? '' : id.slice(9) } : undefined
    case 'agent': return id.startsWith('agent-') ? { delegateId: id === 'agent-none' ? '' : id.slice(6) } : undefined
    case 'project': return id.startsWith('project-') ? { projectId: id === 'project-none' ? '' : id.slice(8) } : undefined
    case 'milestone': return id.startsWith('milestone-') ? { projectMilestoneId: id === 'milestone-none' ? '' : id.slice(10) } : undefined
    case 'cycle': return id.startsWith('cycle-') ? { cycleId: id === 'cycle-none' ? '' : id.slice(6) } : undefined
    // Moving an issue to another team changes its identifier; never do that from a drag.
    case 'team': return undefined
    case 'label': {
      if (!id.startsWith('label-')) return undefined
      const current = (row.labels ?? []).map(label => label.id)
      if (id === 'label-none') return { labelIds: [] }
      const labelId = id.slice(6)
      return current.includes(labelId) ? undefined : { labelIds: [...current, labelId] }
    }
    default: return undefined
  }
}

/** Groupings the server-backed issue list can group by (`issueGroupAttribute` in the API). */
export const PAGED_GROUPINGS: MyIssuesGrouping[] = ['none', 'focus', 'status', 'assignee', 'project', 'milestone', 'priority', 'cycle', 'label', 'team']
/** Orderings the server-backed issue list can sort by. */
export const PAGED_ORDERINGS: MyIssuesOrdering[] = ['importance', 'title', 'priority', 'created', 'updated']

/** Sort, direction, groupBy and display-derived conditions for `PagedIssueList` queries. */
export function pagedDisplayQuery(display: MyIssuesDisplayOptions, now = Date.now(), triageTeamIds: string[] = []) {
  const sort = ({ importance: 'sortOrder', title: 'title', priority: 'priority', created: 'createdAt', updated: 'updatedAt' } as const)[display.ordering as 'importance' | 'title' | 'priority' | 'created' | 'updated'] ?? 'sortOrder'
  const direction = display.orderDirection ?? defaultOrderDirection(display.ordering)
  const conditions: Record<string, unknown>[] = []
  if (!display.showSubIssues) conditions.push({ field: 'parent', operator: 'isEmpty' })
  const open = { field: 'statusType', operator: 'notIn', values: ['completed', 'canceled'] }
  if (display.completedWindow === 'none') conditions.push(open)
  else if (display.completedWindow !== 'all' && display.completedWindow !== 'currentCycle') {
    const days = display.completedWindow === 'pastDay' ? 1 : display.completedWindow === 'pastWeek' ? 7 : 30
    const since = new Date(now - days * DAY).toISOString()
    conditions.push({ or: [open, { field: 'completedAt', operator: 'after', values: [since] }, { field: 'canceledAt', operator: 'after', values: [since] }] })
  }
  // Hide untriaged backlog issues of triage-enabled teams, like the local isTriage() check.
  if (display.showTriageIssues === false && triageTeamIds.length) conditions.push({ not: { and: [{ field: 'team', operator: 'in', values: triageTeamIds }, { field: 'statusType', operator: 'in', values: ['backlog'] }, { field: 'triagedAt', operator: 'isEmpty' }] } })
  const groupBy = display.grouping === 'focus' ? 'status' : PAGED_GROUPINGS.includes(display.grouping) ? display.grouping : 'status'
  return { sort, direction, groupBy, archived: display.showArchived ? 'all' as const : 'false' as const, conditions }
}

/** Top-level groups (sub-groups roll up to their parent) for the display menu's group list. */
export function groupSummaries(groups: MyIssuesGroupData[]) {
  const summary = new Map<string, { id: string; label: string; count: number }>()
  for (const group of groups) {
    const id = group.parentGroupId ?? group.id
    const current = summary.get(id) ?? { id, label: group.parentLabel ?? group.label, count: 0 }
    current.count += group.issues.length
    summary.set(id, current)
  }
  return [...summary.values()]
}


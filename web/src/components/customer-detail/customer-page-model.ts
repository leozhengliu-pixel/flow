import type { BootstrapData, Customer, CustomerRequest, FeatureSettings, Issue, Project } from '@/types/flow'

/**
 * Linear's customer page request list (CustomerPage.Un + CustomerNeedListProvider, type "customer"):
 * requests are grouped by the issue or project they are linked to (one row per "feature", the newest
 * request is the primary one), then ordered and optionally grouped by status type.
 */

export type CustomerPageGrouping = 'none' | 'statusType'
export type CustomerPageOrdering = 'createdAt' | 'statusType'
export type CustomerPageCompletedWindow = 'all' | 'day' | 'week' | 'month' | 'none'

export type CustomerPageViewPreferences = {
  grouping: CustomerPageGrouping
  ordering: CustomerPageOrdering
  completed: CustomerPageCompletedWindow
  importantFirst: boolean
  fieldIdentifier: boolean
  fieldPriority: boolean
  fieldStatus: boolean
  fieldTargetDueDate: boolean
}

/** Linear's defaults (ViewPreferences.customerPageNeeds*). */
export const DEFAULT_CUSTOMER_PAGE_VIEW: CustomerPageViewPreferences = {
  grouping: 'none',
  ordering: 'createdAt',
  completed: 'all',
  importantFirst: true,
  fieldIdentifier: false,
  fieldPriority: false,
  fieldStatus: true,
  fieldTargetDueDate: false,
}

export const CUSTOMER_PAGE_VIEW_STORAGE_KEY = 'flow:customer-page-needs-view'

export function readCustomerPageView(): CustomerPageViewPreferences {
  try {
    const stored = JSON.parse(localStorage.getItem(CUSTOMER_PAGE_VIEW_STORAGE_KEY) ?? 'null') as Partial<CustomerPageViewPreferences> | null
    return { ...DEFAULT_CUSTOMER_PAGE_VIEW, ...(stored ?? {}) }
  } catch {
    return DEFAULT_CUSTOMER_PAGE_VIEW
  }
}

export function writeCustomerPageView(view: CustomerPageViewPreferences) {
  try { localStorage.setItem(CUSTOMER_PAGE_VIEW_STORAGE_KEY, JSON.stringify(view)) } catch { /* storage unavailable */ }
}

/** Per-customer "show archived requests" choice (Linear: customer_page_show_archived_needs_{id}). */
export function showArchivedStorageKey(customerId: string) {
  return `flow:customer-page-show-archived:${customerId}`
}

export type StatusTypeGroupId = 'triage' | 'started' | 'unstarted' | 'backlog' | 'completed' | 'canceled'

/** Linear's status-type groups for customer requests, in order. */
export const STATUS_TYPE_GROUPS: Array<{ id: StatusTypeGroupId; title: string }> = [
  { id: 'triage', title: 'Triage' },
  { id: 'started', title: 'Started' },
  { id: 'unstarted', title: 'Unstarted' },
  { id: 'backlog', title: 'Backlog' },
  { id: 'completed', title: 'Completed' },
  { id: 'canceled', title: 'Canceled' },
]

export type IssueLookup = (id: string) => Issue | undefined
export type ProjectLookup = (id: string) => Project | undefined

export type CustomerNeedGroup = {
  /** Feature key: `issue:{id}`, `project:{id}` or the request id for an unlinked request. */
  key: string
  primary: CustomerRequest
  additional: CustomerRequest[]
  issue?: Issue
  project?: Project
  /** Linked to an issue or project the viewer has not loaded (yet). */
  pending: boolean
}

export function isImportant(request: Pick<CustomerRequest, 'priority'>) {
  return (request.priority ?? 0) > 0
}

export function featureKey(request: Pick<CustomerRequest, 'id' | 'issueId' | 'projectId'>) {
  if (request.issueId) return `issue:${request.issueId}`
  if (request.projectId) return `project:${request.projectId}`
  return request.id
}

/** A request is archived on its own or with the issue / project it belongs to (Linear archives needs with them). */
export function isArchivedNeed(request: CustomerRequest, issue?: Issue, project?: Project) {
  return Boolean(request.archivedAt || issue?.archivedAt || project?.archivedAt)
}

/** Groups the requests by feature; the newest request leads its group. */
export function groupCustomerNeeds(requests: CustomerRequest[], issueById: IssueLookup, projectById: ProjectLookup): CustomerNeedGroup[] {
  const sorted = [...requests].sort((a, b) => b.createdAt.localeCompare(a.createdAt))
  const groups = new Map<string, CustomerNeedGroup>()
  for (const request of sorted) {
    const key = featureKey(request)
    const existing = groups.get(key)
    if (existing) { existing.additional.push(request); continue }
    const issue = request.issueId ? issueById(request.issueId) : undefined
    const project = !request.issueId && request.projectId ? projectById(request.projectId) : undefined
    groups.set(key, { key, primary: request, additional: [], issue, project, pending: Boolean((request.issueId && !issue) || (!request.issueId && request.projectId && !project)) })
  }
  return [...groups.values()]
}

export function groupHasImportant(group: Pick<CustomerNeedGroup, 'primary' | 'additional'>) {
  return isImportant(group.primary) || group.additional.some(isImportant)
}

export function groupHasDetails(group: Pick<CustomerNeedGroup, 'primary' | 'additional'>) {
  return Boolean(group.primary.body.trim()) || group.additional.some(request => request.body.trim())
}

type TriageSettings = Pick<BootstrapData, 'teamSettings'>

/** The status-type group of a request's issue or project (Linear `getGroup` for "statusType"). */
export function statusTypeOf(group: Pick<CustomerNeedGroup, 'issue' | 'project'>, data?: TriageSettings): StatusTypeGroupId | undefined {
  const issue = group.issue
  if (issue) {
    const type = issue.state?.type
    if (type === 'backlog' && !issue.triagedAt && data?.teamSettings?.[issue.team?.id]?.triageEnabled) return 'triage'
    if (type === 'backlog' || type === 'unstarted' || type === 'started' || type === 'completed' || type === 'canceled') return type
    return undefined
  }
  const projectType = group.project?.status?.type
  if (!projectType) return undefined
  if (projectType === 'planned') return 'unstarted'
  if (projectType === 'paused') return 'started'
  if (projectType === 'backlog' || projectType === 'started' || projectType === 'completed' || projectType === 'canceled') return projectType
  return undefined
}

const DAY = 864e5
/** When the linked issue / project was completed or canceled (Linear `doneAt`). */
export function doneAt(group: Pick<CustomerNeedGroup, 'issue' | 'project'>): number | undefined {
  const issue = group.issue
  if (issue) {
    const value = issue.completedAt ?? issue.canceledAt
    return value ? Date.parse(value) : undefined
  }
  const project = group.project
  if (project && (project.status?.type === 'completed' || project.status?.type === 'canceled')) return Date.parse(project.updatedAt)
  return undefined
}

/** Linear's "Completed" display option: keeps requests whose issue/project finished within the window. */
export function passesCompletedWindow(group: Pick<CustomerNeedGroup, 'issue' | 'project'>, window: CustomerPageCompletedWindow, now = Date.now()) {
  if (window === 'all') return true
  const done = doneAt(group)
  if (done === undefined || Number.isNaN(done)) return true
  if (window === 'none') return false
  const span = window === 'day' ? DAY : window === 'week' ? 7 * DAY : 30 * DAY
  return now - done <= span
}

export function orderCustomerNeedGroups(groups: CustomerNeedGroup[], view: Pick<CustomerPageViewPreferences, 'ordering' | 'importantFirst'>, data?: TriageSettings) {
  const statusIndex = (group: CustomerNeedGroup) => {
    const type = statusTypeOf(group, data)
    return type ? STATUS_TYPE_GROUPS.findIndex(item => item.id === type) : -1
  }
  return [...groups].sort((a, b) => {
    if (view.importantFirst) {
      const important = Number(groupHasImportant(b)) - Number(groupHasImportant(a))
      if (important) return important
    }
    if (view.ordering === 'statusType') {
      const status = statusIndex(a) - statusIndex(b)
      if (status) return status
    }
    return b.primary.createdAt.localeCompare(a.primary.createdAt)
  })
}

export type CustomerNeedSection = { id: string; title?: string; groups: CustomerNeedGroup[] }

/** Splits ordered groups into Linear's status-type sections (empty sections are hidden). */
export function sectionCustomerNeedGroups(groups: CustomerNeedGroup[], grouping: CustomerPageGrouping, data?: TriageSettings): CustomerNeedSection[] {
  if (grouping === 'none') return [{ id: 'all', groups }]
  return STATUS_TYPE_GROUPS
    .map(section => ({ id: section.id, title: section.title, groups: groups.filter(group => statusTypeOf(group, data) === section.id) }))
    .filter(section => section.groups.length > 0)
}

/** Linear's short relative time (`3d`, `5h`, `2w`, `now`) used by request rows. */
export function shortRelativeTime(value: string, now = Date.now()) {
  const diff = Math.max(0, now - Date.parse(value))
  const MINUTE = 6e4, HOUR = 36e5, WEEK = 7 * DAY, MONTH = 30 * DAY, YEAR = 365 * DAY
  const round = (amount: number) => Math.max(1, Math.floor(amount) + (amount - Math.floor(amount) >= .66 ? 1 : 0))
  if (diff < MINUTE) return 'now'
  if (diff < HOUR) return `${Math.round(diff / MINUTE)}min`
  if (diff < DAY) return `${Math.round(diff / HOUR)}h`
  if (diff < 5 * DAY) return `${Math.floor(diff / DAY)}d`
  if (diff < MONTH) {
    const days = Math.round(diff / DAY)
    return days % 7 === 0 ? `${days / 7}w` : `${days}d`
  }
  if (diff < 10 * WEEK) return `${round(diff / WEEK)}w`
  if (diff < YEAR) {
    const months = round(diff / MONTH)
    return months === 12 ? '1y' : `${months}mo`
  }
  return `${round(diff / YEAR)}y`
}

/** Linear's long relative time (`3 days ago`, `just now`) used in an expanded request's header. */
export function longRelativeTime(value: string, now = Date.now()) {
  const short = shortRelativeTime(value, now)
  if (short === 'now') return 'just now'
  const match = /^(\d+)(min|h|d|w|mo|y)$/.exec(short)
  if (!match) return short
  const count = Number(match[1])
  const unit = ({ min: 'minute', h: 'hour', d: 'day', w: 'week', mo: 'month', y: 'year' } as Record<string, string>)[match[2]]
  return `${count} ${unit}${count === 1 ? '' : 's'} ago`
}

/** Linear's full timestamp (`Wed, Oct 7, 2026, 3:04:05 PM`) for tooltips. */
export function fullTimestamp(value: string, locale?: string) {
  return new Intl.DateTimeFormat(locale, { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: 'numeric', second: 'numeric' }).format(new Date(value))
}

/** Revenue as Linear shows it on the customer page: compact amount plus a muted `/yr` or `/mo`. */
export function customerRevenueParts(value: number | undefined, settings?: Partial<FeatureSettings>, locale?: string) {
  if (value == null || !value) return undefined
  const monthly = settings?.customerRevenueFormat === 'monthly'
  const amount = monthly ? Math.round(value / 12) : value
  const currency = /^[A-Z]{3}$/.test(settings?.customerRevenueCurrency ?? '') ? settings!.customerRevenueCurrency! : 'USD'
  const compact = new Intl.NumberFormat(locale, { style: 'currency', currency, notation: 'compact', maximumFractionDigits: 1 }).format(amount)
  const exact = new Intl.NumberFormat(locale, { style: 'currency', currency, maximumFractionDigits: 0 }).format(amount)
  return { compact, exact, suffix: monthly ? '/mo' : '/yr' }
}

export function formatCustomerSize(value: number, locale?: string) {
  return new Intl.NumberFormat(locale).format(value)
}

/** The issue/project markdown Linear copies with "Copy requests as Markdown". */
export function customerRequestsMarkdown(name: string, groups: Array<Pick<CustomerNeedGroup, 'primary' | 'additional' | 'issue' | 'project'>>, issueUrl: (issue: Issue) => string, projectUrl: (project: Project) => string) {
  const lines = [`# ${name} requests`, '']
  for (const group of groups) {
    for (const request of [group.primary, ...group.additional]) {
      const feature = group.issue ? `[${group.issue.identifier} ${group.issue.title}](${issueUrl(group.issue)})` : group.project ? `[${group.project.name}](${projectUrl(group.project)})` : undefined
      const head = `- ${isImportant(request) ? '**Important** ' : ''}${feature ?? 'Unlinked request'} (${request.createdAt.slice(0, 10)})`
      lines.push(head)
      const body = request.body.trim()
      if (body) lines.push(...body.split('\n').map(line => `  > ${line}`))
    }
  }
  return lines.join('\n')
}

/** The issue title Linear proposes for a new issue created from a customer page request. */
export function newIssueTitleFor(customer: Pick<Customer, 'name'>) {
  return customer.name ? `Customer request from ${customer.name}` : 'New issue'
}

/** The team new issues from customer requests go to: the settings' default team, else the first team. */
export function customerRequestIssueTeam(data: Pick<BootstrapData, 'teams' | 'workspaceSettings'>) {
  const preferred = data.workspaceSettings.featureSettings?.customerDefaultTeamId
  return data.teams.find(team => team.id === preferred) ?? data.teams[0]
}

/** The team's triage / backlog state (Flow keeps triage as the backlog type before triage). */
export function customerRequestIssueState(data: Pick<BootstrapData, 'states'>, teamId: string | undefined) {
  return [...data.states]
    .filter(state => state.type === 'backlog' && (!state.teamId || state.teamId === teamId))
    .sort((a, b) => Number(Boolean(b.teamId)) - Number(Boolean(a.teamId)) || a.position - b.position)[0]
}

/** Linear's customer notification types (Notification.customerNotificationTypes). */
export const CUSTOMER_NOTIFICATION_EVENTS: Array<[event: string, label: string]> = [
  ['customerNeedCreated', 'A request is added'],
  ['customerNeedMarkedAsImportant', 'A request is marked as important'],
  ['customerNeedResolved', 'A requested issue or project is completed or canceled'],
]

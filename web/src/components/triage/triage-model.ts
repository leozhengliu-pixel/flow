import type { Issue, WorkflowState } from '@/types/flow'

export function canceledState(states: WorkflowState[], teamId: string) {
  const scoped = states.filter(state => !state.teamId || state.teamId === teamId)
  return scoped.find(state => state.type === 'canceled' && !/duplicate/i.test(state.name)) ?? scoped.find(state => state.type === 'canceled')
}

export function duplicateState(states: WorkflowState[], teamId: string) {
  return states.find(state => (!state.teamId || state.teamId === teamId) && state.type === 'canceled' && /duplicate/i.test(state.name))
}

const SNOOZE_HOUR = 9

function atHour(date: Date, hour = SNOOZE_HOUR) {
  const next = new Date(date)
  next.setHours(hour, 0, 0, 0)
  return next
}

function addDays(date: Date, days: number) {
  const next = new Date(date)
  next.setDate(next.getDate() + days)
  return next
}

/** Linear's snooze options: An hour from now, Tomorrow 9 AM, Next week (Monday 9 AM), A month from now 9 AM. */
export function snoozePresets(now = new Date()) {
  const month = new Date(now)
  month.setMonth(month.getMonth() + 1)
  return [
    { id: 'hour', label: 'An hour from now', until: new Date(now.getTime() + 3_600_000) },
    { id: 'tomorrow', label: 'Tomorrow', until: atHour(addDays(now, 1)) },
    { id: 'week', label: 'Next week', until: atHour(addDays(now, ((8 - now.getDay()) % 7) || 7)) },
    { id: 'month', label: 'A month from now', until: atHour(month) },
  ]
}

const UNIT_MS: Record<string, number> = { minute: 60_000, hour: 3_600_000, day: 86_400_000, week: 604_800_000 }
const WEEKDAYS = ['sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday']

function unitOf(word: string) {
  const value = word.toLowerCase()
  if (/^(m|min|mins|minute|minutes)$/.test(value)) return 'minute'
  if (/^(h|hr|hrs|hour|hours)$/.test(value)) return 'hour'
  if (/^(d|day|days)$/.test(value)) return 'day'
  if (/^(w|wk|wks|week|weeks)$/.test(value)) return 'week'
  if (/^(mo|month|months)$/.test(value)) return 'month'
  if (/^(y|yr|year|years)$/.test(value)) return 'year'
  return undefined
}

/**
 * Parses Linear's snooze input ("4 pm", "2 days", "in 5 weeks", "tomorrow", "next week", "friday", "16:30").
 * Returns a future date, or undefined when the text is not understood.
 */
export function parseSnoozeInput(text: string, now = new Date()): Date | undefined {
  const value = text.trim().toLowerCase().replace(/\s+/g, ' ')
  if (!value) return
  if (value === 'tomorrow') return atHour(addDays(now, 1))
  if (value === 'next week') return snoozePresets(now)[2].until
  if (value === 'next month') return snoozePresets(now)[3].until
  const weekday = WEEKDAYS.findIndex(day => value === day || value === day.slice(0, 3) || value === `next ${day}`)
  if (weekday >= 0) return atHour(addDays(now, ((weekday - now.getDay() + 7) % 7) || 7))
  const relative = value.match(/^(?:in )?(\d+|an?|one) ?([a-z]+)(?: from now)?$/)
  if (relative) {
    const amount = /^\d+$/.test(relative[1]) ? Number(relative[1]) : 1
    const unit = unitOf(relative[2])
    if (unit && amount > 0 && amount <= 1000) {
      if (unit === 'month' || unit === 'year') {
        const date = new Date(now)
        if (unit === 'month') date.setMonth(date.getMonth() + amount)
        else date.setFullYear(date.getFullYear() + amount)
        return date
      }
      return new Date(now.getTime() + amount * UNIT_MS[unit])
    }
  }
  const clock = value.match(/^(?:(today|tomorrow) )?(?:at )?(\d{1,2})(?::(\d{2}))? ?(am|pm|a|p)?$/)
  if (clock && (clock[3] || clock[4] || clock[1])) {
    let hour = Number(clock[2])
    const minute = Number(clock[3] ?? 0)
    const meridiem = clock[4]?.[0]
    if (meridiem && (hour < 1 || hour > 12)) return
    if (meridiem === 'p' && hour < 12) hour += 12
    if (meridiem === 'a' && hour === 12) hour = 0
    if (hour > 23 || minute > 59) return
    let date = new Date(now)
    if (clock[1] === 'tomorrow') date = addDays(date, 1)
    date.setHours(hour, minute, 0, 0)
    if (!clock[1] && date.getTime() <= now.getTime()) date = addDays(date, 1)
    return date.getTime() > now.getTime() ? date : undefined
  }
  return
}

export function isSnoozed(issue: Pick<Issue, 'snoozedUntil'>, now = Date.now()) {
  return Boolean(issue.snoozedUntil && Date.parse(issue.snoozedUntil) > now)
}

/** An issue is in Triage when its team has triage enabled, it sits in a backlog-type state and was never triaged. */
export function isIssueInTriage(
  issue: Pick<Issue, 'team' | 'state' | 'triagedAt'>,
  teamSettings: Record<string, { triageEnabled?: boolean } | undefined> | undefined,
) {
  return Boolean(teamSettings?.[issue.team.id]?.triageEnabled && issue.state.type === 'backlog' && !issue.triagedAt)
}

/** Issues listed in a team's Triage view (triage enabled is checked by the page). */
export function isTriageCandidate(issue: Issue, teamId: string) {
  return issue.team.id === teamId && issue.state.type === 'backlog' && !issue.triagedAt && !issue.archivedAt
}

/** Server filter for the same set, used by the paged list and the sidebar count. */
export function triageIssueFilter(options: { excludeSnoozed?: boolean; now?: Date } = {}) {
  const and: Record<string, unknown>[] = [{ field: 'status', operator: 'in', values: ['backlog'] }, { field: 'triagedAt', operator: 'isEmpty' }]
  if (options.excludeSnoozed) and.push({ or: [{ field: 'snoozedUntil', operator: 'isEmpty' }, { field: 'snoozedUntil', operator: 'before', values: [(options.now ?? new Date()).toISOString()] }] })
  return { and }
}

/** When the issue entered Triage: its last status change into the backlog state, else its creation. */
export function addedToTriageAt(issue: Pick<Issue, 'statusChangedAt' | 'createdAt'>) {
  return issue.statusChangedAt ?? issue.createdAt
}

/** Pseudo workflow state used to display Linear's orange "Triage" status glyph. */
export const TRIAGE_STATUS = { id: 'triage', name: 'Triage', color: 'var(--inbox-status-triage)', type: 'backlog' } as const

/** Linear triage shortcuts: 1 Accept, 2 Decline, 3 (or M M) Mark as duplicate, H Snooze. */
export const TRIAGE_SHORTCUTS = { accept: '1', decline: '2', duplicate: '3', snooze: 'H' } as const
export type TriageActionKind = keyof typeof TRIAGE_SHORTCUTS

/** Team statuses ordered by position (team-scoped plus workspace-wide). */
export function teamStates(states: WorkflowState[], teamId: string) {
  return states.filter(state => state.teamId === teamId || !state.teamId).sort((a, b) => (a.position ?? 0) - (b.position ?? 0))
}

/** The team's triage status: its backlog-type default status, else the first backlog status. */
export function triageState(states: WorkflowState[], teamId: string, defaultStateId?: string) {
  const scoped = teamStates(states, teamId)
  return scoped.find(state => state.id === defaultStateId && state.type === 'backlog') ?? scoped.find(state => state.type === 'backlog')
}

/** Status an accepted triage issue moves to: the team default when it is not a triage/backlog state, else the first unstarted. */
export function resolveAcceptState(states: WorkflowState[], defaultStateId?: string): WorkflowState | undefined {
  if (defaultStateId) {
    const configured = states.find(state => state.id === defaultStateId && state.type !== 'backlog')
    if (configured) return configured
  }
  return (
    states.find(state => state.type === 'unstarted') ??
    states.find(state => state.type === 'started') ??
    states.find(state => state.type !== 'backlog')
  )
}

/* ── Display options (Linear: Ordering + direction, Show snoozed, Display properties ID / Due date) ── */

export type TriageOrdering = 'addedToTriage' | 'priority' | 'dueDate'
export const TRIAGE_ORDERINGS: { value: TriageOrdering; label: string }[] = [
  { value: 'addedToTriage', label: 'Added to triage' },
  { value: 'priority', label: 'Priority' },
  { value: 'dueDate', label: 'Due date' },
]
export type TriageDisplayProperty = 'id' | 'dueDate'
export const TRIAGE_DISPLAY_PROPERTIES: { value: TriageDisplayProperty; label: string }[] = [
  { value: 'id', label: 'ID' },
  { value: 'dueDate', label: 'Due date' },
]

export type TriageDisplaySettings = {
  ordering: TriageOrdering
  /** "Newest first" (the default); false is "Oldest first". */
  newestFirst: boolean
  showSnoozed: boolean
  properties: TriageDisplayProperty[]
}

export const DEFAULT_TRIAGE_DISPLAY: TriageDisplaySettings = { ordering: 'addedToTriage', newestFirst: true, showSnoozed: false, properties: ['id', 'dueDate'] }

export function triageDisplayKey(workspaceId: string, userId: string, teamId: string) {
  return `flow.triage.display.${workspaceId}.${userId}.${teamId}`
}

export function readTriageDisplay(key: string): TriageDisplaySettings {
  try {
    const raw = globalThis.localStorage?.getItem(key)
    if (!raw) return DEFAULT_TRIAGE_DISPLAY
    const value = JSON.parse(raw) as Partial<TriageDisplaySettings>
    return {
      ordering: TRIAGE_ORDERINGS.some(item => item.value === value.ordering) ? value.ordering! : DEFAULT_TRIAGE_DISPLAY.ordering,
      newestFirst: typeof value.newestFirst === 'boolean' ? value.newestFirst : DEFAULT_TRIAGE_DISPLAY.newestFirst,
      showSnoozed: value.showSnoozed === true,
      properties: Array.isArray(value.properties) ? value.properties.filter((item): item is TriageDisplayProperty => item === 'id' || item === 'dueDate') : DEFAULT_TRIAGE_DISPLAY.properties,
    }
  } catch {
    return DEFAULT_TRIAGE_DISPLAY
  }
}

export function writeTriageDisplay(key: string, settings: TriageDisplaySettings) {
  try {
    globalThis.localStorage?.setItem(key, JSON.stringify(settings))
  } catch {
    /* Display preferences are best-effort in private browsing. */
  }
}

const PRIORITY_RANK = (priority: number) => (priority === 0 ? 5 : priority)

/**
 * Orders triage issues. "Newest first" puts the most recently added first; for Priority and Due date it is the
 * natural order (urgent / soonest first), "Oldest first" reverses it. Ties fall back to the newest addition.
 */
export function sortTriageIssues(issues: Issue[], settings: Pick<TriageDisplaySettings, 'ordering' | 'newestFirst'>) {
  const added = (issue: Issue) => Date.parse(addedToTriageAt(issue)) || 0
  const direction = settings.newestFirst ? 1 : -1
  const base = (left: Issue, right: Issue) => {
    if (settings.ordering === 'priority') return PRIORITY_RANK(left.priority) - PRIORITY_RANK(right.priority)
    if (settings.ordering === 'dueDate') {
      if (!left.dueDate || !right.dueDate) return 0
      return Date.parse(left.dueDate) - Date.parse(right.dueDate)
    }
    return added(right) - added(left)
  }
  // Issues without a due date always sort after dated ones.
  const undated = (left: Issue, right: Issue) => settings.ordering === 'dueDate' ? Number(!left.dueDate) - Number(!right.dueDate) : 0
  return [...issues].sort((left, right) => undated(left, right) || (base(left, right) * direction) || (added(right) - added(left)))
}

/** "Mon 9:00 AM" within the next week, otherwise "Nov 4, 9:00 AM". */
export function formatSnoozeDate(date: Date) {
  const locale = typeof document !== 'undefined' && document.documentElement.dataset.locale === 'zh-CN' ? 'zh-CN' : 'en-US'
  const soon = date.getTime() - Date.now() < 6 * 86_400_000
  return new Intl.DateTimeFormat(locale, soon ? { weekday: 'short', hour: 'numeric', minute: '2-digit' } : { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }).format(date)
}

/** Fired when triage membership changes so the sidebar count refreshes in paged workspaces. */
export const TRIAGE_CHANGED_EVENT = 'flow-triage-changed'

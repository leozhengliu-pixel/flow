import type { MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'

/**
 * Linear's Dates submenu: each date property compares against a preset point in time
 * ("Due date before 1 week from now", "Created date after 1 month ago") or a custom day.
 * Values are `<kind>:<offset>` (`due:+1w`, `created:-3m`) or `<kind>:<YYYY-MM-DD>`. Client matching and
 * the server query both resolve the threshold through `dateFilterThreshold`, so they always agree.
 */
export type DateFilterKind = 'due' | 'created' | 'updated' | 'started' | 'completed' | 'triaged' | 'status'

const DAY = 86_400_000
const UNIT_DAYS: Record<string, number> = { d: 1, w: 7, m: 30, y: 365 }

export interface ParsedDateValue { kind: DateFilterKind; days?: number; date?: string }

export function parseDateFilterValue(value: string): ParsedDateValue | undefined {
  const match = value.match(/^(due|created|updated|started|completed|triaged|status):(?:([+-])(\d+)([dwmy])|(\d{4}-\d{2}-\d{2}))$/)
  if (!match) return
  const kind = match[1] as DateFilterKind
  if (match[5]) return { kind, date: match[5] }
  return { kind, days: (match[2] === '-' ? -1 : 1) * Number(match[3]) * UNIT_DAYS[match[4]] }
}

/** Local calendar day `YYYY-MM-DD` (due dates are calendar days, not instants). */
export function localDay(time: number) {
  const date = new Date(time)
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

/** The comparison point: a calendar day for due dates, an ISO instant for timestamps. */
export function dateFilterThreshold(parsed: ParsedDateValue, now: number): string {
  if (parsed.kind === 'due') return parsed.date ?? localDay(now + (parsed.days ?? 0) * DAY)
  if (parsed.date) return new Date(`${parsed.date}T00:00:00`).toISOString()
  return new Date(now + (parsed.days ?? 0) * DAY).toISOString()
}

/** Row/server field compared for each kind. */
export const DATE_FILTER_FIELDS: Record<DateFilterKind, 'dueDate' | 'createdAt' | 'updatedAt' | 'startedAt' | 'completedAt' | 'triagedAt' | 'statusChangedAt'> = {
  due: 'dueDate', created: 'createdAt', updated: 'updatedAt', started: 'startedAt', completed: 'completedAt', triaged: 'triagedAt', status: 'statusChangedAt',
}

export function compareDateFilter(actual: string | undefined, parsed: ParsedDateValue, operator: 'before' | 'after', now: number) {
  if (!actual) return false
  const threshold = dateFilterThreshold(parsed, now)
  if (parsed.kind === 'due') {
    const day = actual.slice(0, 10)
    return operator === 'before' ? day < threshold : day > threshold
  }
  const time = Date.parse(actual), limit = Date.parse(threshold)
  if (!Number.isFinite(time)) return false
  return operator === 'before' ? time < limit : time > limit
}

const FUTURE = [['+1d', '1 day from now'], ['+3d', '3 days from now'], ['+1w', '1 week from now'], ['+1m', '1 month from now'], ['+3m', '3 months from now']] as const
const PAST = [['-1d', '1 day ago'], ['-3d', '3 days ago'], ['-1w', '1 week ago'], ['-1m', '1 month ago'], ['-3m', '3 months ago'], ['-6m', '6 months ago'], ['-1y', '1 year ago']] as const
const DURATIONS = [['-1d', '1 day'], ['-3d', '3 days'], ['-1w', '1 week'], ['-2w', '2 weeks'], ['-1m', '1 month'], ['-3m', '3 months']] as const

function custom(kind: DateFilterKind, filterLabel: string): MyIssuesFilterOption {
  return { id: `${kind}-custom`, label: 'Custom date or timeframe…', kind: 'dateCustom', filterLabel, textConditionPrefix: `${kind}:`, textConditionInput: 'date' }
}
function presets(kind: DateFilterKind, filterLabel: string, entries: readonly (readonly [string, string])[]): MyIssuesFilterOption[] {
  return entries.map(([offset, label]) => ({ id: `${kind}:${offset}`, label, kind: 'dueDate', filterLabel }))
}

/** Linear's Dates submenu. `count` helpers stay with the caller (counts depend on the loaded rows). */
export function dateFilterMenu(): MyIssuesFilterOption[] {
  const past = (id: string, kind: DateFilterKind, label: string): MyIssuesFilterOption => ({ id, label, kind: 'dateCategory', children: [...presets(kind, label, PAST), custom(kind, label)] })
  return [
    { id: 'due-date', label: 'Due date', kind: 'dateCategory', children: [
      { id: 'overdue', label: 'Overdue', kind: 'dueDate', filterLabel: 'Due date' },
      ...presets('due', 'Due date', FUTURE),
      custom('due', 'Due date'),
      { id: 'no-due-date', label: 'No due date', kind: 'dueDate', filterLabel: 'Due date' },
    ] },
    past('created-date', 'created', 'Created date'),
    past('updated-date', 'updated', 'Updated date'),
    past('started-date', 'started', 'Started date'),
    past('completed-date', 'completed', 'Completed date'),
    { id: 'auto-closed-date', label: 'Auto-closed date', kind: 'dateCategory', children: [{ id: 'auto-closed-any', label: 'Has auto-closed date', kind: 'dueDate', filterLabel: 'Auto-closed date' }] },
    past('triaged-date', 'triaged', 'Triaged date'),
    { id: 'time-current-status', label: 'Time in current status', kind: 'dateCategory', children: presets('status', 'Time in current status', DURATIONS) },
  ]
}

/** Display label for a stored dates value (custom days show the day itself). */
export function dateFilterValueLabel(value: string): string | undefined {
  const parsed = parseDateFilterValue(value)
  if (!parsed) return
  if (parsed.date) return parsed.date
  const offset = value.slice(value.indexOf(':') + 1)
  const table = parsed.kind === 'status' ? DURATIONS : parsed.kind === 'due' ? FUTURE : PAST
  return table.find(([id]) => id === offset)?.[1]
}

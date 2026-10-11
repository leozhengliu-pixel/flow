import type { MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'

/**
 * The reference app's Dates submenu: each date property compares against a preset point in time
 * ("Due date before 1 week from now", "Created date after 1 month ago") or a custom day.
 * Values are `<kind>:<offset>` (`due:+1w`, `created:-3m`) or `<kind>:<YYYY-MM-DD>`. Client matching and
 * the server query both resolve the threshold through `dateFilterThreshold`, so they always agree.
 * Releases ▸ Released date uses the same values (`released:-1w`) under the Releases filter.
 */
export type DateFilterKind = 'due' | 'created' | 'updated' | 'started' | 'completed' | 'autoClosed' | 'triaged' | 'status' | 'released'

const DAY = 86_400_000
const UNIT_DAYS: Record<string, number> = { d: 1, w: 7, m: 30, y: 365 }

/** `<kind>:<±N unit>` or `<kind>:<YYYY-MM-DD>`, shared with the chip operator rules. */
export const DATE_FILTER_VALUE = /^(due|created|updated|started|completed|autoClosed|triaged|status|released):(?:([+-])(\d+)([dwmy])|(\d{4}-\d{2}-\d{2}))$/

export interface ParsedDateValue { kind: DateFilterKind; days?: number; date?: string }

export function parseDateFilterValue(value: string): ParsedDateValue | undefined {
  const match = value.match(DATE_FILTER_VALUE)
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

/** Row/server field compared for each kind ("releasedAt" matches any of the issue's released releases). */
export const DATE_FILTER_FIELDS: Record<DateFilterKind, 'dueDate' | 'createdAt' | 'updatedAt' | 'startedAt' | 'completedAt' | 'autoClosedAt' | 'triagedAt' | 'statusChangedAt' | 'releasedAt'> = {
  due: 'dueDate', created: 'createdAt', updated: 'updatedAt', started: 'startedAt', completed: 'completedAt', autoClosed: 'autoClosedAt', triaged: 'triagedAt', status: 'statusChangedAt', released: 'releasedAt',
}

/** One date compared to the value's threshold; several dates (an issue's releases) match when any does. */
export function compareDateFilter(actual: string | readonly string[] | undefined, parsed: ParsedDateValue, operator: 'before' | 'after', now: number): boolean {
  if (Array.isArray(actual)) return actual.some(item => compareDateFilter(item, parsed, operator, now))
  if (!actual || typeof actual !== 'string') return false
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
/** The reference app's Time in current status presets (typed durations add more, see `durationFilterOptions`). */
const DURATIONS = [['-1d', '1 day'], ['-1w', '1 week'], ['-2w', '2 weeks'], ['-1m', '1 month'], ['-3m', '3 months'], ['-6m', '6 months']] as const

function custom(kind: DateFilterKind, filterLabel: string): MyIssuesFilterOption {
  return { id: `${kind}-custom`, label: 'Custom date or timeframe…', kind: 'dateCustom', filterLabel, textConditionPrefix: `${kind}:`, textConditionInput: 'date' }
}
function presets(kind: DateFilterKind, filterLabel: string, entries: readonly (readonly [string, string])[]): MyIssuesFilterOption[] {
  return entries.map(([offset, label]) => ({ id: `${kind}:${offset}`, label, kind: 'dueDate', filterLabel }))
}

/** The reference app's past-date block: "1 day ago" … "1 year ago" and a custom date. */
export function pastDateFilterOptions(kind: DateFilterKind, filterLabel: string): MyIssuesFilterOption[] {
  return [...presets(kind, filterLabel, PAST), custom(kind, filterLabel)]
}

/** Releases ▸ Released date: "No released date", the past presets and a custom date. */
export function releasedDateFilterCategory(): MyIssuesFilterOption {
  return { id: 'released-date', label: 'Released date', kind: 'dateCategory', children: [
    { id: 'no-released-date', label: 'No released date', kind: 'dueDate', filterLabel: 'Released date' },
    ...pastDateFilterOptions('released', 'Released date'),
  ] }
}

/**
 * The reference app's Dates submenu. `count` helpers stay with the caller (counts depend on the loaded rows).
 * Released date joins it (after Auto-closed date) once the workspace has release pipelines.
 */
export function dateFilterMenu({ releasedDate = false }: { releasedDate?: boolean } = {}): MyIssuesFilterOption[] {
  const past = (id: string, kind: DateFilterKind, label: string, before: MyIssuesFilterOption[] = []): MyIssuesFilterOption => ({ id, label, kind: 'dateCategory', children: [...before, ...pastDateFilterOptions(kind, label)] })
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
    past('completed-date', 'completed', 'Completed date', [{ id: 'no-completed-date', label: 'No completed date', kind: 'dueDate', filterLabel: 'Completed date' }]),
    past('auto-closed-date', 'autoClosed', 'Auto-closed date'),
    ...(releasedDate ? [releasedDateFilterCategory()] : []),
    past('triaged-date', 'triaged', 'Triaged date'),
    { id: 'time-current-status', label: 'Time in current status', kind: 'dateCategory', durationSearch: 'status:', searchPlaceholder: 'Try: 2 days, 3 weeks…', children: presets('status', 'Time in current status', DURATIONS) },
  ]
}

const UNIT_NAMES: Record<string, string> = { d: 'day', w: 'week', m: 'month', y: 'year' }
function amountLabel(count: number, unit: string) { return `${count} ${UNIT_NAMES[unit]}${count === 1 ? '' : 's'}` }

/**
 * The reference app's typed durations under Time in current status ("Try: 2 days, 3 weeks…"): "2 days", "3w",
 * "10 months" (or "2 天", "3 周") become `status:-2d`-style options. Unparseable text offers nothing.
 */
export function durationFilterOptions(prefix: string, query: string, filterLabel = 'Time in current status'): MyIssuesFilterOption[] {
  const match = query.trim().toLocaleLowerCase().match(/^(\d{1,3})\s*(d|days?|w|wks?|weeks?|m|mos?|months?|y|yrs?|years?|天|日|周|星期|个月|月|年)$/)
  if (!match) return []
  const count = Number(match[1])
  if (!count) return []
  const unit = ({ 天: 'd', 日: 'd', 周: 'w', 星期: 'w', 个月: 'm', 月: 'm', 年: 'y' } as Record<string, string>)[match[2]] ?? match[2][0]
  return [{ id: `${prefix}-${count}${unit}`, label: amountLabel(count, unit), kind: 'dueDate', filterLabel, keywords: query.trim() }]
}

/** Display label for a stored dates value (custom days show the day itself, typed durations their amount). */
export function dateFilterValueLabel(value: string): string | undefined {
  const parsed = parseDateFilterValue(value)
  if (!parsed) return
  if (parsed.date) return parsed.date
  const offset = value.slice(value.indexOf(':') + 1)
  const table = parsed.kind === 'status' ? DURATIONS : parsed.kind === 'due' ? FUTURE : PAST
  const preset = table.find(([id]) => id === offset)?.[1]
  if (preset) return preset
  const amount = amountLabel(Number(offset.slice(1, -1)), offset.slice(-1))
  return parsed.kind === 'status' ? amount : offset.startsWith('+') ? `${amount} from now` : `${amount} ago`
}

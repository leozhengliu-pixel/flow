import type { MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'

/**
 * The reference app's Dates submenu: each date property compares against a preset point in time
 * ("Due date before 1 week from now", "Created date after 1 month ago"), a custom day ("Created date on/before/after
 * Nov 5") or a calendar period ("Created date within Feb 2022", Q4 2026, H2 2026, 2026).
 * Values are `<kind>:<offset>` (`due:+1w`, `created:-3m`), `<kind>:<YYYY-MM-DD>` or a period `<kind>:<YYYY-MM>`,
 * `<kind>:<YYYY>-Q<n>`, `<kind>:<YYYY>-H<n>`, `<kind>:<YYYY>`. Client matching and the server query both resolve
 * the comparison through `dateFilterBounds` / `dateFilterThreshold`, so they always agree.
 * Releases ▸ Released date uses the same values (`released:-1w`) under the Releases filter.
 */
export type DateFilterKind = 'due' | 'created' | 'updated' | 'started' | 'completed' | 'canceled' | 'autoClosed' | 'triaged' | 'status' | 'released'
/** Presets compare before/after a point; a custom day is on, before or after it (on or after: the reference app's `gte`); a period is within. */
export type DateFilterOperator = 'before' | 'after' | 'on' | 'within'
export type DatePeriodUnit = 'month' | 'quarter' | 'half' | 'year'

const DAY = 86_400_000
const UNIT_DAYS: Record<string, number> = { d: 1, w: 7, m: 30, y: 365 }

/** `<kind>:<±N unit>`, `<kind>:<YYYY-MM-DD>` or a period, shared with the chip operator rules. */
export const DATE_FILTER_VALUE = /^(due|created|updated|started|completed|canceled|autoClosed|triaged|status|released):(?:([+-])(\d+)([dwmy])|(\d{4}-\d{2}-\d{2})|(\d{4})(?:-(0[1-9]|1[0-2])|-Q([1-4])|-H([12]))?)$/

/** A calendar period: its first day and the day after its last (`YYYY-MM-DD`, end exclusive). */
export interface DatePeriod { unit: DatePeriodUnit; year: number; index: number; start: string; end: string }
export interface ParsedDateValue { kind: DateFilterKind; days?: number; date?: string; period?: DatePeriod }

function pad(value: number) { return String(value).padStart(2, '0') }
/** Calendar arithmetic on `YYYY-MM-DD` without time zones. */
export function addDays(day: string, days: number) {
  const date = new Date(Date.UTC(Number(day.slice(0, 4)), Number(day.slice(5, 7)) - 1, Number(day.slice(8, 10)) + days))
  return `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())}`
}
function monthStart(year: number, month: number) {
  const date = new Date(Date.UTC(year, month - 1, 1))
  return `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1)}-01`
}

/** The period a unit, year and index (month 1–12, quarter 1–4, half 1–2, year 1) stand for. */
export function datePeriod(unit: DatePeriodUnit, year: number, index = 1): DatePeriod {
  const months = unit === 'month' ? 1 : unit === 'quarter' ? 3 : unit === 'half' ? 6 : 12
  const first = unit === 'year' ? 1 : (index - 1) * months + 1
  return { unit, year, index: unit === 'year' ? 1 : index, start: monthStart(year, first), end: monthStart(year, first + months) }
}

/** The stored value part of a period (`2022-02`, `2026-Q4`, `2026-H2`, `2026`). */
export function datePeriodValue(period: Pick<DatePeriod, 'unit' | 'year' | 'index'>) {
  if (period.unit === 'month') return `${period.year}-${pad(period.index)}`
  if (period.unit === 'quarter') return `${period.year}-Q${period.index}`
  if (period.unit === 'half') return `${period.year}-H${period.index}`
  return String(period.year)
}

export function parseDateFilterValue(value: string): ParsedDateValue | undefined {
  const match = value.match(DATE_FILTER_VALUE)
  if (!match) return
  const kind = match[1] as DateFilterKind
  if (match[5]) return { kind, date: match[5] }
  if (match[6]) {
    const year = Number(match[6])
    const period = match[7] ? datePeriod('month', year, Number(match[7])) : match[8] ? datePeriod('quarter', year, Number(match[8])) : match[9] ? datePeriod('half', year, Number(match[9])) : datePeriod('year', year)
    return { kind, period }
  }
  return { kind, days: (match[2] === '-' ? -1 : 1) * Number(match[3]) * UNIT_DAYS[match[4]] }
}

/** Local calendar day `YYYY-MM-DD` (due dates are calendar days, not instants). */
export function localDay(time: number) {
  const date = new Date(time)
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

/** The comparison point of a preset: a calendar day for due dates, an ISO instant for timestamps. */
export function dateFilterThreshold(parsed: ParsedDateValue, now: number): string {
  if (parsed.kind === 'due') return parsed.date ?? parsed.period?.start ?? localDay(now + (parsed.days ?? 0) * DAY)
  if (parsed.date) return new Date(`${parsed.date}T00:00:00`).toISOString()
  if (parsed.period) return new Date(`${parsed.period.start}T00:00:00`).toISOString()
  return new Date(now + (parsed.days ?? 0) * DAY).toISOString()
}

/**
 * The calendar days a custom day or period covers for an operator: `from` inclusive, `until` exclusive
 * (on Nov 5 = [Nov 5, Nov 6), after Nov 5 = [Nov 5, ∞), before Nov 5 = (∞, Nov 5), within Feb 2022 = [Feb 1, Mar 1)).
 * Presets have no bounds: they compare against `dateFilterThreshold`.
 */
export function dateFilterBounds(parsed: ParsedDateValue, operator: DateFilterOperator): { from?: string; until?: string } | undefined {
  if (parsed.period) return { from: parsed.period.start, until: parsed.period.end }
  if (!parsed.date) return
  if (operator === 'before') return { until: parsed.date }
  if (operator === 'after') return { from: parsed.date }
  return { from: parsed.date, until: addDays(parsed.date, 1) }
}

/** Row/server field compared for each kind ("releasedAt" matches any of the issue's released releases). */
export const DATE_FILTER_FIELDS: Record<DateFilterKind, 'dueDate' | 'createdAt' | 'updatedAt' | 'startedAt' | 'completedAt' | 'canceledAt' | 'autoClosedAt' | 'triagedAt' | 'statusChangedAt' | 'releasedAt'> = {
  due: 'dueDate', created: 'createdAt', updated: 'updatedAt', started: 'startedAt', completed: 'completedAt', canceled: 'canceledAt', autoClosed: 'autoClosedAt', triaged: 'triagedAt', status: 'statusChangedAt', released: 'releasedAt',
}

/**
 * The server conditions for a value: presets compare before/after their threshold; days and periods become an
 * inclusive lower and exclusive upper bound, written with the strict before/after every query path supports
 * (a calendar day after the previous day; an instant after the millisecond before local midnight).
 */
export function dateFilterQueryConditions(parsed: ParsedDateValue, operator: DateFilterOperator, now: number): { operator: 'before' | 'after'; value: string }[] {
  const bounds = dateFilterBounds(parsed, operator)
  if (!bounds) return [{ operator: operator === 'before' ? 'before' : 'after', value: dateFilterThreshold(parsed, now) }]
  const conditions: { operator: 'before' | 'after'; value: string }[] = []
  const midnight = (day: string) => new Date(`${day}T00:00:00`).getTime()
  if (bounds.from) conditions.push({ operator: 'after', value: parsed.kind === 'due' ? addDays(bounds.from, -1) : new Date(midnight(bounds.from) - 1).toISOString() })
  if (bounds.until) conditions.push({ operator: 'before', value: parsed.kind === 'due' ? bounds.until : new Date(midnight(bounds.until)).toISOString() })
  return conditions
}

/** One date compared to the value; several dates (an issue's releases) match when any does. */
export function compareDateFilter(actual: string | readonly string[] | undefined, parsed: ParsedDateValue, operator: DateFilterOperator, now: number): boolean {
  if (Array.isArray(actual)) return actual.some(item => compareDateFilter(item, parsed, operator, now))
  if (!actual || typeof actual !== 'string') return false
  const bounds = dateFilterBounds(parsed, operator)
  if (bounds) {
    if (parsed.kind === 'due') {
      const day = actual.slice(0, 10)
      return (!bounds.from || day >= bounds.from) && (!bounds.until || day < bounds.until)
    }
    const time = Date.parse(actual)
    if (!Number.isFinite(time)) return false
    return (!bounds.from || time >= new Date(`${bounds.from}T00:00:00`).getTime()) && (!bounds.until || time < new Date(`${bounds.until}T00:00:00`).getTime())
  }
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
 * `withDateFilterCounts` adds the reference app's per-option match counts.
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
    past('canceled-date', 'canceled', 'Canceled date'),
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

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/**
 * A custom day or period as the reference app shows it in a chip: "Nov 5" (this year), "Nov 5, 2025",
 * "Feb 2022", "Q4 2026", "H2 2026", "2026"; Chinese "11月5日", "2025年11月5日", "2022年2月", "2026年第4季度",
 * "2026年下半年", "2026年".
 */
export function customDateLabel(parsed: Pick<ParsedDateValue, 'date' | 'period'>, now = Date.now(), locale = 'en-US'): string | undefined {
  const zh = locale.startsWith('zh'), thisYear = new Date(now).getFullYear()
  if (parsed.date) {
    const year = Number(parsed.date.slice(0, 4)), month = Number(parsed.date.slice(5, 7)), day = Number(parsed.date.slice(8, 10))
    if (zh) return `${year === thisYear ? '' : `${year}年`}${month}月${day}日`
    return `${MONTHS[month - 1]} ${day}${year === thisYear ? '' : `, ${year}`}`
  }
  const period = parsed.period
  if (!period) return
  if (period.unit === 'month') return zh ? `${period.year}年${period.index}月` : `${MONTHS[period.index - 1]} ${period.year}`
  if (period.unit === 'quarter') return zh ? `${period.year}年第${period.index}季度` : `Q${period.index} ${period.year}`
  if (period.unit === 'half') return zh ? `${period.year}年${period.index === 1 ? '上' : '下'}半年` : `H${period.index} ${period.year}`
  return zh ? `${period.year}年` : String(period.year)
}

/** Display label for a stored dates value (custom days and periods as `customDateLabel`, typed durations their amount). */
export function dateFilterValueLabel(value: string, now = Date.now(), locale = 'en-US'): string | undefined {
  const parsed = parseDateFilterValue(value)
  if (!parsed) return
  if (parsed.date || parsed.period) return customDateLabel(parsed, now, locale)
  const offset = value.slice(value.indexOf(':') + 1)
  const table = parsed.kind === 'status' ? DURATIONS : parsed.kind === 'due' ? FUTURE : PAST
  const preset = table.find(([id]) => id === offset)?.[1]
  if (preset) return preset
  const amount = amountLabel(Number(offset.slice(1, -1)), offset.slice(-1))
  return parsed.kind === 'status' ? amount : offset.startsWith('+') ? `${amount} from now` : `${amount} ago`
}

/** Custom days and periods (the dialog's values), as opposed to presets. */
export function isCustomDateValue(value: string) {
  const parsed = parseDateFilterValue(value)
  return Boolean(parsed && (parsed.date || parsed.period))
}

/** The date fields a dates value can read from a loaded issue. */
export type DateFilterRecord = Partial<Record<Exclude<(typeof DATE_FILTER_FIELDS)[DateFilterKind], 'releasedAt'>, string | null>> & { releasedDates?: readonly string[] }

/** Whether one record matches a Dates option the way the issue list filters it (the menu's match counts). */
export function dateOptionMatches(record: DateFilterRecord, value: string, now: number, operator?: DateFilterOperator): boolean | undefined {
  const parsed = parseDateFilterValue(value)
  if (parsed) {
    const actual = parsed.kind === 'released' ? record.releasedDates : record[DATE_FILTER_FIELDS[parsed.kind] as keyof DateFilterRecord] as string | null | undefined
    return compareDateFilter(actual ?? undefined, parsed, operator ?? defaultDateFilterOperator(value), now)
  }
  if (value === 'no-due-date') return !record.dueDate
  if (value === 'no-completed-date') return !record.completedAt
  if (value === 'no-released-date') return !record.releasedDates?.length
  if (value === 'overdue') return Boolean(record.dueDate && record.dueDate.slice(0, 10) < localDay(now))
  return undefined
}

/** Default comparison for a dates value: future presets read "before", past presets "after", periods "within". */
export function defaultDateFilterOperator(value: string): DateFilterOperator {
  const match = value.match(/^(\w+):([+-])?(\d{4})?(-\d{2}-\d{2})?/)
  if (!match) return 'after'
  if (match[1] === 'status') return 'before'
  if (match[2]) return match[2] === '+' ? 'before' : 'after'
  if (match[3] && !match[4]) return 'within'
  return match[1] === 'due' ? 'before' : 'after'
}

/** The Dates menu with each value option's match count over the loaded issues (the reference app's "11 issues"). */
export function withDateFilterCounts(options: MyIssuesFilterOption[], records: readonly DateFilterRecord[], now = Date.now()): MyIssuesFilterOption[] {
  return options.map(option => {
    if (option.children) return { ...option, children: withDateFilterCounts(option.children, records, now) }
    if (option.kind !== 'dueDate') return option
    if (dateOptionMatches({}, option.id, now) === undefined) return option
    return { ...option, count: records.reduce((sum, record) => sum + (dateOptionMatches(record, option.id, now) ? 1 : 0), 0) }
  })
}

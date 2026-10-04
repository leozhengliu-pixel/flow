/**
 * Recurring issue schedules. The API stores `issue.recurrence` as a preset
 * (daily, weekdays, weekly, biweekly, monthly, yearly) or an RRULE subset:
 *   FREQ=DAILY|WEEKLY|MONTHLY|YEARLY [;INTERVAL=n] [;BYDAY=MO,WE | 2TU | -1FR]
 *   [;BYMONTHDAY=n|-1] [;BYMONTH=1-12]
 * Presets without a fixed day repeat on the next occurrence's weekday/day.
 */

export type RecurrenceFrequency = 'daily' | 'weekly' | 'monthly' | 'yearly'
export type RecurrenceMonthMode = 'day' | 'weekday'

export interface RecurrenceSchedule {
  frequency: RecurrenceFrequency
  interval: number
  /** Weekly: 0 (Sun) – 6 (Sat). */
  weekdays: number[]
  /** Monthly/yearly: repeat on a day of the month or on the Nth weekday. */
  monthMode: RecurrenceMonthMode
  /** 1–31, or -1 for the last day of the month. */
  monthDay: number
  /** 1–4, or -1 for the last weekday of the month. */
  ordinal: number
  weekday: number
  /** Yearly: 1–12. */
  month: number
}

type Translate = (text: string) => string

export const RECURRENCE_PRESETS = ['daily', 'weekdays', 'weekly', 'biweekly', 'monthly', 'yearly'] as const
export const MAX_RECURRENCE_INTERVAL = 99
const CODES = ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA']
const WORKDAYS = [1, 2, 3, 4, 5]

const byMonday = (a: number, b: number) => ((a + 6) % 7) - ((b + 6) % 7)

/**
 * Calendar date (local midnight) for a YYYY-MM-DD string or an instant. Pass the
 * team timezone for API instants: occurrences are team-local midnights.
 */
export function recurrenceDate(value?: string | Date, timeZone?: string): Date {
  if (typeof value === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(value)) {
    const [year, month, day] = value.split('-').map(Number)
    return new Date(year, month - 1, day)
  }
  const instant = value instanceof Date ? value : value ? new Date(value) : new Date()
  if (timeZone) {
    try {
      return recurrenceDate(new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(instant))
    } catch {
      // Unknown zone: fall back to the browser's calendar.
    }
  }
  return new Date(instant.getFullYear(), instant.getMonth(), instant.getDate())
}

export function toDateInput(date: Date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

/** The ordinal week of a date within its month (5th weeks count as last). */
export function weekdayOrdinal(date: Date) {
  const ordinal = Math.ceil(date.getDate() / 7)
  const lastDay = new Date(date.getFullYear(), date.getMonth() + 1, 0).getDate()
  return date.getDate() + 7 > lastDay ? -1 : Math.min(ordinal, 4)
}

export function defaultRecurrence(anchor: Date, frequency: RecurrenceFrequency = 'weekly'): RecurrenceSchedule {
  return { frequency, interval: 1, weekdays: [anchor.getDay()], monthMode: 'day', monthDay: anchor.getDate(), ordinal: weekdayOrdinal(anchor), weekday: anchor.getDay(), month: anchor.getMonth() + 1 }
}

/** Parses a stored schedule; presets are anchored on `anchor`. */
export function parseRecurrence(value: string | undefined | null, anchor: Date = recurrenceDate()): RecurrenceSchedule | null {
  const raw = (value ?? '').trim()
  if (!raw) return null
  const base = defaultRecurrence(anchor)
  switch (raw.toLowerCase()) {
    case 'daily': return { ...base, frequency: 'daily' }
    case 'weekdays': return { ...base, frequency: 'weekly', weekdays: [...WORKDAYS] }
    case 'weekly': return { ...base, frequency: 'weekly' }
    case 'biweekly': return { ...base, frequency: 'weekly', interval: 2 }
    case 'monthly': return { ...base, frequency: 'monthly' }
    case 'yearly': return { ...base, frequency: 'yearly' }
  }
  const fields = new Map(raw.toUpperCase().replace(/^RRULE:/, '').split(';').map(part => part.split('=') as [string, string]))
  const frequency = fields.get('FREQ')?.toLowerCase()
  if (frequency !== 'daily' && frequency !== 'weekly' && frequency !== 'monthly' && frequency !== 'yearly') return null
  const schedule: RecurrenceSchedule = { ...base, frequency, interval: Math.min(MAX_RECURRENCE_INTERVAL, Math.max(1, Number(fields.get('INTERVAL') ?? 1) || 1)) }
  const days = (fields.get('BYDAY') ?? '').split(',').filter(Boolean)
  const ordinalDay = days.map(day => /^(-?\d)([A-Z]{2})$/.exec(day)).find(Boolean)
  if (ordinalDay) {
    schedule.monthMode = 'weekday'
    schedule.ordinal = Number(ordinalDay[1])
    schedule.weekday = Math.max(0, CODES.indexOf(ordinalDay[2]))
  } else if (days.length) {
    schedule.weekdays = days.map(day => CODES.indexOf(day)).filter(day => day >= 0).sort(byMonday)
  }
  if (fields.has('BYMONTHDAY')) {
    schedule.monthMode = 'day'
    schedule.monthDay = Number(fields.get('BYMONTHDAY')) || base.monthDay
  }
  if (fields.has('BYMONTH')) schedule.month = Number(fields.get('BYMONTH')) || base.month
  return schedule
}

/** Canonical stored value; simple schedules keep their preset names. */
export function serializeRecurrence(schedule: RecurrenceSchedule): string {
  const interval = Math.min(MAX_RECURRENCE_INTERVAL, Math.max(1, Math.round(schedule.interval) || 1))
  if (schedule.frequency === 'daily') return interval === 1 ? 'daily' : `FREQ=DAILY;INTERVAL=${interval}`
  const parts = [`FREQ=${schedule.frequency.toUpperCase()}`]
  if (interval > 1) parts.push(`INTERVAL=${interval}`)
  if (schedule.frequency === 'weekly') {
    const days = [...new Set(schedule.weekdays)].sort(byMonday)
    if (interval === 1 && days.join() === WORKDAYS.join()) return 'weekdays'
    parts.push(`BYDAY=${(days.length ? days : [1]).map(day => CODES[day]).join(',')}`)
    return parts.join(';')
  }
  if (schedule.frequency === 'yearly') parts.push(`BYMONTH=${schedule.month}`)
  parts.push(schedule.monthMode === 'weekday' ? `BYDAY=${schedule.ordinal}${CODES[schedule.weekday]}` : `BYMONTHDAY=${schedule.monthDay}`)
  return parts.join(';')
}

function monthOccurrence(schedule: RecurrenceSchedule, year: number, month: number) {
  const last = new Date(year, month + 1, 0).getDate()
  if (schedule.monthMode === 'weekday') {
    if (schedule.ordinal < 0) {
      const date = new Date(year, month, last)
      date.setDate(last - ((date.getDay() - schedule.weekday + 7) % 7))
      return date
    }
    const first = new Date(year, month, 1)
    return new Date(year, month, 1 + ((schedule.weekday - first.getDay() + 7) % 7) + 7 * (schedule.ordinal - 1))
  }
  return new Date(year, month, schedule.monthDay < 0 || schedule.monthDay > last ? last : schedule.monthDay)
}

/** First date on or after `from` matching the schedule (mirrors the API). */
export function firstRecurrenceOnOrAfter(schedule: RecurrenceSchedule, from: Date): Date {
  const start = recurrenceDate(from)
  if (schedule.frequency === 'weekly') {
    const days = schedule.weekdays.length ? schedule.weekdays : [start.getDay()]
    for (let offset = 0; offset < 7; offset++) {
      const date = new Date(start.getFullYear(), start.getMonth(), start.getDate() + offset)
      if (days.includes(date.getDay())) return date
    }
  }
  if (schedule.frequency === 'monthly') {
    const date = monthOccurrence(schedule, start.getFullYear(), start.getMonth())
    return date >= start ? date : monthOccurrence(schedule, start.getFullYear(), start.getMonth() + 1)
  }
  if (schedule.frequency === 'yearly') {
    for (let year = start.getFullYear(); year <= start.getFullYear() + 8; year++) {
      const date = monthOccurrence(schedule, year, schedule.month - 1)
      if (date >= start) return date
    }
  }
  return start
}

const ordinalLabels: Record<number, string> = { 1: 'first', 2: 'second', 3: 'third', 4: 'fourth', [-1]: 'last' }

function phrase(t: Translate, template: string, values: Record<string, string | number>) {
  return t(template).replace(/\{(\w+)\}/g, (_, key: string) => String(values[key] ?? ''))
}

/** Human summary, e.g. "Every 2 weeks on Mon, Fri" or "Monthly on the last Friday". */
export function describeRecurrence(value: string | undefined | null, options: { t?: Translate; locale?: string; anchor?: Date } = {}): string {
  const t = options.t ?? ((text: string) => text)
  const schedule = parseRecurrence(value, options.anchor)
  if (!schedule) return ''
  const weekdayName = (day: number, width: 'short' | 'long' = 'short') => new Intl.DateTimeFormat(options.locale, { weekday: width }).format(new Date(2024, 0, 7 + day))
  const monthName = (month: number) => new Intl.DateTimeFormat(options.locale, { month: 'short' }).format(new Date(2024, month - 1, 1))
  const n = schedule.interval
  if (schedule.frequency === 'daily') return n === 1 ? t('Every day') : phrase(t, 'Every {n} days', { n })
  if (schedule.frequency === 'weekly') {
    const days = [...schedule.weekdays].sort(byMonday)
    if (n === 1 && days.join() === WORKDAYS.join()) return t('Every weekday')
    const list = days.map(day => weekdayName(day)).join(', ')
    return n === 1 ? phrase(t, 'Weekly on {days}', { days: list }) : phrase(t, 'Every {n} weeks on {days}', { n, days: list })
  }
  const when = schedule.monthMode === 'weekday'
    ? phrase(t, `on the ${ordinalLabels[schedule.ordinal] ?? 'first'} {weekday}`, { weekday: weekdayName(schedule.weekday, 'long') })
    : schedule.monthDay < 0 ? t('on the last day') : phrase(t, 'on day {day}', { day: schedule.monthDay })
  if (schedule.frequency === 'monthly') return n === 1 ? phrase(t, 'Monthly {when}', { when }) : phrase(t, 'Every {n} months {when}', { n, when })
  const yearly = schedule.monthMode === 'weekday' ? phrase(t, '{when} of {month}', { when, month: monthName(schedule.month) }) : schedule.monthDay < 0 ? phrase(t, 'on the last day of {month}', { month: monthName(schedule.month) }) : phrase(t, 'on {month} {day}', { month: monthName(schedule.month), day: schedule.monthDay })
  return n === 1 ? phrase(t, 'Yearly {when}', { when: yearly }) : phrase(t, 'Every {n} years {when}', { n, when: yearly })
}

/**
 * Preset menu entries for quick "Make recurring" choices. Values follow Linear's
 * "repeats every N unit" model: they carry no day parts and are anchored on the
 * issue's first due date (`anchor`), which only shapes the labels.
 */
export function recurrencePresetOptions(anchor: Date, t: Translate = text => text, locale?: string) {
  const weekday = new Intl.DateTimeFormat(locale, { weekday: 'long' }).format(anchor)
  return [
    { value: 'daily', label: t('Daily') },
    { value: 'weekdays', label: t('Every weekday (Mon–Fri)') },
    { value: simpleRecurrence('weekly', 1), label: phrase(t, 'Weekly on {weekday}', { weekday }) },
    { value: simpleRecurrence('weekly', 2), label: phrase(t, 'Every 2 weeks on {weekday}', { weekday }) },
    { value: simpleRecurrence('monthly', 1), label: describeRecurrence(simpleRecurrence('monthly', 1), { t, locale, anchor }) },
    { value: simpleRecurrence('yearly', 1), label: describeRecurrence(simpleRecurrence('yearly', 1), { t, locale, anchor }) },
  ]
}

/** Linear's "repeats every N unit" schedule: FREQ + INTERVAL, anchored on the due date. */
export function simpleRecurrence(frequency: RecurrenceFrequency, interval = 1): string {
  const n = Math.min(MAX_RECURRENCE_INTERVAL, Math.max(1, Math.round(interval) || 1))
  return `FREQ=${frequency.toUpperCase()}${n > 1 ? `;INTERVAL=${n}` : ''}`
}

/** The schedule's day parts re-anchored on `anchor` (what a simple schedule means). */
export function anchorRecurrence(schedule: RecurrenceSchedule, anchor: Date): RecurrenceSchedule {
  return { ...defaultRecurrence(anchor, schedule.frequency), interval: schedule.interval }
}

/** True when the schedule repeats on the anchor's own weekday / day / date (no custom day parts). */
export function isAnchoredRecurrence(schedule: RecurrenceSchedule, anchor: Date): boolean {
  if (schedule.frequency === 'daily') return true
  if (schedule.frequency === 'weekly') return schedule.weekdays.length === 1 && schedule.weekdays[0] === anchor.getDay()
  const dayMatches = schedule.monthMode === 'day' && schedule.monthDay === anchor.getDate()
  return schedule.frequency === 'monthly' ? dayMatches : dayMatches && schedule.month === anchor.getMonth() + 1
}

/** Stored value for a schedule anchored on `firstDue`: the simple form when it has no custom day parts. */
export function serializeRecurrenceFrom(schedule: RecurrenceSchedule, firstDue: Date): string {
  return isAnchoredRecurrence(schedule, firstDue) ? simpleRecurrence(schedule.frequency, schedule.interval) : serializeRecurrence(schedule)
}

export function addDays(date: Date, days: number) {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate() + days)
}

/** Linear's default first due date: one week from today. */
export function defaultFirstDue(today: Date = recurrenceDate()) {
  return addDays(recurrenceDate(today), 7)
}

/**
 * The first due date the API picks for a schedule set without one (mirrors
 * applyRecurrenceUpdate): schedules without day parts ("weekly", FREQ=MONTHLY)
 * repeat on today's weekday / day / date and are first due one period from
 * today; others ("daily", "weekdays", BYDAY lists) on their first date on or
 * after tomorrow.
 */
export function defaultRecurrenceFirstDue(value: string | undefined | null, today: Date = recurrenceDate()): Date | undefined {
  const start = recurrenceDate(today)
  const schedule = parseRecurrence(value, start)
  if (!schedule) return undefined
  const raw = (value ?? '').trim().toUpperCase().replace(/^RRULE:/, '')
  const fields = new Set(raw.split(';').map(part => part.split('=')[0]))
  const needsAnchor = schedule.frequency === 'weekly'
    ? raw !== 'WEEKDAYS' && !fields.has('BYDAY')
    : schedule.frequency === 'monthly'
      ? !fields.has('BYDAY') && !fields.has('BYMONTHDAY')
      : schedule.frequency === 'yearly' && (!fields.has('BYMONTH') || (!fields.has('BYDAY') && !fields.has('BYMONTHDAY')))
  return needsAnchor ? nextRecurrenceDue(schedule, start) : firstRecurrenceOnOrAfter(schedule, addDays(start, 1))
}

/**
 * The due date of the instance after the one due on `due`: the next cadence date
 * (mirrors the API's scheduler, which anchors every period on the previous due date).
 */
export function nextRecurrenceDue(schedule: RecurrenceSchedule, due: Date): Date {
  const n = Math.max(1, schedule.interval)
  if (schedule.frequency === 'daily') return addDays(due, n)
  if (schedule.frequency === 'weekly') {
    const days = (schedule.weekdays.length ? schedule.weekdays : [due.getDay()]).map(day => (day + 6) % 7).sort((a, b) => a - b)
    const offset = (due.getDay() + 6) % 7
    const later = days.find(day => day > offset)
    if (later !== undefined) return addDays(due, later - offset)
    return addDays(due, 7 * n - offset + days[0])
  }
  if (schedule.frequency === 'monthly') {
    const same = monthOccurrence(schedule, due.getFullYear(), due.getMonth())
    return same > due ? same : monthOccurrence(schedule, due.getFullYear(), due.getMonth() + n)
  }
  const same = monthOccurrence(schedule, due.getFullYear(), schedule.month - 1)
  return same > due ? same : monthOccurrence(schedule, due.getFullYear() + n, schedule.month - 1)
}

const UNIT_PHRASES: Record<RecurrenceFrequency, [string, string]> = {
  daily: ['Repeats every day', 'Repeats every {n} days'],
  weekly: ['Repeats every week', 'Repeats every {n} weeks'],
  monthly: ['Repeats every month', 'Repeats every {n} months'],
  yearly: ['Repeats every year', 'Repeats every {n} years'],
}

/** "Repeats every 2 weeks" for anchored schedules, else the detailed summary (e.g. "Every weekday"). */
export function describeRepeats(value: string | undefined | null, options: { t?: Translate; locale?: string; anchor?: Date } = {}): string {
  const t = options.t ?? ((text: string) => text)
  const anchor = options.anchor ?? recurrenceDate()
  const schedule = parseRecurrence(value, anchor)
  if (!schedule) return ''
  if (!isAnchoredRecurrence(schedule, anchor)) return describeRecurrence(value, { ...options, anchor })
  const [one, many] = UNIT_PHRASES[schedule.frequency]
  return schedule.interval === 1 ? t(one) : phrase(t, many, { n: schedule.interval })
}

/**
 * The current due date of a recurring issue. Legacy schedules (created before due
 * dates anchored them) may only carry the next occurrence instant.
 */
export function recurrenceDueDate(issue: { dueDate?: string; nextOccurrenceAt?: string }, timeZone?: string): Date | undefined {
  if (issue.dueDate) return recurrenceDate(issue.dueDate.slice(0, 10))
  return issue.nextOccurrenceAt ? recurrenceDate(issue.nextOccurrenceAt, timeZone) : undefined
}

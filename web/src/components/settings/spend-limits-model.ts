import type { SpendLimitReset, SpendLimitSettings } from '@/types/flow'

export const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'] as const
export const RESET_OPTIONS: { value: SpendLimitReset; label: string }[] = [
  { value: 'daily', label: 'Daily' },
  { value: 'weekly', label: 'Weekly' },
  { value: 'monthly', label: 'Monthly' },
]

/** Defaults of the reference app: daily at 6 AM, weekly on Saturday, monthly on the day the workspace was created. */
export const DEFAULT_RESET_HOUR = 6
export const DEFAULT_RESET_WEEKDAY = 6

export function timeZoneAbbreviation(date = new Date()) {
  try {
    return new Intl.DateTimeFormat('en-US', { timeZoneName: 'short' }).formatToParts(date).find((part) => part.type === 'timeZoneName')?.value ?? 'UTC'
  } catch {
    return 'UTC'
  }
}

export function hourLabel(hour: number) {
  const suffix = hour < 12 ? 'AM' : 'PM'
  return `${hour % 12 || 12} ${suffix}`
}

export function ordinal(day: number) {
  const rest = day % 100
  if (rest >= 11 && rest <= 13) return `${day}th`
  return `${day}${['th', 'st', 'nd', 'rd'][day % 10] ?? 'th'}`
}

export type ResetSchedule = { hour: number; weekday: number; monthDay: number }

export function resetSchedule(limits: SpendLimitSettings | undefined, createdAt?: string): ResetSchedule {
  const created = createdAt ? new Date(createdAt) : undefined
  const fallbackDay = created && !Number.isNaN(created.getTime()) ? Math.min(created.getDate(), 28) : 1
  return {
    hour: limits?.dailyResetHour ?? DEFAULT_RESET_HOUR,
    weekday: limits?.weeklyResetDay ?? DEFAULT_RESET_WEEKDAY,
    monthDay: limits?.monthlyResetDay ?? fallbackDay,
  }
}

export function scheduleSummary(schedule: ResetSchedule, zone: string) {
  return `Daily at ${hourLabel(schedule.hour)} (${zone}) · Weekly on ${WEEKDAYS[schedule.weekday]} · Monthly on the ${ordinal(schedule.monthDay)}`
}

/** "Resets Saturdays at 6 AM (PDT)" for the chosen frequency. */
export function resetsText(reset: SpendLimitReset, schedule: ResetSchedule, zone: string) {
  const time = `${hourLabel(schedule.hour)} (${zone})`
  if (reset === 'daily') return `Resets daily at ${time}`
  if (reset === 'monthly') return `Resets on the ${ordinal(schedule.monthDay)} at ${time}`
  return `Resets ${WEEKDAYS[schedule.weekday]}s at ${time}`
}

/** The cadence the reference app writes into column headers and descriptions ("Limit (Weekly)", "reset weekly on Saturdays at 6 AM"). */
export function resetsPhrase(reset: SpendLimitReset, schedule: ResetSchedule, zone: string) {
  const time = `${hourLabel(schedule.hour)} (${zone})`
  if (reset === 'daily') return `daily at ${time}`
  if (reset === 'monthly') return `monthly on the ${ordinal(schedule.monthDay)} at ${time}`
  return `weekly on ${WEEKDAYS[schedule.weekday]}s at ${time}`
}

export function formatUsd(amount: number | undefined) {
  const value = amount ?? 0
  return `$${value.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

/** "$250" for whole amounts, "$250.50" otherwise (limits are typed by hand, so no forced cents). */
export function formatLimit(amount: number | undefined) {
  if (!amount || amount <= 0) return ''
  return `$${amount.toLocaleString('en-US', { minimumFractionDigits: Number.isInteger(amount) ? 0 : 2, maximumFractionDigits: 2 })}`
}

/** Parses "250", "$250.5" or "1,000"; null when the text is not a non-negative amount. */
export function parseLimitInput(text: string): number | null {
  const cleaned = text.trim().replace(/^\$/, '').replace(/,/g, '')
  if (!/^\d+(\.\d{1,2})?$/.test(cleaned)) return null
  const value = Number(cleaned)
  return Number.isFinite(value) && value <= 1e9 ? value : null
}

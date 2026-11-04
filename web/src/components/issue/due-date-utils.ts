import { parseNaturalDateInput } from '@/lib/natural-date'
import type { Cycle } from '@/types/flow'

export function addDays(date: Date, days: number) { const next = new Date(date); next.setDate(next.getDate() + days); return next }
export function endOfWorkWeek(date: Date) { return addDays(date, (5 - date.getDay() + 7) % 7) }
export function isoDate(date: Date) { const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000); return local.toISOString().slice(0, 10) }

/** "tomorrow", "24h", "3 days", "in 3d", "2026-10-12", "10/28/2026" or "Feb 9" as an ISO date. */
export function parseNaturalDate(input: string) {
  const parsed = parseNaturalDateInput(input)
  return parsed ? isoDate(parsed) : undefined
}

export function formatDate(value: string) { return new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', year: 'numeric' }).format(new Date(`${value}T12:00:00`)) }

export function formatShortDate(value: string) {
  const date = new Date(`${value}T12:00:00`)
  const weekday = new Intl.DateTimeFormat('en-GB', { weekday: 'short' }).format(date)
  const dayAndMonth = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' }).format(date)
  return `${weekday}, ${dayAndMonth}`
}

/** MM/DD/YYYY, the text the due-date dialog shows in its field. */
export function formatFieldDate(value: string) {
  return new Intl.DateTimeFormat('en-US', { month: '2-digit', day: '2-digit', year: 'numeric' }).format(new Date(`${value}T12:00:00`))
}

/** The due-date list's "End of this cycle" and "End of next cycle" rows for a team's cycles. */
export function cycleDueDateOptions(cycles: Cycle[], teamId: string, nowMs: number, t: (key: string) => string) {
  const team = cycles.filter(cycle => cycle.teamId === teamId).sort((a, b) => a.startsAt.localeCompare(b.startsAt))
  const current = team.find(cycle => new Date(cycle.startsAt).getTime() <= nowMs && new Date(cycle.endsAt).getTime() > nowMs)
  const next = team.find(cycle => new Date(cycle.startsAt).getTime() > nowMs)
  return [current && { label: t('End of this cycle'), value: current.endsAt.slice(0, 10) }, next && { label: t('End of next cycle'), value: next.endsAt.slice(0, 10) }].filter((item): item is { label: string; value: string } => Boolean(item))
}

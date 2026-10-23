export function addDays(date: Date, days: number) { const next = new Date(date); next.setDate(next.getDate() + days); return next }
export function endOfWorkWeek(date: Date) { return addDays(date, (5 - date.getDay() + 7) % 7) }
export function isoDate(date: Date) { const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000); return local.toISOString().slice(0, 10) }

/** "tomorrow", "24h", "3 days", "in 3d", "2026-10-12", "10/28/2026" or "Feb 9" as an ISO date. */
export function parseNaturalDate(input: string) {
  const value = input.trim().toLowerCase()
  if (!value) return undefined
  if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return value
  if (value === 'tomorrow' || value === '24h') return isoDate(addDays(new Date(), 1))
  const days = value.match(/^(?:in\s+)?(\d+)\s*d(?:ays?)?$/)
  if (days) return isoDate(addDays(new Date(), Number(days[1])))
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? undefined : isoDate(parsed)
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

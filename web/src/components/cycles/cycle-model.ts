import type { Cycle, Issue, TeamSettings } from '@/types/flow'
import { estimatePoints, teamUsesEstimates } from '@/lib/estimates'

export interface CycleStats {
  /** True when scope, started and completed are estimate points rather than issue counts. */
  estimates: boolean
  scope: number
  started: number
  completed: number
  capacity: number
  /** scope / capacity, unrounded. */
  capacityRatio: number
  capacityPercent: number
  successPercent: number
  startedPercent: number
  completedPercent: number
}

type CycleEstimateSettings = Partial<Pick<TeamSettings, 'estimateType' | 'estimateAllowZero' | 'estimateCountUnestimated'>> | undefined

/**
 * Scope and capacity of a cycle.
 *
 * Scope is the sum of estimate points of the cycle's (non-archived) issues when
 * the team uses estimates — unestimated issues count as 1 point unless "Count
 * unestimated issues" is off, and a 0 estimate counts as 0 only when zero
 * estimates are allowed — otherwise it is the number of issues.
 *
 * Capacity comes from the server, in the same unit as scope: the override typed
 * in Edit cycle, else the team's velocity (average completed scope of its last
 * three completed cycles) or, without history, 30 per active team member.
 * Capacity % = scope / capacity.
 */
export function cycleStats(cycle: Cycle, issues: Issue[], settings?: CycleEstimateSettings): CycleStats {
  const scoped = issues.filter(issue => issue.cycleId === cycle.id && !issue.archivedAt)
  const estimates = teamUsesEstimates(settings)
  const weight = (issue: Issue) => estimates ? estimatePoints(issue.estimate, settings) : 1
  const total = (items: Issue[]) => items.reduce((sum, issue) => sum + weight(issue), 0)
  const completed = total(scoped.filter(issue => issue.state.type === 'completed'))
  const started = total(scoped.filter(issue => issue.state.type === 'started'))
  const scope = total(scoped)
  const capacity = Math.max(cycle.capacity, 1)
  return {
    estimates,
    scope,
    started,
    completed,
    capacity: cycle.capacity,
    capacityRatio: scope / capacity,
    capacityPercent: Math.round(scope / capacity * 100),
    successPercent: scope ? Math.round((completed + started * .25) / scope * 100) : 0,
    startedPercent: scope ? Math.round(started / scope * 100) : 0,
    completedPercent: scope ? Math.round(completed / scope * 100) : 0,
  }
}

// Cycle boundaries are calendar days stored as UTC midnights; format and count
// them in UTC so the viewer's timezone never shifts a cycle by a day.
export function formatCycleRange(cycle: Cycle, compact = false) {
  const formatter = new Intl.DateTimeFormat('en-US', compact ? { month: 'short', day: 'numeric', timeZone: 'UTC' } : { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' })
  return `${formatter.format(new Date(cycle.startsAt))} – ${formatter.format(new Date(cycle.endsAt))}`
}

export function formatCycleDay(value: string) {
  const date = new Date(value)
  return { month: new Intl.DateTimeFormat('en-US', { month: 'short', timeZone: 'UTC' }).format(date), day: date.getUTCDate() }
}

/** Weekdays from the viewer's today through the cycle's last calendar day, inclusive. */
export function weekdaysLeft(cycle: Cycle) {
  const now = new Date()
  const end = new Date(cycle.endsAt)
  const cursor = new Date(Date.UTC(now.getFullYear(), now.getMonth(), now.getDate()))
  const last = Date.UTC(end.getUTCFullYear(), end.getUTCMonth(), end.getUTCDate())
  let days = 0
  while (cursor.getTime() <= last) {
    if (cursor.getUTCDay() !== 0 && cursor.getUTCDay() !== 6) days++
    cursor.setUTCDate(cursor.getUTCDate() + 1)
  }
  return days
}

/**
 * The cycles list order: latest start first. A cycle completed on the day the next one started
 * shares its start day, so the higher number (the later cycle) goes first.
 */
export function cycleListOrder<T extends Pick<Cycle, 'startsAt' | 'number'>>(cycles: T[]) {
  return [...cycles].sort((a, b) => b.startsAt.slice(0, 10).localeCompare(a.startsAt.slice(0, 10)) || b.number - a.number)
}

const DAY = 86_400_000

/**
 * The cooldown row between a cycle and the next one, in weeks (0: no row). It counts the
 * Sunday-to-Saturday calendar weeks from the day after the earlier cycle's last day to the
 * later cycle's first day, so 6 days from a Monday to a Sunday is 1 week and 31 days from a
 * Friday to a Monday is 5. Cycle dates are inclusive calendar days.
 */
export function cooldownWeeksBetween(earlier: Pick<Cycle, 'endsAt'>, later: Pick<Cycle, 'startsAt'>) {
  const end = Date.parse(earlier.endsAt.slice(0, 10)), start = Date.parse(later.startsAt.slice(0, 10))
  if (!Number.isFinite(end) || !Number.isFinite(start)) return 0
  const after = end + DAY
  if (start <= after) return 0
  const sunday = (time: number) => time - new Date(time).getUTCDay() * DAY
  return Math.round((sunday(start) - sunday(after)) / (7 * DAY))
}

/** An ISO calendar day (YYYY-MM-DD) moved by a number of days. */
export function addCycleDays(value: string, days: number) {
  return new Date(Date.parse(value.slice(0, 10)) + days * DAY).toISOString().slice(0, 10)
}

/** Today as a cycle day: the server dates cycles in UTC days. */
export function cycleToday(now = new Date()) {
  return now.toISOString().slice(0, 10)
}

type ScheduledCycle = Pick<Cycle, 'id' | 'number' | 'status' | 'startsAt' | 'endsAt'>

/** The team's cycles just before and just after a cycle (by start day, then number). */
export function cycleNeighbors<T extends ScheduledCycle>(cycles: T[], cycle: ScheduledCycle) {
  const ordered = [...cycles].sort((a, b) => a.startsAt.slice(0, 10).localeCompare(b.startsAt.slice(0, 10)) || a.number - b.number)
  const index = ordered.findIndex(item => item.id === cycle.id)
  return { previous: index > 0 ? ordered[index - 1] : undefined, next: index >= 0 ? ordered[index + 1] : undefined }
}

/** "Start cycle today…" is offered on the team's next upcoming cycle while no cycle is current. */
export function canStartCycleToday(cycles: ScheduledCycle[], cycle: ScheduledCycle) {
  if (cycle.status !== 'upcoming' || cycles.some(item => item.status === 'current')) return false
  const next = cycles.filter(item => item.status === 'upcoming').sort((a, b) => a.startsAt.localeCompare(b.startsAt) || a.number - b.number)[0]
  return next?.id === cycle.id
}

/**
 * The days a cycle's start or end can move to (inclusive ISO days; max undefined: no limit).
 * The cycle keeps at least two days; the next cycle keeps its last day; the previous cycle keeps
 * two days; nothing starts before today and the current cycle does not end before today. Mirrors
 * api/cmd/server/cycle_dates.go.
 */
export function cycleDateBounds(cycles: ScheduledCycle[], cycle: ScheduledCycle, mode: 'start' | 'end', today = cycleToday()) {
  const { previous, next } = cycleNeighbors(cycles, cycle)
  const later = (a: string, b: string) => a > b ? a : b
  if (mode === 'end') {
    let min = addCycleDays(cycle.startsAt, 1)
    if (cycle.status === 'current') min = later(min, today)
    const max = next && next.status !== 'completed' ? addCycleDays(next.endsAt, -1) : undefined
    return { min, max }
  }
  let min = today
  if (previous) min = later(min, previous.status === 'completed' ? addCycleDays(previous.endsAt, 1) : addCycleDays(previous.startsAt, 2))
  return { min, max: undefined }
}

/** The neighbour a date move also changes: the next cycle's start, or the previous cycle's end. */
export function cycleDateMoveAffects<T extends ScheduledCycle>(cycles: T[], cycle: ScheduledCycle, mode: 'start' | 'end', value: string): T | undefined {
  const { previous, next } = cycleNeighbors(cycles, cycle)
  if (mode === 'end') return next && next.status !== 'completed' && value >= next.startsAt.slice(0, 10) ? next : undefined
  return previous && previous.status !== 'completed' && value <= previous.endsAt.slice(0, 10) ? previous : undefined
}

export function cycleStatusLabel(status: Cycle['status']) {
  return status[0].toUpperCase() + status.slice(1)
}


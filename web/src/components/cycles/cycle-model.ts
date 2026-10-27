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
 * Whole weeks of cooldown between a cycle and the next one (0 when they are
 * back to back). Cycle dates are inclusive calendar days.
 */
export function cooldownWeeksBetween(earlier: Pick<Cycle, 'endsAt'>, later: Pick<Cycle, 'startsAt'>) {
  const day = 86_400_000
  const end = Date.parse(earlier.endsAt.slice(0, 10)), start = Date.parse(later.startsAt.slice(0, 10))
  if (!Number.isFinite(end) || !Number.isFinite(start)) return 0
  const gapDays = Math.round((start - end) / day) - 1
  return gapDays >= 7 ? Math.round(gapDays / 7) : 0
}

export function cycleStatusLabel(status: Cycle['status']) {
  return status[0].toUpperCase() + status.slice(1)
}


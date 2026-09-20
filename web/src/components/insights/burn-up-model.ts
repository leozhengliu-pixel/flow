/**
 * LS-0102 BurnUpGraph + LS-0175 CycleDetailComponents host kernel.
 * Honest per-day Scope / Started / Completed series (no decorative fake progress).
 */

import type { Cycle, Issue } from '@/types/flow'

export type BurnUpMeasure = 'issue_count' | 'estimate'

export type BurnUpDayPoint = {
  date: string // YYYY-MM-DD UTC
  dayIndex: number
  scope: number
  started: number
  completed: number
}

export type BurnUpSeries = {
  days: BurnUpDayPoint[]
  maxValue: number
  scope: number
  started: number
  completed: number
  measure: BurnUpMeasure
  activeTickIndex: number
}

function utcDay(value: string | Date): string {
  const date = value instanceof Date ? value : new Date(value)
  return date.toISOString().slice(0, 10)
}

function addUtcDays(isoDay: string, days: number): string {
  const date = new Date(`${isoDay}T00:00:00.000Z`)
  date.setUTCDate(date.getUTCDate() + days)
  return date.toISOString().slice(0, 10)
}

function eachDayInclusive(start: string, end: string): string[] {
  const days: string[] = []
  let cursor = start
  while (cursor <= end) {
    days.push(cursor)
    cursor = addUtcDays(cursor, 1)
    if (days.length > 400) break
  }
  return days
}

function issueWeight(issue: Issue, measure: BurnUpMeasure): number {
  if (measure === 'estimate') return Math.max(0, Number(issue.estimate ?? 0))
  return 1
}

function issueInScopeOnDay(issue: Issue, day: string, cycleId: string): boolean {
  if (issue.cycleId !== cycleId || issue.archivedAt) return false
  const created = utcDay(issue.createdAt)
  if (created > day) return false
  // Removed-from-cycle after completion still counts historically via completedAt.
  return true
}

function issueStartedByDay(issue: Issue, day: string): boolean {
  if (issue.state.type === 'started' || issue.state.type === 'completed') {
    const started = issue.startedAt ? utcDay(issue.startedAt) : utcDay(issue.updatedAt ?? issue.createdAt)
    return started <= day
  }
  return false
}

function issueCompletedByDay(issue: Issue, day: string): boolean {
  if (issue.state.type === 'completed') {
    const completed = issue.completedAt ? utcDay(issue.completedAt) : utcDay(issue.updatedAt ?? issue.createdAt)
    return completed <= day
  }
  return false
}

/**
 * Fold cycle issues into a per-day burn-up series.
 * Scope grows when issues exist on/before the day; Started/Completed are cumulative.
 */
export function computeBurnUpSeries(
  cycle: Pick<Cycle, 'id' | 'startsAt' | 'endsAt'>,
  issues: Issue[],
  options: { measure?: BurnUpMeasure; asOf?: string | Date } = {},
): BurnUpSeries {
  const measure = options.measure ?? 'issue_count'
  const start = utcDay(cycle.startsAt)
  const end = utcDay(cycle.endsAt)
  const asOf = utcDay(options.asOf ?? new Date())
  const days = eachDayInclusive(start, end)
  const scoped = issues.filter(issue => issue.cycleId === cycle.id && !issue.archivedAt)

  const points: BurnUpDayPoint[] = days.map((date, dayIndex) => {
    let scope = 0
    let started = 0
    let completed = 0
    for (const issue of scoped) {
      if (!issueInScopeOnDay(issue, date, cycle.id)) continue
      // Scope on day: issues created on/before day (and in cycle).
      const created = utcDay(issue.createdAt)
      if (created > date) continue
      const weight = issueWeight(issue, measure)
      scope += weight
      if (issueCompletedByDay(issue, date)) completed += weight
      else if (issueStartedByDay(issue, date)) started += weight
    }
    // Started series includes completed (Linear burn-up "started" line = in progress + done).
    return { date, dayIndex, scope, started: started + completed, completed }
  })

  const last = points.at(-1)
  const activeTickIndex = Math.max(0, Math.min(points.length - 1, days.findIndex(day => day >= asOf) === -1
    ? points.length - 1
    : Math.min(days.findIndex(day => day >= asOf), points.length - 1)))

  return {
    days: points,
    maxValue: Math.max(1, ...points.map(point => Math.max(point.scope, point.started, point.completed))),
    scope: last?.scope ?? 0,
    started: Math.max(0, (last?.started ?? 0) - (last?.completed ?? 0)),
    completed: last?.completed ?? 0,
    measure,
    activeTickIndex: asOf < start ? 0 : asOf > end ? points.length - 1 : activeTickIndex,
  }
}

export function burnUpPath(
  values: number[],
  maxValue: number,
  width: number,
  height: number,
  padLeft = 0,
  padTop = 8,
): string {
  if (!values.length) return ''
  const usable = Math.max(1, maxValue)
  return values.map((value, index) => {
    const x = padLeft + (values.length === 1 ? 0 : (index / (values.length - 1)) * width)
    const y = padTop + height - (Math.min(value, usable) / usable) * height
    return `${index === 0 ? 'M' : 'L'}${x.toFixed(2)} ${y.toFixed(2)}`
  }).join(' ')
}

export function isWeekendUtc(isoDay: string): boolean {
  const day = new Date(`${isoDay}T12:00:00.000Z`).getUTCDay()
  return day === 0 || day === 6
}

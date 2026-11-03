/**
 * Rendering model for the server-built cycle graph (GET /api/cycles/{id}/graph).
 * The server replays issue history into per-day Scope / Started / Completed
 * values; this module derives the reference app's Target line, the active-cycle
 * projection and chart geometry.
 */
import type { CycleGraphData, CycleGraphTotals } from '@/lib/api'

export type CycleGraphMeasure = 'issues' | 'points'

export type CycleGraphSeriesPoint = {
  date: string
  index: number
  /** null for days after today. */
  scope: number | null
  started: number | null
  completed: number | null
  target: number
  /** Projected completion, only from today onward in an active cycle. */
  projected: number | null
}

export type CycleGraphSeries = {
  points: CycleGraphSeriesPoint[]
  maxValue: number
  /** Last day with actual values, -1 before the cycle starts. */
  lastActualIndex: number
  todayIndex: number
  scope: number
  started: number
  completed: number
  added: number
  removed: number
  startedPercent: number
  completedPercent: number
}

/** Days of recent history used for the completion velocity. */
export const PROJECTION_WINDOW = 7

export function measureValue(totals: CycleGraphTotals, key: 'scope' | 'started' | 'completed', measure: CycleGraphMeasure) {
  const value = measure === 'points' ? totals[`${key}Points`] : totals[key]
  return Math.round((value ?? 0) * 100) / 100
}

function percent(part: number, whole: number) {
  return whole > 0 ? Math.round(part / whole * 100) : 0
}

export function cycleGraphSeries(graph: CycleGraphData, measure: CycleGraphMeasure = 'issues'): CycleGraphSeries {
  const days = graph.days
  const lastActualIndex = days.reduce((last, day, index) => day.future ? last : index, -1)
  const todayIndex = graph.today ? days.findIndex(day => day.date === graph.today) : -1
  const scope = measureValue(graph.summary, 'scope', measure)
  const started = measureValue(graph.summary, 'started', measure)
  const completed = measureValue(graph.summary, 'completed', measure)
  const span = Math.max(1, days.length - 1)
  const completedAt = (index: number) => measureValue(days[index], 'completed', measure)
  let velocity = 0
  if (todayIndex >= 0) {
    const from = Math.max(0, todayIndex - PROJECTION_WINDOW)
    const elapsed = todayIndex - from
    velocity = elapsed > 0 ? (completedAt(todayIndex) - completedAt(from)) / elapsed : completedAt(todayIndex)
    velocity = Math.max(0, velocity)
  }
  const points = days.map((day, index): CycleGraphSeriesPoint => {
    const actual = !day.future
    const projected = todayIndex >= 0 && index >= todayIndex && todayIndex < days.length - 1
      ? Math.min(Math.max(scope, completedAt(todayIndex)), completedAt(todayIndex) + velocity * (index - todayIndex))
      : null
    return {
      date: day.date,
      index,
      scope: actual ? measureValue(day, 'scope', measure) : null,
      started: actual ? measureValue(day, 'started', measure) : null,
      completed: actual ? measureValue(day, 'completed', measure) : null,
      target: days.length > 1 ? scope * index / span : scope,
      projected,
    }
  })
  const maxValue = Math.max(1, ...points.flatMap(point => [point.scope ?? 0, point.started ?? 0, point.completed ?? 0, point.target, point.projected ?? 0]))
  return {
    points,
    maxValue,
    lastActualIndex,
    todayIndex,
    scope,
    started,
    completed,
    added: measure === 'points' ? graph.summary.addedPoints : graph.summary.added,
    removed: measure === 'points' ? graph.summary.removedPoints : graph.summary.removed,
    startedPercent: percent(started, scope),
    completedPercent: percent(completed, scope),
  }
}

/** SVG path through the values; nulls break the line. */
export function cycleGraphPath(values: (number | null)[], maxValue: number, width: number, height: number, top = 0): string {
  const span = Math.max(1, values.length - 1)
  const max = Math.max(1, maxValue)
  let pen = false
  const parts: string[] = []
  values.forEach((value, index) => {
    if (value === null) {
      pen = false
      return
    }
    const x = values.length === 1 ? 0 : index / span * width
    const y = top + height - Math.min(value, max) / max * height
    parts.push(`${pen ? 'L' : 'M'}${x.toFixed(2)} ${y.toFixed(2)}`)
    pen = true
  })
  return parts.join(' ')
}

export function cycleGraphX(index: number, count: number, width: number) {
  return count <= 1 ? 0 : index / (count - 1) * width
}

/** Nearest day index for a horizontal position inside the plot. */
export function cycleGraphIndexAt(x: number, count: number, width: number) {
  if (count <= 1 || width <= 0) return 0
  return Math.max(0, Math.min(count - 1, Math.round(x / width * (count - 1))))
}

export function isWeekend(date: string) {
  const day = new Date(`${date}T12:00:00.000Z`).getUTCDay()
  return day === 0 || day === 6
}

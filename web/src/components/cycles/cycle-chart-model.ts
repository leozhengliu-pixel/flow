import type { CycleGraphData } from '@/lib/api'
import { measureValue, type CycleGraphMeasure } from './cycle-graph-model'

const DAY = 86_400_000
/** Ticks closer than this to the previous one are dropped, so narrow charts show fewer weeks. */
const MIN_TICK_GAP = 80

const isoDay = (time: number) => new Date(time).toISOString().slice(0, 10)

/** Inclusive calendar days of a cycle; cycle boundaries are UTC calendar days. */
export function cycleChartDays(startsAt: string, endsAt: string) {
  const start = Date.parse(startsAt.slice(0, 10)), end = Date.parse(endsAt.slice(0, 10))
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return []
  const days: string[] = []
  for (let time = start; time <= end; time += DAY) days.push(isoDay(time))
  return days
}

const weekday = (iso: string) => new Date(`${iso}T12:00:00.000Z`).getUTCDay()
export const isCycleWeekend = (iso: string) => weekday(iso) === 0 || weekday(iso) === 6

/** Runs of consecutive weekend days as [first, end) boundary indices: one band per weekend. */
export function cycleWeekendBands(days: string[]) {
  const bands: [number, number][] = []
  days.forEach((day, index) => {
    if (!isCycleWeekend(day)) return
    const last = bands[bands.length - 1]
    if (last && last[1] === index) last[1] = index + 1
    else bands.push([index, index + 1])
  })
  return bands
}

export type CycleAxisTick = { boundary: number; fraction: number; date: string; kind: 'start' | 'week' | 'end' }

/**
 * Axis ticks on day boundaries: the cycle start, each Monday inside it and the day after the last
 * day. A week tick closer than MIN_TICK_GAP to the one before it, or to the end, is left out.
 */
export function cycleAxisTicks(days: string[], plotWidth: number): CycleAxisTick[] {
  if (!days.length) return []
  const count = days.length
  const first = Date.parse(days[0])
  const at = (boundary: number, kind: CycleAxisTick['kind']): CycleAxisTick => ({ boundary, fraction: boundary / count, date: isoDay(first + boundary * DAY), kind })
  const ticks = [at(0, 'start')]
  const end = at(count, 'end')
  for (let boundary = 1; boundary < count; boundary++) {
    if (weekday(isoDay(first + boundary * DAY)) !== 1) continue
    const tick = at(boundary, 'week')
    if ((tick.fraction - ticks[ticks.length - 1].fraction) * plotWidth < MIN_TICK_GAP) continue
    if ((end.fraction - tick.fraction) * plotWidth < MIN_TICK_GAP) continue
    ticks.push(tick)
  }
  return [...ticks, end]
}

/** Time of day in the cycle's time zone as a share of the day: the "now" marker moves through today. */
export function cycleDayShare(now: Date, timeZone: string) {
  let parts: Intl.DateTimeFormatPart[]
  try { parts = new Intl.DateTimeFormat('en-US', { timeZone, hourCycle: 'h23', hour: 'numeric', minute: 'numeric', second: 'numeric' }).formatToParts(now) } catch { parts = [] }
  const part = (type: string) => Number(parts.find(item => item.type === type)?.value ?? 0)
  return (part('hour') * 3600 + part('minute') * 60 + part('second')) / 86_400
}

export type CycleLegendMetric = { key: 'scope' | 'started' | 'completed'; label: string; count: number | null; percent?: number }

/**
 * The Scope / Started / Completed legend. Without a hovered day it shows the cycle's totals; with
 * one it shows that day's figures, or a dash for a day that has not happened yet.
 */
export function cycleLegendMetrics({ graph, measure, hoveredDate, scope, started, startedPercent, completed, completedPercent }: { graph?: CycleGraphData; measure: CycleGraphMeasure; hoveredDate?: string; scope: number; started: number; startedPercent: number; completed: number; completedPercent: number }): CycleLegendMetric[] {
  if (hoveredDate === undefined) return [
    { key: 'scope', label: 'Scope', count: scope },
    { key: 'started', label: 'Started', count: started, percent: startedPercent },
    { key: 'completed', label: 'Completed', count: completed, percent: completedPercent },
  ]
  const day = graph?.days.find(item => item.date === hoveredDate)
  const value = (key: 'scope' | 'started' | 'completed') => day && !day.future ? measureValue(day, key, measure) : null
  const dayScope = value('scope')
  const share = (count: number | null) => count === null || !dayScope ? 0 : Math.round(count / dayScope * 100)
  return [
    { key: 'scope', label: 'Scope', count: dayScope },
    { key: 'started', label: 'Started', count: value('started'), percent: share(value('started')) },
    { key: 'completed', label: 'Completed', count: value('completed'), percent: share(value('completed')) },
  ]
}

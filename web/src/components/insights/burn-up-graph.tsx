/**
 * LS-0102 BurnUpGraph — honest Scope / Started / Completed lines for cycle detail.
 */

import type { Cycle, Issue } from '@/types/flow'
import { formatCycleDay } from '@/components/cycles/cycle-model'
import {
  burnUpPath,
  computeBurnUpSeries,
  isWeekendUtc,
  type BurnUpMeasure,
} from './burn-up-model'
import './burn-up-graph.css'

export function BurnUpGraph({
  cycle,
  issues,
  compact = false,
  measure = 'issue_count',
  activeTick,
}: {
  cycle: Cycle
  issues: Issue[]
  compact?: boolean
  measure?: BurnUpMeasure
  /** Optional override for legend hover tick (day index). */
  activeTick?: number
}) {
  const series = computeBurnUpSeries(cycle, issues, { measure })
  const tick = activeTick ?? series.activeTickIndex
  const point = series.days[tick] ?? series.days.at(-1)
  const width = 594
  const height = 138
  const padTop = 22
  const start = formatCycleDay(cycle.startsAt)
  const end = formatCycleDay(cycle.endsAt)

  const scopePath = burnUpPath(series.days.map(d => d.scope), series.maxValue, width, height, 0, padTop)
  const startedPath = burnUpPath(series.days.map(d => d.started), series.maxValue, width, height, 0, padTop)
  const completedPath = burnUpPath(series.days.map(d => d.completed), series.maxValue, width, height, 0, padTop)

  const tickX = series.days.length <= 1
    ? 0
    : (tick / (series.days.length - 1)) * width

  return (
    <div className={`burn-up-graph cycle-graph ${compact ? 'is-compact' : ''}`} data-burn-up="true">
      {!compact && (
        <div className="cycle-graph__legend burn-up-graph__legend" role="list">
          <LegendSwatch color="var(--data-vis-neutral)" label="Scope" value={point?.scope ?? series.scope} />
          <LegendSwatch color="var(--data-vis-3)" label="Started" value={point?.started ?? series.started} />
          <LegendSwatch color="var(--data-vis-1)" label="Completed" value={point?.completed ?? series.completed} />
        </div>
      )}
      <svg
        aria-label={`Cycle burn-up. Scope ${series.scope}, started ${series.started}, completed ${series.completed}.`}
        role="img"
        viewBox="0 0 640 210"
        preserveAspectRatio="none"
      >
        <defs>
          <pattern id={`weekend-burn-${cycle.id}`} width="8" height="8" patternUnits="userSpaceOnUse" patternTransform="rotate(35)">
            <line x1="0" x2="0" y1="0" y2="8" stroke="var(--theme-soft-overlay)" strokeWidth="3" />
          </pattern>
        </defs>
        {series.days.map((day, index) => {
          if (!isWeekendUtc(day.date) || series.days.length < 2) return null
          const x = (index / (series.days.length - 1)) * width
          const next = Math.min(width, ((index + 1) / (series.days.length - 1)) * width)
          return <rect key={day.date} x={x} y={padTop} width={Math.max(2, next - x)} height={height} fill={`url(#weekend-burn-${cycle.id})`} />
        })}
        <path d={scopePath} fill="none" stroke="var(--data-vis-neutral)" strokeWidth="1.4" />
        <path d={startedPath} fill="none" stroke="var(--data-vis-3)" strokeWidth="2" />
        <path d={completedPath} fill="none" stroke="var(--data-vis-1)" strokeWidth="2" />
        <line x1="0" x2={width} y1={padTop + height} y2={padTop + height} stroke="var(--theme-border-strong)" />
        <line x1={tickX} x2={tickX} y1={padTop} y2={padTop + height} stroke="var(--theme-border-strong)" strokeDasharray="3 4" />
        <text x="0" y="187">{start.month} {start.day}</text>
        <text x={width} y="187" textAnchor="end">{end.month} {end.day}</text>
      </svg>
    </div>
  )
}

function LegendSwatch({ color, label, value }: { color: string; label: string; value: number }) {
  return (
    <div role="listitem">
      <i style={{ background: color }} />
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  )
}

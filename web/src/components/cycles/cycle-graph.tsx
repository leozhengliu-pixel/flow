/**
 * Linear-style cycle graph: Scope / Started / Completed per day from the
 * server-built history, a Target line and, for the active cycle, a projection
 * of completion from recent velocity. Hover (or arrow keys) inspects a day.
 */
import { useId, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'

import type { CycleGraphData } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import {
  cycleGraphIndexAt,
  cycleGraphPath,
  cycleGraphSeries,
  cycleGraphX,
  isWeekend,
  type CycleGraphMeasure,
} from './cycle-graph-model'
import './cycle-graph.css'

const WIDTH = 600
const HEIGHT = 150
const TOP = 6

export function CycleGraph({
  graph,
  measure = 'issues',
  onMeasureChange,
  compact = false,
}: {
  graph: CycleGraphData
  measure?: CycleGraphMeasure
  onMeasureChange?: (measure: CycleGraphMeasure) => void
  compact?: boolean
}) {
  const { t, formatDate } = useI18n()
  const patternId = `cycle-graph-weekend-${useId().replace(/:/g, '')}`
  const plotRef = useRef<HTMLDivElement>(null)
  const [hover, setHover] = useState<number | null>(null)
  const effectiveMeasure: CycleGraphMeasure = graph.estimates ? measure : 'issues'
  const series = cycleGraphSeries(graph, effectiveMeasure)
  const count = series.points.length
  const fallback = series.todayIndex >= 0 ? series.todayIndex : Math.max(0, series.lastActualIndex)
  const index = hover ?? fallback
  const point = series.points[index]
  const plotHeight = HEIGHT - TOP
  const path = (values: (number | null)[]) => cycleGraphPath(values, series.maxValue, WIDTH, plotHeight, TOP)
  const x = (value: number) => cycleGraphX(value, count, WIDTH)
  const y = (value: number) => TOP + plotHeight - Math.min(value, series.maxValue) / series.maxValue * plotHeight
  const dayLabel = (date: string) => formatDate(new Date(`${date}T12:00:00.000Z`), { month: 'short', day: 'numeric', timeZone: 'UTC' })
  const move = (event: PointerEvent<HTMLDivElement>) => {
    const rect = plotRef.current?.getBoundingClientRect()
    if (!rect || rect.width <= 0) return
    setHover(cycleGraphIndexAt(event.clientX - rect.left, count, rect.width))
  }
  const key = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
    event.preventDefault()
    setHover(Math.max(0, Math.min(count - 1, index + (event.key === 'ArrowLeft' ? -1 : 1))))
  }
  const format = (value: number | null) => value === null ? '–' : String(Math.round(value * 10) / 10)
  const percentOf = (value: number | null) => value === null || !point?.scope ? '' : `${Math.round(value / point.scope * 100)}%`
  if (!count) return null
  return (
    <div className={`flow-cycle-graph ${compact ? 'is-compact' : ''}`} data-cycle-graph="true">
      <div className="flow-cycle-graph__head">
        <span className="flow-cycle-graph__date">{point ? dayLabel(point.date) : ''}{hover === null && series.todayIndex >= 0 ? ` · ${t('Today')}` : ''}</span>
        {graph.estimates && onMeasureChange && (
          <div className="flow-cycle-graph__measure" role="radiogroup" aria-label={t('Measure')}>
            {(['issues', 'points'] as const).map(value => (
              <button key={value} type="button" role="radio" aria-checked={effectiveMeasure === value} onClick={() => onMeasureChange(value)}>{t(value === 'issues' ? 'Issues' : 'Points')}</button>
            ))}
          </div>
        )}
      </div>
      <div className="flow-cycle-graph__legend" role="list">
        <Legend tone="scope" label={t('Scope')} value={format(point?.scope ?? null)}/>
        <Legend tone="started" label={t('Started')} value={format(point?.started ?? null)} detail={percentOf(point?.started ?? null)}/>
        <Legend tone="completed" label={t('Completed')} value={format(point?.completed ?? null)} detail={percentOf(point?.completed ?? null)}/>
        {!compact && <Legend tone="target" label={t('Target')} value={format(point ? Math.round(point.target * 10) / 10 : null)}/>}
        {!compact && point?.projected !== null && point?.projected !== undefined && index > series.todayIndex && <Legend tone="projected" label={t('Projected')} value={format(Math.round(point.projected * 10) / 10)}/>}
      </div>
      <div
        ref={plotRef}
        className="flow-cycle-graph__plot"
        tabIndex={0}
        role="img"
        aria-label={`${t('Cycle progress')}: ${t('Scope')} ${series.scope}, ${t('Started')} ${series.started}, ${t('Completed')} ${series.completed}`}
        onPointerMove={move}
        onPointerLeave={() => setHover(null)}
        onBlur={() => setHover(null)}
        onKeyDown={key}
      >
        <svg viewBox={`0 0 ${WIDTH} ${HEIGHT}`} preserveAspectRatio="none" aria-hidden="true">
          <defs>
            <pattern id={patternId} width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
              <line x1="0" x2="0" y1="0" y2="6" className="flow-cycle-graph__weekend-line"/>
            </pattern>
          </defs>
          {count > 1 && series.points.map(day => isWeekend(day.date) && (
            <rect key={day.date} x={x(Math.max(0, day.index - .5))} y={TOP} width={x(Math.min(count - 1, day.index + .5)) - x(Math.max(0, day.index - .5))} height={plotHeight} fill={`url(#${patternId})`}/>
          ))}
          <line className="flow-cycle-graph__axis" x1="0" x2={WIDTH} y1={TOP + plotHeight} y2={TOP + plotHeight}/>
          <path className="flow-cycle-graph__line is-target" d={path(series.points.map(item => item.target))}/>
          <path className="flow-cycle-graph__line is-scope" d={path(series.points.map(item => item.scope))}/>
          <path className="flow-cycle-graph__line is-started" d={path(series.points.map(item => item.started))}/>
          <path className="flow-cycle-graph__line is-completed" d={path(series.points.map(item => item.completed))}/>
          <path className="flow-cycle-graph__line is-projected" d={path(series.points.map(item => item.projected))}/>
          {series.todayIndex >= 0 && <line className="flow-cycle-graph__today" x1={x(series.todayIndex)} x2={x(series.todayIndex)} y1={TOP} y2={TOP + plotHeight}/>}
          {point && <line className="flow-cycle-graph__cursor" x1={x(index)} x2={x(index)} y1={TOP} y2={TOP + plotHeight}/>}
        </svg>
        {point && point.scope !== null && (['scope', 'started', 'completed'] as const).map(tone => (
          <i key={tone} className={`flow-cycle-graph__dot is-${tone}`} style={{ left: `${x(index) / WIDTH * 100}%`, top: `${y(point[tone] ?? 0) / HEIGHT * 100}%` }}/>
        ))}
      </div>
      <div className="flow-cycle-graph__axis-labels">
        <time>{dayLabel(graph.startDate)}</time>
        {series.todayIndex > 0 && series.todayIndex < count - 1 && <time style={{ left: `${x(series.todayIndex) / WIDTH * 100}%` }} className="is-today">{t('Today')}</time>}
        <time>{dayLabel(graph.endDate)}</time>
      </div>
    </div>
  )
}

function Legend({ tone, label, value, detail }: { tone: string; label: string; value: string; detail?: string }) {
  return (
    <div role="listitem" className={`flow-cycle-graph__legend-item is-${tone}`}>
      <i/>
      <span>{label}</span>
      <strong>{value}</strong>
      {detail ? <em>{detail}</em> : null}
    </div>
  )
}

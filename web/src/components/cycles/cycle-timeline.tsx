import { useEffect, useMemo, useRef } from 'react'
import type { Cycle, Issue } from '@/types/flow'
import type { CycleGraphData } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import { EstimateIcon } from '@/components/issue/estimate-icon'
import { formatEstimatePoints } from '@/lib/estimates'
import { cycleStats, formatCycleDay } from './cycle-model'
import { cycleGraphSeries, isWeekend, type CycleGraphMeasure } from './cycle-graph-model'
import { useCycleGraph } from './use-cycle-graph'

const PLAY_PATH = 'M6.95588 5.28329L10.6901 7.43926C11.0235 7.63171 11.0235 8.11283 10.6901 8.30528L6.95588 10.4612C6.62255 10.6537 6.20588 10.4131 6.20588 10.0282L6.20588 5.71631C6.20588 5.33141 6.62255 5.09084 6.95588 5.28329Z'

export function CycleCooldownIcon() {
  return <svg className="cycle-cooldown-icon" aria-hidden="true" viewBox="0 0 16 16" fill="currentColor"><path fillRule="evenodd" d="M 15 8 A 7 7 0 0 1 8 15 A 7 7 0 0 1 1 8 A 7 7 0 0 1 8 1 A 7 7 0 0 1 15 8 Z M 13.5 8 A 5.5 5.5 0 0 1 8 13.5 A 5.5 5.5 0 0 1 2.5 8 A 5.5 5.5 0 0 1 8 2.5 A 5.5 5.5 0 0 1 13.5 8 Z"/><path d="M5.545 5.545c0-.3.245-.545.546-.545h.818c.301 0 .546.244.546.545v4.91a.545.545 0 0 1-.546.545h-.818a.545.545 0 0 1-.546-.546V5.545ZM8.545 5.545c0-.3.245-.545.546-.545h.818c.301 0 .546.244.546.545v4.91a.545.545 0 0 1-.546.545h-.818a.545.545 0 0 1-.546-.546V5.545Z"/></svg>
}

function TimelineCycleIcon({ upcoming, progress }: { upcoming: boolean; progress: number }) {
  const circumference = 39.269908169872416
  return <svg className="cycle-timeline-icon" aria-hidden="true" viewBox="0 0 16 16" fill="currentColor">
    <circle className="cycle-timeline-icon__track" cx="8" cy="8" r="6.25" fill="none" strokeWidth="1.5" strokeDasharray={upcoming ? undefined : circumference} strokeDashoffset={upcoming ? undefined : 5.357544031041707} strokeLinecap="round" transform={upcoming ? undefined : 'rotate(-54.26075591453578 6.25 6.25)'}/>
    {upcoming ? <circle className="cycle-timeline-icon__track" cx="8" cy="8" r="6.25" fill="none" strokeWidth="1.5" strokeDasharray="1.636246173744684" strokeDashoffset="0.818123086872342"/> : <circle className="cycle-timeline-icon__value" cx="8" cy="8" r="6.25" fill="none" strokeWidth="1.5" strokeDasharray={circumference} strokeDashoffset={circumference + .642455968958294 - circumference * progress / 100} strokeLinecap="round" transform="rotate(-76 6.25 6.25)"/>}
    <path d={PLAY_PATH}/>
  </svg>
}

export function CycleTimelineCard({ cycle, issues, stats, onOpen }: { cycle: Cycle; issues: Issue[]; stats: ReturnType<typeof cycleStats>; onOpen: () => void }) {
  const { t, formatDate } = useI18n()
  const upcoming = cycle.status === 'upcoming'
  const refreshKey = useMemo(() => issues.filter(issue => issue.cycleId === cycle.id).map(issue => `${issue.id}:${issue.state.id}:${issue.estimate ?? ''}:${issue.updatedAt}`).join('|'), [issues, cycle.id])
  const { graph } = useCycleGraph(upcoming ? undefined : cycle.id, refreshKey)
  const measure = stats.estimates ? 'points' : 'issues'
  const series = graph ? cycleGraphSeries(graph, measure) : undefined
  const scope = series?.scope ?? stats.scope
  const completedPercent = series?.completedPercent ?? stats.completedPercent
  const date = formatCycleDay(cycle.startsAt)
  const end = new Date(cycle.endsAt)
  end.setUTCDate(end.getUTCDate() + 1)
  const start = new Date(cycle.startsAt)
  const middle = new Date(start.getTime() + (end.getTime() - start.getTime()) * .6)
  const label = (value: Date) => formatDate(value, { month: 'short', day: 'numeric', timeZone: 'UTC' })
  const metrics = [
    { key: 'scope', label: t('Scope'), count: scope },
    { key: 'started', label: t('Started'), count: series?.started ?? stats.started, percent: series?.startedPercent ?? stats.startedPercent },
    { key: 'completed', label: t('Completed'), count: series?.completed ?? stats.completed, percent: completedPercent },
  ]
  return <button type="button" className={`cycle-timeline-card${upcoming ? ' is-upcoming' : ''}`} data-testid={`cycle-timeline-${cycle.number}`} onClick={onOpen}>
    <span className="cycle-timeline-date">{date.month}<br/>{date.day}</span>
    <span className="cycle-timeline-heading"><TimelineCycleIcon upcoming={upcoming} progress={completedPercent}/><span className="cycle-timeline-title" data-i18n-ignore>{cycle.name}</span></span>
    {!upcoming && <>
      <TimelineChart graph={graph} measure={measure} label={`${t('Cycle progress')}: ${completedPercent}%`}/>
      <span className="cycle-timeline-axis"><span>{label(start)}</span><span>{label(middle)}</span><span>{label(end)}</span></span>
      <span className="cycle-timeline-metrics">{metrics.map(metric => <span className={`cycle-timeline-metric is-${metric.key}`} key={metric.key}>
        <i/><span className="cycle-timeline-metric-label">{metric.label}</span><span className="cycle-timeline-metric-value">{stats.estimates && <EstimateIcon size={14}/>}<strong>{formatEstimatePoints(metric.count)}</strong>{metric.percent !== undefined && <span className="cycle-timeline-percent">{` • ${metric.percent}%`}</span>}</span>
      </span>)}</span>
    </>}
  </button>
}

function TimelineChart({ graph, measure, label }: { graph?: CycleGraphData; measure: CycleGraphMeasure; label: string }) {
  const ref = useRef<HTMLCanvasElement>(null)
  useEffect(() => {
    const canvas = ref.current
    if (!canvas) return
    const draw = () => {
      const width = canvas.clientWidth
      if (!width) return
      const ratio = window.devicePixelRatio || 1
      canvas.width = width * ratio
      canvas.height = 210 * ratio
      const ctx = canvas.getContext('2d')
      if (!ctx || !graph) return
      ctx.scale(ratio, ratio)
      const style = getComputedStyle(canvas)
      const color = (token: string) => style.getPropertyValue(token).trim()
      const series = cycleGraphSeries(graph, measure)
      const plotWidth = width - 4
      const x = (index: number) => index / Math.max(1, series.points.length - 1) * plotWidth
      const y = (value: number) => 178 - value / series.maxValue * 172
      ctx.save()
      ctx.beginPath()
      ctx.rect(0, 6, plotWidth, 172)
      ctx.clip()
      ctx.strokeStyle = color('--issue-dim')
      ctx.lineWidth = 1
      for (const day of series.points.filter(point => isWeekend(point.date))) {
        const left = x(day.index - .5), right = x(day.index + .5)
        ctx.save()
        ctx.beginPath()
        ctx.rect(left, 6, right - left, 172)
        ctx.clip()
        for (let at = left - 172; at < right; at += 6) {
          ctx.beginPath(); ctx.moveTo(at, 178); ctx.lineTo(at + 100, 6); ctx.stroke()
        }
        ctx.restore()
      }
      const drawLine = (values: (number | null)[], tone: string, dashed = false) => {
        ctx.strokeStyle = color(tone)
        ctx.lineWidth = 1.5
        ctx.setLineDash(dashed ? [3, 3] : [])
        ctx.beginPath()
        let pen = false
        values.forEach((value, index) => {
          if (value === null) { pen = false; return }
          if (pen) ctx.lineTo(x(index), y(value)); else ctx.moveTo(x(index), y(value))
          pen = true
        })
        ctx.stroke()
      }
      const workdays = series.points.filter(point => !isWeekend(point.date)).length
      let elapsedWorkdays = 0
      const target = series.points.map(point => {
        const value = series.scope * elapsedWorkdays / Math.max(1, workdays - 1)
        if (!isWeekend(point.date)) elapsedWorkdays++
        return Math.min(series.scope, value)
      })
      drawLine(target, '--accent-primary', true)
      drawLine(series.points.map(point => point.scope ?? series.scope), '--issue-dim')
      drawLine(series.points.map(point => point.started), '--priority-medium')
      drawLine(series.points.map(point => point.completed), '--accent-primary')
      ctx.restore()
      ctx.setLineDash([])
      ctx.fillStyle = color('--accent-primary')
      if (series.lastActualIndex >= 0) {
        const point = series.points[series.lastActualIndex]
        ctx.beginPath(); ctx.arc(x(point.index), y(point.completed ?? 0), 3.5, 0, Math.PI * 2); ctx.fill()
      }
      ctx.strokeStyle = color('--issue-muted')
      ctx.lineWidth = 1
      for (const at of [0, plotWidth * .6, plotWidth]) {
        ctx.beginPath(); ctx.moveTo(at, 179); ctx.lineTo(at, 184); ctx.stroke()
      }
    }
    draw()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(draw)
    observer.observe(canvas)
    return () => observer.disconnect()
  }, [graph, measure])
  return <canvas ref={ref} className="cycle-timeline-chart" role="img" aria-label={label}/>
}

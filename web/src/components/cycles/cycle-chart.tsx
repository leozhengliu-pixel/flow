import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type RefObject } from 'react'
import type { CycleGraphData } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import { THEME_CHANGE_EVENT } from '@/lib/theme'
import { cycleAxisTicks, cycleChartDays, cycleDayShare, cycleWeekendBands, isCycleWeekend } from './cycle-chart-model'
import { cycleGraphSeries, type CycleGraphMeasure } from './cycle-graph-model'
import './cycle-chart.css'

/** Plot margins inside the canvas: 6px above the scope line, 32px below the baseline for the axis. */
const TOP = 6
const BOTTOM = 32
/** The last tick sits 4px inside the right edge so its 1px mark and label stay in the canvas. */
const PLOT_INSET = 4
/** Weekend hatch: stripes rise two pixels for every one they move right; 5.65px apart, 2.35px wide on a row. */
const HATCH_RISE = 2
const HATCH_PERIOD = 5.65
const HATCH_RUN = 2.35
/** The scope fill fades out over this share of the plot height. */
const FILL_SHARE = .45

type Props = { graph?: CycleGraphData; measure: CycleGraphMeasure; startsAt: string; endsAt: string; height: number; label: string; className?: string; axisClassName?: string; hover?: number | null; onHover?: (day: number | null) => void; onOpen?: () => void }

/** The cycle chart: weekend bands, the Target, Scope, Started and Completed lines, a "now" marker and the week axis. */
export function CycleChart({ graph, measure, startsAt, endsAt, height, label, className, axisClassName, hover = null, onHover, onOpen }: Props) {
  const { formatDate } = useI18n()
  const ref = useRef<HTMLCanvasElement>(null)
  const days = useMemo(() => cycleChartDays(startsAt, endsAt), [startsAt, endsAt])
  const bands = useMemo(() => cycleWeekendBands(days), [days])
  useEffect(() => {
    const canvas = ref.current
    if (!canvas) return
    const draw = () => {
      const width = canvas.clientWidth
      if (!width) return
      const ratio = window.devicePixelRatio || 1
      canvas.width = width * ratio
      canvas.height = height * ratio
      const ctx = canvas.getContext('2d')
      if (!ctx || !graph || !days.length) return
      ctx.scale(ratio, ratio)
      const style = getComputedStyle(canvas)
      const color = (token: string) => style.getPropertyValue(token).trim()
      const series = cycleGraphSeries(graph, measure)
      const count = days.length
      const plotWidth = width - PLOT_INSET
      const bottom = height - BOTTOM
      const plotHeight = bottom - TOP
      const x = (boundary: number) => boundary / count * width
      const y = (value: number) => bottom - Math.min(value, series.maxValue) / series.maxValue * plotHeight
      const todayBoundary = series.todayIndex >= 0 ? days.indexOf(graph.days[series.todayIndex].date) : -1
      const nowX = todayBoundary >= 0 ? x(todayBoundary + cycleDayShare(new Date(), graph.timezone)) : -1
      ctx.save()
      ctx.beginPath()
      ctx.rect(0, TOP, plotWidth, plotHeight)
      ctx.clip()
      // One hatched band per weekend. The stripes are laid out a device pixel row at a time so their
      // edges stay crisp, and one stripe pattern runs through the whole band.
      ctx.fillStyle = color('--cycle-track')
      const snap = (value: number) => Math.round(value * ratio) / ratio
      for (const [from, to] of bands) {
        const left = x(from), right = x(to)
        ctx.save()
        ctx.beginPath()
        ctx.rect(left, TOP, right - left, plotHeight)
        ctx.clip()
        for (let row = 0; row < Math.round(plotHeight * ratio); row++) {
          const top = TOP + row / ratio
          const shift = (bottom - top) / HATCH_RISE
          for (let at = left + shift % HATCH_PERIOD - HATCH_PERIOD; at < right; at += HATCH_PERIOD) {
            ctx.fillRect(snap(at), top, snap(at + HATCH_RUN) - snap(at), 1 / ratio)
          }
        }
        ctx.restore()
      }
      if (nowX >= 0) {
        ctx.fillStyle = color('--theme-surface-hover')
        ctx.fillRect(Math.round(nowX) - .5, TOP, 1, plotHeight)
      }
      const drawLine = (points: { x: number; y: number }[], tone: string, dashed = false) => {
        if (points.length < 2) return
        ctx.strokeStyle = color(tone)
        ctx.lineWidth = 1.5
        ctx.setLineDash(dashed ? [3, 3] : [])
        ctx.beginPath()
        points.forEach((point, index) => index ? ctx.lineTo(point.x, point.y) : ctx.moveTo(point.x, point.y))
        ctx.stroke()
      }
      // The Target rises through the working days and rests on weekends.
      const worked: number[] = [0]
      days.forEach(day => worked.push(worked[worked.length - 1] + (isCycleWeekend(day) ? 0 : 1)))
      if (worked[count] > 0) drawLine(worked.map((total, boundary) => ({ x: x(boundary), y: y(series.scope * total / worked[count]) })), '--accent-primary', true)
      // Values are end-of-day figures: day i is drawn at the end of day i, the current day at "now".
      const through = (pick: (point: (typeof series.points)[number]) => number | null, start: number) => {
        const points = [{ x: 0, y: y(start) }]
        series.points.forEach(point => {
          const value = pick(point)
          const boundary = days.indexOf(point.date)
          if (value === null || boundary < 0) return
          points.push({ x: boundary === todayBoundary ? nowX : x(boundary + 1), y: y(value) })
        })
        return points
      }
      // The scope line runs the whole cycle: it rests at the current scope after today.
      const scopeLine = through(point => point.scope, series.points[0]?.scope ?? 0)
      drawLine([...scopeLine, { x: width, y: scopeLine[scopeLine.length - 1].y }], '--issue-dim')
      drawLine(through(point => point.started, 0), '--cycle-started')
      const completedLine = through(point => point.completed, 0)
      drawLine(completedLine, '--cycle-completed')
      if (hover !== null && hover >= 0 && hover < count) {
        ctx.strokeStyle = color('--issue-dim')
        ctx.lineWidth = 1
        ctx.setLineDash([3, 3])
        ctx.beginPath(); ctx.moveTo(Math.round(x(hover)) + .5, TOP); ctx.lineTo(Math.round(x(hover)) + .5, bottom); ctx.stroke()
      }
      ctx.restore()
      ctx.setLineDash([])
      ctx.fillStyle = color('--cycle-completed')
      if (series.lastActualIndex >= 0 && completedLine.length > 1) {
        const last = completedLine[completedLine.length - 1]
        ctx.beginPath(); ctx.arc(last.x, last.y, 3.5, 0, Math.PI * 2); ctx.fill()
      }
    }
    draw()
    window.addEventListener(THEME_CHANGE_EVENT, draw)
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(draw)
    observer?.observe(canvas)
    return () => {
      window.removeEventListener(THEME_CHANGE_EVENT, draw)
      observer?.disconnect()
    }
  }, [graph, measure, days, bands, height, hover])
  // The tick spacing needs the rendered width; the chart re-renders on resize through `width`.
  const width = useChartWidth(ref)
  const ticks = cycleAxisTicks(days, Math.max(0, width - PLOT_INSET))
  return <div className={`cycle-chart${className ? ` ${className}` : ''}`} style={{ height }}>
    <div className="cycle-chart__fill" style={{ top: TOP, height: (height - BOTTOM - TOP) * FILL_SHARE }}/>
    <canvas ref={ref} className="cycle-chart__canvas" role="img" aria-label={label} onClick={onOpen}
      onPointerMove={onHover && (event => { if (event.pointerType === 'touch' || !days.length) return; const box = event.currentTarget.getBoundingClientRect(); onHover(Math.max(0, Math.min(days.length - 1, Math.floor((event.clientX - box.left) / box.width * days.length)))) })}
      onPointerLeave={onHover && (() => onHover(null))}/>
    {hover !== null && hover >= 0 && hover < days.length && <div className="cycle-chart__tooltip" style={{ '--tick': hover / days.length, top: height - BOTTOM } as CSSProperties}>{formatDate(new Date(`${days[hover]}T12:00:00.000Z`), { month: 'short', day: 'numeric', timeZone: 'UTC' })}</div>}
    <div className="cycle-chart__marks" style={{ top: height - BOTTOM + 4 }} aria-hidden="true">
      {ticks.map(tick => <i key={tick.boundary} style={{ '--tick': tick.fraction } as CSSProperties}/>)}
    </div>
    <div className={`cycle-chart__axis${axisClassName ? ` ${axisClassName}` : ''}`} style={{ top: height - BOTTOM + 4 }} aria-hidden="true">
      {ticks.map(tick => <span key={tick.boundary} className={`cycle-chart__label is-${tick.kind}`} style={{ '--tick': tick.fraction } as CSSProperties}>{formatDate(new Date(`${tick.date}T12:00:00.000Z`), { month: 'short', day: 'numeric', timeZone: 'UTC' })}</span>)}
    </div>
  </div>
}

function useChartWidth(ref: RefObject<HTMLCanvasElement | null>) {
  const [width, setWidth] = useState(0)
  useLayoutEffect(() => {
    const canvas = ref.current
    if (!canvas) return
    const measure = () => setWidth(canvas.clientWidth)
    measure()
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(measure)
    observer?.observe(canvas)
    return () => observer?.disconnect()
  }, [ref])
  return width
}

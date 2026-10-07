import { ArrowDown, ArrowUp } from 'lucide-react'
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import { StatusIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'
import { formatMetric, insightColor, type InsightData, type InsightRow } from './insight-data'
import type { SavedViewInsightDimension, SavedViewInsightsConfig } from './insight-config'
import { aggregateInsightValues, aggregationLabels, durationAxis, insightAxis, insightHighlight, sameInsightTarget, type InsightTarget, type InsightAggregation } from './insight-interaction'
import styles from './insight-explorer.module.css'

/** Status / priority glyphs where Linear shows them; a colour mark for every other value. */
export function InsightValueIcon({ dimension, id, color, data, size = 14 }: { dimension: SavedViewInsightDimension | 'none'; id: string; color?: string; data: BootstrapData; size?: number }) {
  if (dimension === 'status') { const state = data.states.find(item => item.id === id); if (state) return <StatusIcon state={state} size={size}/> }
  if (dimension === 'priority') return <PriorityIcon priority={Number(id)} size={size}/>
  return <i className={styles.mark} style={{ background: color ?? 'var(--data-vis-neutral)' }}/>
}

let measureContext: CanvasRenderingContext2D | null | undefined
function textWidth(text: string) {
  if (measureContext === undefined) measureContext = typeof navigator === 'undefined' || /jsdom/i.test(navigator.userAgent) ? null : document.createElement('canvas').getContext('2d')
  if (!measureContext) return text.length * 6.5
  measureContext.font = '500 12px "Inter Variable", "SF Pro Display", -apple-system, system-ui, sans-serif'
  return measureContext.measureText(text).width
}

function useWidth<T extends HTMLElement>(fallback: number) {
  const ref = useRef<T>(null)
  const [width, setWidth] = useState(fallback)
  useLayoutEffect(() => {
    const node = ref.current
    if (!node) return
    const update = () => { const next = node.getBoundingClientRect().width; if (next > 0) setWidth(next) }
    update()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(update)
    observer.observe(node)
    return () => observer.disconnect()
  }, [])
  return [ref, width] as const
}

export function InsightExplorer({ config, data, insight, expanded, target, onSelect, onClear, sliceLabel, onOpenIssue }: {
  onOpenIssue?: (row: InsightData['samples'][number]['item']) => void
  sliceLabel: string
  config: SavedViewInsightsConfig; data: BootstrapData; insight: InsightData; expanded: boolean
  target?: InsightTarget; onSelect: (target: InsightTarget) => void; onClear: () => void
}) {
  const { t } = useI18n()
  const [hover, setHover] = useState<InsightTarget>()
  const [sort, setSort] = useState<{ column: string; descending: boolean }>()
  const operator = 'gt' as const
  const [scrollLeft, setScrollLeft] = useState(0)
  const table = useRef<HTMLDivElement>(null)
  const virtual = useRef<VirtuosoHandle>(null)
  const latency = config.measure !== 'issueCount'
  const highlight = hover ?? target
  const rows = useMemo(() => {
    if (!sort) return insight.rows
    return [...insight.rows].sort((a, b) => {
      const value = (row: InsightRow) => sort.column === 'total' ? latency ? row.values.length : row.total : latency ? row.aggregations[sort.column as InsightAggregation] ?? 0 : row.segments[sort.column] ?? 0
      const difference = sort.column === 'slice' ? a.label.localeCompare(b.label) : value(a) - value(b)
      return difference * (sort.descending ? -1 : 1) || a.id.localeCompare(b.id)
    })
  }, [insight, sort, latency])
  useEffect(() => { setHover(undefined); setSort(undefined) }, [config.slice, config.segment, config.measure])
  const columns = useMemo(() => latency ? (config.aggregations ?? (config.aggregation ? [config.aggregation] : ['median', 'p75', 'p95'] as const)).map(id => ({ id, label: id === 'median' ? 'P50' : aggregationLabels[id], color: undefined as string | undefined })) : config.segment === 'none' ? [] : insight.segments, [latency, config.aggregations, config.aggregation, config.segment, insight.segments])
  // Linear sizes every value column to its content (20px padding each side) and gives the rest to the first column.
  const uniform = columns.length > 30
  const widths = useMemo(() => {
    const numberWidth = (values: number[]) => values.reduce((max, value) => Math.max(max, textWidth(latency ? formatMetric(value, config.measure) : String(value))), 0)
    const total = Math.ceil(41 + Math.max(textWidth(t('Issue count')), numberWidth(insight.rows.map(row => row.total))))
    if (uniform) return { total, columns: columns.map(() => 106) }
    return { total, columns: columns.map(column => Math.ceil(41 + Math.max(textWidth(t(column.label)) + (latency ? 0 : 22), numberWidth(insight.rows.map(row => latency ? row.aggregations[column.id as InsightAggregation] ?? 0 : row.segments[column.id] ?? 0))))) }
  }, [columns, insight.rows, latency, config.measure, t, uniform])
  const firstMin = latency ? 110 : 96
  const lead = firstMin + (latency ? 0 : widths.total)
  useEffect(() => {
    if (!target) return
    const index = rows.findIndex(row => row.id === target.slice)
    if (index >= 0) virtual.current?.scrollIntoView({ index, align: 'center', behavior: 'auto' })
    const scroller = table.current
    const columnIndex = target.aggregation ? 0 : columns.findIndex(column => column.id === target.segment)
    if (uniform && scroller && columnIndex >= 0 && (target.segment || target.aggregation)) {
      const left = lead + columnIndex * 106
      if (left < scroller.scrollLeft + 150 || left + 106 > scroller.scrollLeft + scroller.clientWidth) {
        scroller.scrollLeft = Math.max(0, left + 106 - scroller.clientWidth)
        setScrollLeft(scroller.scrollLeft)
      }
    }
    const frame = requestAnimationFrame(() => {
      const container = table.current
      const cell = container?.querySelector<HTMLElement>('[data-selected="true"]')
      if (!container || !cell) return
      const c = container.getBoundingClientRect(), r = cell.getBoundingClientRect()
      const header = container.querySelector('[role="row"]')?.getBoundingClientRect().height ?? 38
      const first = container.querySelector('[role="columnheader"]')?.getBoundingClientRect().width ?? 0
      const top = r.top < c.top + header ? r.top - c.top - header : r.bottom > c.bottom ? r.bottom - c.bottom : 0
      const left = target.segment || target.aggregation ? r.left < c.left + first ? r.left - c.left - first : r.right > c.right ? r.right - c.right : 0 : 0
      container.scrollBy?.({ top, left, behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' })
    })
    return () => cancelAnimationFrame(frame)
  }, [target, rows, columns, uniform, lead])
  const columnStart = uniform ? Math.max(0, Math.floor((scrollLeft - lead) / 106) - 1) : 0
  const visibleColumns = uniform ? columns.slice(columnStart, columnStart + Math.ceil((table.current?.clientWidth ?? 1000) / 106) + 4) : columns
  const cellOffset = latency ? 2 : 3
  const tableStyle = {
    '--insight-columns': `minmax(${firstMin}px,1fr)${latency ? '' : ` ${widths.total}px`}${columns.length ? ` ${widths.columns.map(width => `${width}px`).join(' ')}` : ''}`,
    '--insight-min-width': `${lead + widths.columns.reduce((sum, width) => sum + width, 0)}px`,
  } as CSSProperties
  const chooseSort = (column: string) => setSort(current => ({ column, descending: current?.column === column ? !current.descending : column !== 'slice' }))
  const mark = (row: InsightRow) => <InsightValueIcon dimension={config.slice} id={row.id} color={row.color} data={data}/>
  // Selecting a cell dims everything outside its row and column (Linear: 0.6 opacity).
  const dimmed = (cell: InsightTarget) => {
    if (!target || target.slice === undefined && target.segment === undefined && target.aggregation === undefined) return undefined
    const inRow = target.slice !== undefined && target.slice === cell.slice
    const inColumn = target.segment !== undefined && target.segment === cell.segment || target.aggregation !== undefined && target.aggregation === cell.aggregation
    return inRow || inColumn ? undefined : true
  }
  const headerCell = (id: string, label: string, icon?: ReactNode) => <div role="columnheader" aria-sort={sort?.column === id ? sort.descending ? 'descending' : 'ascending' : 'none'} key={id} onMouseEnter={() => id !== 'slice' && id !== 'total' ? setHover(latency ? { aggregation: id, operator, threshold: aggregateInsightValues(insight.samples.map(sample => sample.value), id as InsightAggregation) } : { segment: id }) : setHover(undefined)} data-highlight={hover ? insightHighlight(hover, latency ? { aggregation: id } : { segment: id }) : undefined}>
    <button type="button" onClick={() => chooseSort(id)}>{icon}<span>{t(label)}</span>{sort?.column === id && (sort.descending ? <ArrowDown size={12}/> : <ArrowUp size={12}/>)}</button>
  </div>
  const renderCell = (value: string, cellTarget: InsightTarget, prefix?: ReactNode, label?: boolean) => <div role="cell" data-highlight={hover ? insightHighlight(hover, cellTarget) : undefined} data-dim={dimmed(cellTarget)} data-label={label || undefined} data-selected={target && sameInsightTarget(target, cellTarget) || undefined}>
    <button type="button" aria-pressed={Boolean(target && sameInsightTarget(target, cellTarget))} onMouseEnter={() => setHover(cellTarget)} onFocus={() => setHover(cellTarget)} onBlur={() => setHover(undefined)} onClick={() => onSelect(cellTarget)}>{prefix}<span>{value}</span></button>
  </div>
  const renderRow = (row: InsightRow) => <div role="row" className={styles.row} data-slice={row.id}>
    {renderCell(t(row.label), { slice: row.id }, mark(row), true)}
    {!latency && renderCell(String(row.total), { slice: row.id })}
    {visibleColumns.map((column, index) => {
      const value = latency ? row.aggregations[column.id as InsightAggregation] : row.segments[column.id] ?? 0
      const cellTarget: InsightTarget = latency ? { slice: row.id, aggregation: column.id, operator, threshold: value } : { slice: row.id, segment: column.id }
      return <div className={styles.columnCell} style={{ gridColumn: columnStart + index + cellOffset }} key={column.id}>{value === undefined ? <div role="cell">-</div> : renderCell(formatMetric(value, config.measure), cellTarget)}</div>
    })}
  </div>
  const totalRow = latency && <div role="row" className={`${styles.row} ${styles.total}`}>
    {renderCell(t(config.slice === 'status' ? 'Across all statuses' : 'Across all groups'), {})}
    {visibleColumns.map((column, index) => <div className={styles.columnCell} style={{ gridColumn: columnStart + index + cellOffset }} key={column.id}>{renderCell(formatMetric(aggregateInsightValues(insight.samples.map(sample => sample.value), column.id as InsightAggregation) ?? 0, config.measure), { aggregation: column.id, operator, threshold: aggregateInsightValues(insight.samples.map(sample => sample.value), column.id as InsightAggregation) ?? 0 })}</div>)}
  </div>
  return <div className={styles.explorer} data-expanded={expanded} onKeyDown={event => { if (event.key === 'Escape' && target) { event.stopPropagation(); onClear() } }}>
    <div className={styles.chart} aria-label={t('Insight chart')} onMouseLeave={() => setHover(undefined)}>
      {!rows.length ? <div className={styles.empty}>{t('No data for this insight')}</div> : latency ? <LatencyGraph rows={rows} insight={insight} config={config} highlight={highlight} onHover={setHover} onSelect={onSelect} onOpenIssue={onOpenIssue}/> : rows.length > 100 || insight.segments.length > 100 ? <DenseDistributionGraph rows={rows} config={config} segments={insight.segments} highlight={highlight} onHover={setHover} onSelect={onSelect}/> : <InsightBarChart rows={rows} insight={insight} config={config} data={data} expanded={expanded} hover={hover} target={target} onHover={setHover} onSelect={onSelect}/>}
    </div>
    <div role="table" aria-label={t('Insights table')} className={styles.table} ref={table} style={tableStyle} onScroll={event => setScrollLeft(event.currentTarget.scrollLeft)} onMouseLeave={() => setHover(undefined)}>
      <div role="row" className={`${styles.row} ${styles.header}`}>{headerCell('slice', sliceLabel)}{!latency && headerCell('total', 'Issue count')}{visibleColumns.map((column, index) => <div className={styles.columnCell} style={{ gridColumn: columnStart + index + cellOffset }} key={column.id}>{headerCell(column.id, column.label, latency || config.segment === 'none' ? undefined : <InsightValueIcon dimension={config.segment} id={column.id} color={column.color} data={data} size={16}/>)}</div>)}</div>
      {totalRow}
      {rows.length > 100 ? <Virtuoso ref={virtual} className={styles.virtual} style={{ height: 'min(380px, max(152px, calc(100vh - 530px)))', minWidth: 'var(--insight-min-width)' }} data={rows} fixedItemHeight={38} computeItemKey={(_, row) => row.id} itemContent={(_, row) => renderRow(row)}/> : rows.map(row => <div className={styles.contents} key={row.id}>{renderRow(row)}</div>)}
    </div>
  </div>
}

/** Linear's bar chart: 10px bars stacked by segment, dashed half-step gridlines, values on the right, text labels below. */
function InsightBarChart({ rows, insight, config, data, expanded, hover, target, onHover, onSelect }: {
  rows: InsightRow[]; insight: InsightData; config: SavedViewInsightsConfig; data: BootstrapData; expanded: boolean
  hover?: InsightTarget; target?: InsightTarget; onHover: (target?: InsightTarget) => void; onSelect: (target: InsightTarget) => void
}) {
  const { t } = useI18n()
  const [ref, width] = useWidth<HTMLDivElement>(expanded ? 900 : 427)
  const height = expanded ? 265 : 250
  const margin = expanded ? { top: 10, right: 32, bottom: 30, left: 20 } : { top: 10, right: 28, bottom: 30, left: 16 }
  const innerWidth = Math.max(0, width - margin.left - margin.right), innerHeight = height - margin.top - margin.bottom
  const axis = insightAxis(rows.reduce((max, row) => Math.max(max, row.total), 0))
  const y = (value: number) => margin.top + innerHeight * (1 - value / axis.max)
  const band = rows.length ? innerWidth / rows.length : innerWidth
  const barWidth = Math.max(1, Math.min(10, band - 2))
  const center = (index: number) => margin.left + (index + .5) * band
  const highlight = hover ?? target
  const segmented = config.segment !== 'none'
  const showAllLabels = band >= 36
  const tip = hover && !hover.issueId && !hover.aggregation ? tooltipFor(hover) : undefined
  function tooltipFor(cell: InsightTarget) {
    const index = rows.findIndex(row => row.id === cell.slice)
    const row = rows[index]
    if (!row) return undefined
    const segmentIndex = segmented && cell.segment !== undefined ? insight.segments.findIndex(segment => segment.id === cell.segment) : -1
    const segment = insight.segments[segmentIndex]
    const value = segment ? row.segments[segment.id] ?? 0 : row.total
    const share = segment ? row.total ? value / row.total : 0 : insight.samples.length ? row.total / insight.samples.length : 0
    let base = 0
    if (segment) for (const [position, item] of insight.segments.entries()) { if (position >= segmentIndex) break; base += row.segments[item.id] ?? 0 }
    return { index, label: segment ? segment.label : row.label, color: insightColor(config, segment?.color, row.color, segment ? segmentIndex : index), value, share, y: y(base + value / 2) }
  }
  return <div ref={ref} className={styles.barChart} style={{ height }}>
    <svg width={width} height={height} role="img" aria-label={t('Insight chart')}>
      {axis.lines.filter(value => value <= axis.max).map(value => <line key={value} className={value === 0 ? styles.baseline : styles.gridline} x1={margin.left} x2={margin.left + innerWidth} y1={y(value)} y2={y(value)}/>)}
      {axis.labels.map(value => <text key={value} className={styles.tick} x={margin.left + innerWidth + 5} y={y(value)} dominantBaseline="central">{value}</text>)}
      {rows.map((row, index) => {
        const active = highlight?.slice === row.id
        let base = 0
        return <g key={row.id}>
          <rect className={styles.track} x={center(index) - band / 2} y={margin.top} width={Math.max(0, band)} height={innerHeight + margin.bottom} onMouseEnter={() => onHover({ slice: row.id })} onClick={() => onSelect({ slice: row.id })}/>
          <rect className={styles.columnTrack} data-active={active || undefined} x={center(index) - barWidth / 2} y={margin.top} width={barWidth} height={innerHeight} pointerEvents="none"/>
          {insight.segments.map((segment, segmentIndex) => {
            const value = segmented ? row.segments[segment.id] ?? 0 : segmentIndex === 0 ? row.total : 0
            if (!value) return null
            const top = y(base + value), bottom = y(base)
            base += value
            const cellTarget: InsightTarget = segmented ? { slice: row.id, segment: segment.id } : { slice: row.id }
            const strong = insightHighlight(highlight, cellTarget) === 'strong'
            const opacity = !highlight || strong ? 1 : hover ? .6 : .2
            return <g key={segment.id} opacity={opacity} className={styles.bar}>
              <rect x={center(index) - barWidth / 2} y={top} width={barWidth} height={Math.max(0, bottom - top)} fill={insightColor(config, segment.color, row.color, segmented ? segmentIndex : index)}
                role="button" tabIndex={0} aria-label={`${t(row.label)}, ${segmented ? t(segment.label) : t('Issue count')}: ${value}`} aria-pressed={Boolean(target && sameInsightTarget(target, cellTarget))}
                onMouseEnter={() => onHover(cellTarget)} onFocus={() => onHover(cellTarget)} onBlur={() => onHover(undefined)}
                onClick={() => onSelect(cellTarget)} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onSelect(cellTarget) } }}/>
              <line className={styles.barCap} x1={center(index) - barWidth / 2 - 1} x2={center(index) + barWidth / 2 + 1} y1={top} y2={top} pointerEvents="none"/>
            </g>
          })}
        </g>
      })}
    </svg>
    <div className={styles.axisLabels} style={{ left: margin.left, right: margin.right }}>
      {rows.map((row, index) => {
        const active = highlight?.slice === row.id
        if (!active && !showAllLabels) return null
        return <button type="button" key={row.id} className={styles.axisLabel} data-active={active || undefined} style={{ left: `${(index + .5) / rows.length * 100}%`, maxWidth: active ? undefined : Math.max(24, band - 4) }} aria-label={t(row.label)} onMouseEnter={() => onHover({ slice: row.id })} onFocus={() => onHover({ slice: row.id })} onBlur={() => onHover(undefined)} onClick={() => onSelect({ slice: row.id })}>
          {active && <InsightValueIcon dimension={config.slice} id={row.id} color={row.color} data={data}/>}<span>{t(row.label)}</span>
        </button>
      })}
    </div>
    {tip && <div role="tooltip" className={styles.tooltip} data-side={tip.index < rows.length / 2 && center(tip.index) < 220 ? 'right' : 'left'} style={{ '--tip-x': `${center(tip.index) + (tip.index < rows.length / 2 && center(tip.index) < 220 ? barWidth / 2 + 12 : -barWidth / 2 - 12)}px`, '--tip-y': `${tip.y}px` } as CSSProperties}>
      <i style={{ background: tip.color }}/><span>{t(tip.label)}</span><b>{tip.value}</b><small>{Math.round(tip.share * 100)}%</small>
    </div>}
  </div>
}

function LatencyGraph({ rows, insight, config, highlight, onHover, onSelect, onOpenIssue }: {
  rows: InsightRow[]; insight: InsightData; config: SavedViewInsightsConfig; highlight?: InsightTarget
  onHover: (target?: InsightTarget) => void; onSelect: (target: InsightTarget) => void
  onOpenIssue?: (row: InsightData['samples'][number]['item']) => void
}) {
  const canvas = useRef<HTMLCanvasElement>(null)
  const points = useRef<{ x: number; y: number; sample: InsightData['samples'][number]; slice: string }[]>([])
  const lines = useRef<{ x: number; y: number; target: InsightTarget }[]>([])
  const frame = useRef<number | undefined>(undefined)
  const selectedAggregations = config.aggregations ?? (config.aggregation ? [config.aggregation] : ['median', 'p75', 'p95'] as const)
  const aggregationKey = selectedAggregations.join(',')
  const axis = useMemo(() => durationAxis(insight.samples.map(sample => sample.value), config.latencyScale !== 'linear'), [insight, config.latencyScale])
  const grouped = useMemo(() => {
    const result = new Map<string, InsightData['samples']>()
    for (const sample of insight.samples) for (const slice of sample.slices) { const values = result.get(slice) ?? []; values.push(sample); result.set(slice, values) }
    return result
  }, [insight])
  const pointHover = highlight?.issueId ? insight.samples.find(sample => sample.item.id === highlight.issueId) : undefined
  useEffect(() => {
    const node = canvas.current
    if (!node) return
    const draw = () => {
      const rect = node.getBoundingClientRect(), ratio = devicePixelRatio || 1
      node.width = rect.width * ratio; node.height = rect.height * ratio
      const ctx = node.getContext('2d')
      if (!ctx) return
      ctx.scale(ratio, ratio)
      const css = getComputedStyle(node), foreground = css.color, faint = css.getPropertyValue('--insight-grid')
      const color = (value: string) => value.startsWith('var(') ? css.getPropertyValue(value.slice(4,-1)).trim() || foreground : value
      const width = Math.max(1, rect.width - 63), height = Math.max(1, rect.height - 42), band = width / rows.length
      const yPosition = (value: number) => 10 + height * (1 - axis.fraction(value))
      points.current = []; lines.current = []
      ctx.font = '11px "Inter Variable", sans-serif'
      for (const tick of axis.ticks) {
        const y = yPosition(tick.value)
        ctx.strokeStyle = faint; ctx.lineWidth = 1; ctx.setLineDash?.(tick.value ? [3, 3] : []); ctx.beginPath(); ctx.moveTo(16, y); ctx.lineTo(16 + width, y); ctx.stroke(); ctx.setLineDash?.([])
        ctx.fillStyle = foreground; ctx.fillText(tick.label, 24 + width, y + 4)
      }
      rows.forEach((row, index) => {
        const x = 16 + (index + .5) * band
        const samples = grouped.get(row.id) ?? []
        const dates = samples.map(sample => Date.parse(sample.item.createdAt))
        const first = dates.reduce((min,date) => Math.min(min,date), Infinity)
        const last = dates.reduce((max,date) => Math.max(max,date), 0)
        const tint = color(insightColor(config, undefined, row.color, index))
        ctx.fillStyle = tint
        for (let i = 0; i < samples.length; i++) {
          const sample = samples[i], value = sample.value
          const jitter = last > first ? .7 * ((dates[i] - first) / (last - first) - .5) * band : 0
          const point = { x: x + jitter, y: yPosition(value), sample, slice: row.id }
          points.current.push(point)
          ctx.globalAlpha = highlight && (highlight.slice && highlight.slice !== row.id || highlight.threshold !== undefined && (highlight.operator === 'gt' ? value <= highlight.threshold : value > highlight.threshold)) ? .12 : .7
          ctx.beginPath(); ctx.arc(point.x, point.y, highlight?.issueId === sample.item.id ? 3.5 : 2, 0, Math.PI * 2); ctx.fill()
        }
        ctx.globalAlpha = 1
        for (const aggregation of aggregationKey.split(',') as InsightAggregation[]) {
          const threshold = row.aggregations[aggregation]
          if (threshold === undefined) continue
          const line = { x, y: yPosition(threshold), target: { slice: row.id, aggregation, threshold, operator: 'lte' as const } }
          lines.current.push(line)
          ctx.fillStyle = highlight?.aggregation === aggregation ? tint : foreground
          ctx.fillRect(x - Math.min(10, band / 3), line.y, Math.min(20, band * 2 / 3), 1)
        }
      })
    }
    const observer = new ResizeObserver(draw); observer.observe(node)
    const themes = new MutationObserver(draw); themes.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'class'] })
    draw()
    return () => { observer.disconnect(); themes.disconnect(); if (frame.current !== undefined) cancelAnimationFrame(frame.current) }
  }, [rows, grouped, config, axis, aggregationKey, highlight])
  const hit = (clientX: number, clientY: number) => {
    const bounds = canvas.current!.getBoundingClientRect(), x = clientX - bounds.left, y = clientY - bounds.top
    const point = points.current.find(point => Math.hypot(point.x-x, point.y-y) <= 5)
    if (point) return { target: { slice: point.slice, issueId: point.sample.item.id }, item: point.sample.item }
    const line = lines.current.find(line => Math.abs(line.x-x) <= 12 && Math.abs(line.y-y) <= 5)
    if (line) return { target: line.target }
    const row = rows[Math.floor((x-16) / (bounds.width-63) * rows.length)]
    return row ? { target: { slice: row.id } } : undefined
  }
  return <>
    <canvas ref={canvas} className={styles.scatter} aria-label="Issue duration distribution" onMouseMove={event => {
      const { clientX, clientY } = event
      if (frame.current !== undefined) cancelAnimationFrame(frame.current)
      frame.current = requestAnimationFrame(() => { const next = hit(clientX, clientY)?.target; if (!sameInsightTarget(next, highlight)) onHover(next) })
    }} onClick={event => { const next = hit(event.clientX,event.clientY); if (next?.item) onOpenIssue?.(next.item); else if (next) onSelect(next.target) }}/>
    {rows.length <= 100 && <div className={styles.durationLabels}>{rows.map(row => <button type="button" key={row.id} aria-label={row.label} onMouseEnter={() => onHover({ slice: row.id })} onFocus={() => onHover({ slice: row.id })} onBlur={() => onHover(undefined)} onClick={() => onSelect({ slice: row.id })}><span>{row.label}</span></button>)}</div>}
    {pointHover && <div role="tooltip" className={styles.pointTooltip}><strong>{pointHover.item.title}</strong><div><span>{pointHover.item.identifier}</span><b>{formatMetric(pointHover.value,config.measure)}</b></div></div>}
  </>
}

// Keep high-cardinality distributions off the DOM. Hit testing retains the
// original slice/segment IDs; only their raster representation is compacted.
function DenseDistributionGraph({ rows, segments, config, highlight, onHover, onSelect }: {
  rows: InsightRow[]; segments: InsightData['segments']; config: SavedViewInsightsConfig; highlight?: InsightTarget
  onHover: (target?: InsightTarget) => void; onSelect: (target: InsightTarget) => void
}) {
  const canvas = useRef<HTMLCanvasElement>(null)
  const frame = useRef<number | undefined>(undefined)
  const max = rows.reduce((value, row) => Math.max(value, row.total), 1) * 1.1
  const segmentIndex = useMemo(() => new Map(segments.map((segment, index) => [segment.id, { segment, index }])), [segments])
  useEffect(() => {
    const node = canvas.current
    if (!node) return
    const draw = () => {
      const bounds = node.getBoundingClientRect(), ratio = devicePixelRatio || 1
      node.width = bounds.width * ratio; node.height = bounds.height * ratio
      const ctx = node.getContext('2d')
      if (!ctx) return
      ctx.scale(ratio, ratio)
      const css = getComputedStyle(node)
      const color = (value: string) => value.startsWith('var(') ? css.getPropertyValue(value.slice(4, -1)).trim() || css.color : value
      const width = Math.max(1, bounds.width - 44), height = bounds.height - 40, step = width / rows.length
      ctx.font = '10px "Inter Variable", sans-serif'
      for (let i = 0; i <= 4; i++) {
        const y = 10 + height * i / 4
        ctx.strokeStyle = css.getPropertyValue('--insight-grid'); ctx.lineWidth = 1; ctx.setLineDash?.(i < 4 ? [3, 3] : [])
        ctx.beginPath(); ctx.moveTo(16, y); ctx.lineTo(16 + width, y); ctx.stroke()
        ctx.fillStyle = css.color; ctx.fillText(String(Math.round(max * (1 - i / 4))), 20 + width, y + 3)
      }
      rows.forEach((row, index) => {
        let bottom = 10 + height
        Object.entries(row.segments).forEach(([id, value]) => {
          const entry = segmentIndex.get(id)
          if (!entry || !value) return
          const { segment, index: part } = entry
          const cell = { slice: row.id, ...(config.segment !== 'none' ? { segment: segment.id } : {}) }
          ctx.globalAlpha = highlight && insightHighlight(highlight, cell) !== 'strong' ? .2 : 1
          ctx.fillStyle = color(insightColor(config, segment.color, row.color, part))
          const barHeight = height * value / max
          const barWidth = Math.min(10, step)
          ctx.fillRect(16 + index * step + (step - barWidth) / 2, bottom - barHeight, Math.max(1 / ratio, barWidth), barHeight)
          bottom -= barHeight
        })
      })
    }
    const resize = new ResizeObserver(draw); resize.observe(node)
    const theme = new MutationObserver(draw); theme.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'class'] })
    draw()
    return () => { resize.disconnect(); theme.disconnect(); if (frame.current !== undefined) cancelAnimationFrame(frame.current) }
  }, [rows, segmentIndex, config, highlight, max])
  const hit = (x: number, y: number): InsightTarget | undefined => {
    const rect = canvas.current!.getBoundingClientRect()
    if (x < rect.left + 16 || x >= rect.right - 28) return undefined
    const row = rows[Math.floor((x - rect.left - 16) / (rect.width - 44) * rows.length)]
    if (!row) return undefined
    if (config.segment === 'none') return { slice: row.id }
    const value = (rect.bottom - 30 - y) / (rect.height - 40) * max
    let total = 0
    for (const [id, count] of Object.entries(row.segments)) {
      total += count
      if (value >= 0 && value <= total) return { slice: row.id, segment: id }
    }
    return { slice: row.id }
  }
  return <canvas ref={canvas} className={styles.scatter} aria-label="Insight distribution" onMouseMove={event => {
    const { clientX, clientY } = event
    if (frame.current !== undefined) cancelAnimationFrame(frame.current)
    frame.current = requestAnimationFrame(() => { const next = hit(clientX, clientY); if (!sameInsightTarget(next, highlight)) onHover(next) })
  }} onClick={event => { const next = hit(event.clientX, event.clientY); if (next) onSelect(next) }}/>
}

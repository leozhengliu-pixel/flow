import { ResponsiveBar } from '@nivo/bar'
import { ArrowDown, ArrowUp } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import { StatusIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'
import { formatMetric, insightColor, type InsightData, type InsightRow } from './insight-data'
import type { SavedViewInsightsConfig } from './saved-view-panels'
import { aggregateInsightValues, aggregationLabels, durationAxis, insightHighlight, sameInsightTarget, type InsightTarget, type InsightAggregation } from './insight-interaction'
import styles from './insight-explorer.module.css'

const chartTheme = {
  text: { fill: 'var(--theme-text-tertiary)', fontSize: 10 },
  grid: { line: { stroke: 'var(--theme-border)', strokeWidth: .5 } },
  axis: { ticks: { text: { fill: 'var(--theme-text-tertiary)', fontSize: 10 }, line: { stroke: 'transparent' } }, domain: { line: { stroke: 'transparent' } } },
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
  useEffect(() => {
    if (!target) return
    const index = rows.findIndex(row => row.id === target.slice)
    if (index >= 0) virtual.current?.scrollIntoView({ index, align: 'center', behavior: 'auto' })
    const scroller = table.current
    const columnIndex = target.aggregation ? 0 : insight.segments.findIndex(segment => segment.id === target.segment)
    if (scroller && columnIndex >= 0 && (target.segment || target.aggregation)) {
      const left = 256 + columnIndex * 106
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
  }, [target, rows, insight.segments])
  const columns = latency ? (config.aggregations ?? (config.aggregation ? [config.aggregation] : ['median', 'p75', 'p95'] as const)).map(id => ({ id, label: id === 'median' ? 'P50' : aggregationLabels[id] })) : config.segment === 'none' ? [] : insight.segments
  const columnStart = columns.length > 30 ? Math.max(0, Math.floor((scrollLeft - 256) / 106) - 1) : 0
  const visibleColumns = columns.slice(columnStart, columns.length > 30 ? columnStart + Math.ceil((table.current?.clientWidth ?? 1000) / 106) + 4 : undefined)
  const cellOffset = latency ? 2 : 3
  const tableStyle = { '--insight-columns': `minmax(${latency ? 110 : 150}px,1fr)${latency ? '' : ' 106px'}${columns.length ? ` repeat(${columns.length},${latency ? 64 : 106}px)` : ''}`, '--insight-min-width': `${latency ? 110 + columns.length * 64 : 256 + columns.length * 106}px` } as CSSProperties
  const chooseSort = (column: string) => setSort(current => ({ column, descending: current?.column === column ? !current.descending : column !== 'slice' }))
  const mark = (row: InsightRow) => {
    const state = config.slice === 'status' ? data.states.find(state => state.id === row.id) : undefined
    return state ? <StatusIcon state={state} size={14}/> : config.slice === 'priority' ? <PriorityIcon priority={Number(row.id)} size={14}/> : <i className={styles.mark} style={{ background: row.color ?? 'var(--data-vis-neutral)' }}/>
  }
  const headerCell = (id: string, label: string) => <div role="columnheader" aria-sort={sort?.column === id ? sort.descending ? 'descending' : 'ascending' : 'none'} key={id} onMouseEnter={() => id !== 'slice' && id !== 'total' ? setHover(latency ? { aggregation: id, operator, threshold: aggregateInsightValues(insight.samples.map(sample => sample.value), id as InsightAggregation) } : { segment: id }) : setHover(undefined)} data-highlight={insightHighlight(highlight, latency ? { aggregation: id } : { segment: id })}>
    <button type="button" onClick={() => chooseSort(id)}>{t(label)}{sort?.column === id && (sort.descending ? <ArrowDown size={12}/> : <ArrowUp size={12}/>)}</button>
  </div>
  const renderCell = (value: string, cellTarget: InsightTarget, prefix?: React.ReactNode) => <div role="cell" data-highlight={insightHighlight(highlight, cellTarget)} data-selected={target && sameInsightTarget(target, cellTarget) || undefined}>
    <button type="button" aria-pressed={Boolean(target && sameInsightTarget(target, cellTarget))} onMouseEnter={() => setHover(cellTarget)} onFocus={() => setHover(cellTarget)} onBlur={() => setHover(undefined)} onClick={() => onSelect(cellTarget)}>{prefix}<span>{value}</span></button>
  </div>
  const renderRow = (row: InsightRow) => <div role="row" className={styles.row} data-slice={row.id} data-selected-row={target?.slice === row.id && target.segment === undefined && target.aggregation === undefined || undefined}>
    {renderCell(t(row.label), { slice: row.id }, mark(row))}
    {!latency && renderCell(String(row.total), { slice: row.id })}
    {visibleColumns.map((column, index) => {
      const value = latency ? row.aggregations[column.id as InsightAggregation] : row.segments[column.id] ?? 0
      const cellTarget: InsightTarget = latency ? { slice: row.id, aggregation: column.id, operator, threshold: value } : { slice: row.id, segment: column.id }
      return <div className={styles.columnCell} style={{ gridColumn: columnStart + index + cellOffset }} key={column.id}>{value === undefined ? <div role="cell">-</div> : renderCell(formatMetric(value, config.measure), cellTarget)}</div>
    })}
  </div>
  const activeRow = rows.find(row => row.id === highlight?.slice)
  const activeSegment = insight.segments.find(segment => segment.id === highlight?.segment)
  const barData = useMemo(() => rows.length > 100 || insight.segments.length > 100 ? [] : rows.map(row => ({ id: row.id, ...Object.fromEntries(insight.segments.map((segment, i) => [`segment${i}`, row.segments[segment.id] ?? 0])) })), [rows, insight.segments])
  const max = rows.reduce((value, row) => Math.max(value, latency ? row.aggregations.max ?? 0 : row.total), 1)
  const tickStep = Math.max(1, Math.ceil(max / 5))
  const ticks = Array.from({ length: Math.ceil(max / tickStep) + 1 }, (_, i) => i * tickStep)
  const totalRow = <div role="row" className={`${styles.row} ${styles.total}`}>
    {renderCell(t(latency ? config.slice === 'status' ? 'Across all statuses' : 'Across all groups' : 'All issues'), {})}{!latency && renderCell(String(insight.samples.length), {})}
    {visibleColumns.map((column, index) => <div className={styles.columnCell} style={{ gridColumn: columnStart + index + cellOffset }} key={column.id}>{latency ? renderCell(formatMetric(aggregateInsightValues(insight.samples.map(sample => sample.value), column.id as InsightAggregation) ?? 0, config.measure), { aggregation: column.id, operator, threshold: aggregateInsightValues(insight.samples.map(sample => sample.value), column.id as InsightAggregation) ?? 0 }) : renderCell(String(insight.samples.filter(sample => sample.segments.includes(column.id)).length), { segment: column.id })}</div>)}
  </div>
  return <div className={styles.explorer} data-expanded={expanded} onKeyDown={event => { if (event.key === 'Escape' && target) { event.stopPropagation(); onClear() } }}>
    <div className={styles.chart} aria-label={t('Insight chart')} onMouseLeave={() => setHover(undefined)}>
      {!rows.length ? <div className={styles.empty}>{t('No data for this insight')}</div> : latency ? <LatencyGraph rows={rows} insight={insight} config={config} highlight={highlight} onHover={setHover} onSelect={onSelect} onOpenIssue={onOpenIssue}/> : rows.length > 100 || insight.segments.length > 100 ? <DenseDistributionGraph rows={rows} config={config} segments={insight.segments} highlight={highlight} onHover={setHover} onSelect={onSelect}/> : <ResponsiveBar
        data={barData} keys={insight.segments.map((_, i) => `segment${i}`)} indexBy="id" animate={false} isInteractive
        enableLabel={false} enableGridY gridYValues={ticks} valueScale={{ type: 'linear', min: 0, max: max * 1.1, nice: false }}
        axisLeft={null} axisBottom={null} axisTop={null} axisRight={{ tickSize: 0, tickPadding: 8, tickValues: ticks }}
        margin={{ top: 10, right: 28, bottom: 30, left: 16 }} padding={0} theme={chartTheme}
        layers={['grid', 'axes', ({ innerWidth, innerHeight }) => <g key="slice-tracks">{rows.map((row, index) => <g key={row.id}>
          <rect x={index * innerWidth / rows.length} width={Math.max(0, innerWidth / rows.length)} height={Math.max(0, innerHeight)} fill="transparent" onMouseEnter={() => setHover({ slice: row.id })} onClick={() => onSelect({ slice: row.id })}/>
          {highlight?.slice === row.id && <rect x={(index + .5) * innerWidth / rows.length - 5} width={10} height={Math.max(0, innerHeight)} fill="var(--theme-border)" opacity={.5} pointerEvents="none"/>}
        </g>)}</g>, 'bars']}
        enableTotals={false}
        barComponent={({ bar }) => {
          const row = rows.find(row => row.id === String(bar.data.indexValue))!
          const index = Number(String(bar.data.id).slice(7)), segment = insight.segments[index]
          const cellTarget = { slice: row.id, ...(config.segment !== 'none' ? { segment: segment.id } : {}) }
          const active = insightHighlight(highlight, cellTarget) === 'strong'
          const width = Math.min(10, bar.width)
          return <rect x={bar.x + (bar.width - width) / 2} y={bar.y} width={Math.max(0, width)} height={Math.max(0, bar.height)} rx={1}
            fill={insightColor(config, segment.color, row.color, index)} opacity={highlight && !active ? .2 : 1}
            role="button" tabIndex={0} aria-label={`${row.label}, ${config.segment === 'none' ? t('Issue count') : segment.label}: ${bar.data.value}`} aria-pressed={Boolean(target && sameInsightTarget(target, cellTarget))}
            onMouseEnter={() => setHover(cellTarget)} onFocus={() => setHover(cellTarget)} onBlur={() => setHover(undefined)}
            onClick={() => onSelect(cellTarget)} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onSelect(cellTarget) } }}/>
        }}
      />}
      {!latency && rows.length <= 100 && <div className={styles.axisLabels}>{rows.map(row => <button type="button" key={row.id} aria-label={row.label} data-active={highlight?.slice === row.id} onMouseEnter={() => setHover({ slice: row.id })} onFocus={() => setHover({ slice: row.id })} onBlur={() => setHover(undefined)} onClick={() => onSelect({ slice: row.id })}>{rows.length > 4 ? <>{mark(row)}{highlight?.slice === row.id && <span className={styles.axisHoverLabel}>{t(row.label)}</span>}</> : <span>{t(row.label)}</span>}</button>)}</div>}
      {hover && !hover.issueId && activeRow && <div role="tooltip" className={styles.tooltip}><strong>{t(activeRow.label)}</strong><div><span>{activeSegment?.label ?? (hover.aggregation ? aggregationLabels[hover.aggregation as InsightAggregation] : t('Issue count'))}</span><b>{hover.aggregation ? formatMetric(hover.threshold ?? 0, config.measure) : String(activeSegment ? activeRow.segments[activeSegment.id] ?? 0 : latency ? activeRow.values.length : activeRow.total)}</b></div></div>}
    </div>
    <div role="table" aria-label={t('Insights table')} className={styles.table} ref={table} style={tableStyle} onScroll={event => setScrollLeft(event.currentTarget.scrollLeft)} onMouseLeave={() => setHover(undefined)}>
      <div role="row" className={`${styles.row} ${styles.header}`}>{headerCell('slice', sliceLabel)}{!latency && headerCell('total', 'Issue count')}{visibleColumns.map((column, index) => <div className={styles.columnCell} style={{ gridColumn: columnStart + index + cellOffset }} key={column.id}>{headerCell(column.id, column.label)}</div>)}</div>
      {latency && totalRow}
      {rows.length > 100 ? <Virtuoso ref={virtual} className={styles.virtual} style={{ height: 'min(380px, max(152px, calc(100vh - 530px)))', minWidth: 'var(--insight-min-width)' }} data={rows} fixedItemHeight={38} computeItemKey={(_, row) => row.id} itemContent={(_, row) => renderRow(row)}/> : rows.map(row => <div className={styles.contents} key={row.id}>{renderRow(row)}</div>)}
      {!latency && columns.length > 0 && totalRow}
    </div>
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
      const css = getComputedStyle(node), foreground = css.color, faint = css.getPropertyValue('--theme-border')
      const color = (value: string) => value.startsWith('var(') ? css.getPropertyValue(value.slice(4,-1)).trim() || foreground : value
      const width = Math.max(1, rect.width - 63), height = Math.max(1, rect.height - 42), band = width / rows.length
      const yPosition = (value: number) => 10 + height * (1 - axis.fraction(value))
      points.current = []; lines.current = []
      ctx.font = '11px "Inter Variable", sans-serif'
      for (const tick of axis.ticks) {
        const y = yPosition(tick.value)
        ctx.strokeStyle = faint; ctx.lineWidth = .5; ctx.beginPath(); ctx.moveTo(16, y); ctx.lineTo(16 + width, y); ctx.stroke()
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
    {pointHover && <div role="tooltip" className={styles.tooltip}><strong>{pointHover.item.title}</strong><div><span>{pointHover.item.identifier}</span><b>{formatMetric(pointHover.value,config.measure)}</b></div></div>}
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
        ctx.strokeStyle = css.getPropertyValue('--theme-border'); ctx.lineWidth = .5
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

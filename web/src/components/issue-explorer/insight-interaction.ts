export type InsightTarget = {
  issueId?: string
  slice?: string
  segment?: string
  aggregation?: string
  operator?: 'lte' | 'gt'
  threshold?: number
}

export type InsightSample<T> = { item: T; value: number; slices: string[]; segments: string[] }
export type InsightAggregation = 'average' | 'p25' | 'median' | 'p75' | 'p95' | 'min' | 'max'
export const insightAggregations: InsightAggregation[] = ['average', 'p25', 'median', 'p75', 'p95', 'min', 'max']
export const aggregationLabels = { average: 'Average', p25: 'P25', median: 'Median', p75: 'P75', p95: 'P95', min: 'Min', max: 'Max' }

export function sameInsightTarget(a: InsightTarget | undefined, b: InsightTarget | undefined) {
  if (!a || !b) return a === b
  return a?.issueId === b?.issueId && a?.slice === b?.slice && a?.segment === b?.segment && a?.aggregation === b?.aggregation && a?.operator === b?.operator && a?.threshold === b?.threshold
}

export function insightTargetMatches<T>(sample: InsightSample<T>, target: InsightTarget) {
  return (target.slice === undefined || sample.slices.includes(target.slice)) &&
    (target.segment === undefined || sample.segments.includes(target.segment)) &&
    (target.threshold === undefined || (target.operator === 'gt' ? sample.value > target.threshold : sample.value <= target.threshold))
}

export function aggregateInsightValues(values: number[], kind: InsightAggregation) {
  if (!values.length) return undefined
  if (kind === 'average') return values.reduce((a, b) => a + b, 0) / values.length
  const sorted = [...values].sort((a, b) => a - b)
  const fraction = ({ p25: .25, median: .5, p75: .75, p95: .95, min: 0, max: 1 } as const)[kind]
  // Percentile drill-down uses an observed duration boundary, so a small
  // population never acquires an interpolated threshold that no issue has.
  return sorted[Math.max(0, Math.ceil(sorted.length * fraction) - 1)]
}

// Hover temporarily takes precedence; leaving restores the persistent drill target.
export function insightHighlight(target: InsightTarget | undefined, cell: InsightTarget): 'strong' | 'weak' | undefined {
  if (!target) return undefined
  const row = target.slice !== undefined && target.slice === cell.slice
  const column = target.segment !== undefined && target.segment === cell.segment || target.aggregation !== undefined && target.aggregation === cell.aggregation
  const hasColumn = target.segment !== undefined || target.aggregation !== undefined
  if (row && (!hasColumn || column) || column && target.slice === undefined) return 'strong'
  if (row || column) return 'weak'
  return undefined
}

export function durationAxis(values: number[], logarithmic = true) {
  const minute = 60000, hour = 60 * minute, day = 24 * hour, week = 7 * day, month = 4.35 * week, year = 12 * month
  const min = values.reduce((min, value) => Math.min(min, value), Infinity)
  const max = values.reduce((max, value) => Math.max(max, value), 1)
  const ticks: { value: number; label: string }[] = []
  if (logarithmic) {
    if (min <= minute) ticks.push(min < 1000 ? { value: 0, label: '0' } : { value: 1000, label: '1s' })
    const steps: [number, string, number, number][] = [[minute,'1min',1000,hour],[hour,'1h',minute,day],[day,'1d',hour,week],[week,'1w',day,month],[month,'1mo',week,year],[max > 6*month ? year : 6*month,max > 6*month ? '1y' : '6mo',month,10*year],[max > 5*year ? 10*year : 5*year,max > 5*year ? '10y' : '5y',year,Infinity]]
    for (const [value,label,above,below] of steps) if (max > above && min < below) ticks.push({ value,label })
  } else {
    const unit = max < hour ? minute : max < day ? hour : day
    const step = Math.max(1,Math.ceil(max / unit / 4)) * unit
    for (let i=0;i<=4;i++) ticks.push({value: i * step,label: `${i * step / unit}${unit === day ? 'd' : unit === hour ? 'h' : 'min'}`})
  }
  if (!ticks.length) ticks.push({value:0,label:'0'},{value:max,label:`${Math.round(max/day)}d`})
  const low = logarithmic ? Math.min(Number.isFinite(min) ? min : 0, ticks[0].value) : 0
  const high = Math.max(max, ticks.at(-1)!.value, low + 1)
  const transform = (value: number) => logarithmic ? Math.log1p(Math.max(0,value) / minute) : value
  const start = transform(low), span = transform(high) - start
  return { ticks, fraction: (value: number) => (transform(value) - start) / span }
}

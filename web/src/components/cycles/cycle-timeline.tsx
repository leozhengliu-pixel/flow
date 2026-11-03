import { useMemo, useState, type ReactNode } from 'react'
import type { Cycle, Issue } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'
import { EstimateIcon } from '@/components/issue/estimate-icon'
import { formatEstimatePoints } from '@/lib/estimates'
import { cycleStats, cycleStatusText, formatCycleDay } from './cycle-model'
import { cycleGraphSeries } from './cycle-graph-model'
import { cycleChartDays, cycleLegendMetrics } from './cycle-chart-model'
import { CycleChart } from './cycle-chart'
import './cycle-chart.css'
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

/** Capacity ring: the share of the capacity the scope fills, as an arc that always leaves a small gap. */
const RING_RADIUS = 7
const RING_LENGTH = 2 * Math.PI * RING_RADIUS
/** The arc is a round-capped dash: 0.483 long when empty, 40.29 longer when full. */
const RING_EMPTY = .483
const RING_SPAN = 40.29
const RING_START = -76
const RING_GAP = 24.7
const RING_GAP_END = 24.4

export function CycleCapacityRing({ ratio }: { ratio: number }) {
  const share = Math.max(0, Math.min(1, Number.isFinite(ratio) ? ratio : 0))
  const arc = RING_EMPTY + share * RING_SPAN
  const arcDegrees = arc / RING_LENGTH * 360
  const trackDegrees = 360 - arcDegrees - RING_GAP - RING_GAP_END
  const common = { cx: 8, cy: 8, r: RING_RADIUS, fill: 'none', strokeWidth: 2, strokeLinecap: 'round' as const, strokeDasharray: RING_LENGTH }
  return <svg className="cycle-capacity-ring" aria-hidden="true" viewBox="0 0 16 16">
    <circle {...common} className="cycle-capacity-ring__track" transform={`rotate(${RING_START + arcDegrees + RING_GAP} 8 8)`} strokeDashoffset={RING_LENGTH - trackDegrees / 360 * RING_LENGTH}/>
    <circle {...common} className="cycle-capacity-ring__value" transform={`rotate(${RING_START} 8 8)`} strokeDashoffset={RING_LENGTH - arc}/>
  </svg>
}

type CardProps = { cycle: Cycle; issues: Issue[]; stats: ReturnType<typeof cycleStats>; derivedStatus?: string; onOpen: () => void; actions?: ReactNode; className?: string }

/**
 * One cycle on the timeline: its start date on the rail, the cycle and its status, capacity and
 * scope, and, once it has started, the chart with the Scope / Started / Completed legend.
 * The whole card opens the cycle; the options menu is a sibling control above it.
 */
export function CycleTimelineCard({ cycle, issues, stats, derivedStatus = cycle.status, onOpen, actions, className }: CardProps) {
  const { t } = useI18n()
  const upcoming = cycle.status === 'upcoming'
  const refreshKey = useMemo(() => issues.filter(issue => issue.cycleId === cycle.id).map(issue => `${issue.id}:${issue.state.id}:${issue.estimate ?? ''}:${issue.updatedAt}`).join('|'), [issues, cycle.id])
  const { graph } = useCycleGraph(upcoming ? undefined : cycle.id, refreshKey)
  const measure = stats.estimates ? 'points' : 'issues'
  const series = graph ? cycleGraphSeries(graph, measure) : undefined
  const scope = series?.scope ?? stats.scope
  const completedPercent = series?.completedPercent ?? stats.completedPercent
  const date = formatCycleDay(cycle.startsAt)
  // The directory row rounds capacity down ("26% of capacity" for 8 of 30 points).
  const capacityPercent = Math.floor(stats.capacityRatio * 100)
  const [hover, setHover] = useState<number | null>(null)
  const days = useMemo(() => cycleChartDays(cycle.startsAt, cycle.endsAt), [cycle.startsAt, cycle.endsAt])
  const metrics = cycleLegendMetrics({ graph, measure, hoveredDate: hover === null ? undefined : days[hover], scope, started: series?.started ?? stats.started, startedPercent: series?.startedPercent ?? stats.startedPercent, completed: series?.completed ?? stats.completed, completedPercent })
    .map(metric => ({ ...metric, label: t(metric.label) }))
  return <div className={`cycle-timeline-card${upcoming ? ' is-upcoming' : ''}${className ? ` ${className}` : ''}`} data-testid={`cycle-timeline-${cycle.number}`} data-status={cycle.status} data-current={cycle.status === 'current'}>
    <button type="button" className="cycle-timeline-open" aria-label={cycle.name} data-i18n-ignore onClick={onOpen}/>
    <span className="cycle-timeline-date">{date.month}<br/>{date.day}</span>
    <span className="cycle-timeline-heading"><TimelineCycleIcon upcoming={upcoming} progress={completedPercent}/><span className="cycle-timeline-title" data-i18n-ignore>{cycle.name}</span></span>
    <span className={`cycle-timeline-status is-${derivedStatus}`}>{t(cycleStatusText(derivedStatus))}</span>
    <span className="cycle-timeline-capacity"><CycleCapacityRing ratio={stats.capacityRatio}/><b>{capacityPercent}%</b>{' '}<em>{t('of capacity')}</em></span>
    <span className="cycle-timeline-scope">{cycle.status === 'completed'
      ? <><b>{stats.successPercent}%</b>{' '}<em>{t('success')}</em></>
      : <>{stats.estimates && <EstimateIcon size={14}/>}<b>{formatEstimatePoints(stats.scope)}</b>{' '}<em>{t('scope')}</em></>}</span>
    {actions}
    {!upcoming && <>
      <CycleChart graph={graph} measure={measure} startsAt={cycle.startsAt} endsAt={cycle.endsAt} height={210} label={`${t('Cycle progress')}: ${completedPercent}%`} className="cycle-timeline-chart" hover={hover} onHover={setHover} onOpen={onOpen}/>
      <span className="cycle-timeline-metrics">{metrics.map(metric => <span className={`cycle-timeline-metric is-${metric.key}`} key={metric.key}>
        <i/><span className="cycle-timeline-metric-label">{metric.label}</span><span className="cycle-timeline-metric-value">{stats.estimates && <EstimateIcon size={14}/>}<strong>{metric.count === null ? '-' : formatEstimatePoints(metric.count)}</strong>{metric.percent !== undefined && metric.count !== null && <span className="cycle-timeline-percent"><b>•</b>{metric.percent}%</span>}</span>
      </span>)}</span>
    </>}
  </div>
}

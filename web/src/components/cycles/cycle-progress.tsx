import { useMemo, useState } from 'react'

import type { CycleGraphData } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import { EstimateIcon } from '@/components/issue/estimate-icon'
import { formatEstimatePoints } from '@/lib/estimates'
import type { Cycle, Issue } from '@/types/flow'
import type { CycleStats } from './cycle-model'
import { CycleBreakdown } from './cycle-breakdown'
import { CycleChart } from './cycle-chart'
import { cycleChartDays, cycleLegendMetrics } from './cycle-chart-model'
import { CyclePanelSection } from './cycle-panel'
import { cycleGraphSeries } from './cycle-graph-model'
import type { CyclePlanningSettings } from './cycle-planning-model'

/**
 * Cycle details card: "Progress" of a started cycle — the Scope / Started / Completed legend, the
 * chart (hover a day to read its figures) and the breakdown with how much of each row is done.
 */
export function CycleProgress({ cycle, graph, issues, stats, settingsFor }: { cycle: Cycle; graph: CycleGraphData; issues: readonly Issue[]; stats: CycleStats; settingsFor: CyclePlanningSettings }) {
  const { t } = useI18n()
  const [hover, setHover] = useState<number | null>(null)
  const measure = graph.estimates ? 'points' : 'issues'
  const series = cycleGraphSeries(graph, measure)
  const days = useMemo(() => cycleChartDays(cycle.startsAt, cycle.endsAt), [cycle.startsAt, cycle.endsAt])
  const metrics = cycleLegendMetrics({ graph, measure, hoveredDate: hover === null ? undefined : days[hover], scope: series.scope, started: series.started, startedPercent: series.startedPercent, completed: series.completed, completedPercent: series.completedPercent })
  return <CyclePanelSection className="cycle-panel-progress" title={t('Progress')} collapseLabel="Collapse progress section" expandLabel="Expand progress section">
    <div className="cycle-progress" data-cycle-progress="true">
      <div className="cycle-stat-grid" role="list">
        {metrics.map(metric => <div role="listitem" className={`cycle-stat is-${metric.key}`} key={metric.key}>
          <i/>
          <span className="cycle-stat-label">{t(metric.label)}</span>
          <span className="cycle-stat-value">
            {graph.estimates && <EstimateIcon size={14}/>}
            <strong>{metric.count === null ? '-' : formatEstimatePoints(metric.count)}</strong>
            {metric.percent !== undefined && metric.count !== null && <span className="cycle-stat-percent"><b>•</b>{metric.percent}%</span>}
          </span>
        </div>)}
      </div>
      <CycleChart graph={graph} measure={measure} startsAt={cycle.startsAt} endsAt={cycle.endsAt} height={200} label={`${t('Cycle progress')}: ${series.completedPercent}%`} className="cycle-progress-chart" hover={hover} onHover={setHover}/>
      <CycleBreakdown issues={issues} estimates={stats.estimates} settingsFor={settingsFor} progress/>
    </div>
  </CyclePanelSection>
}

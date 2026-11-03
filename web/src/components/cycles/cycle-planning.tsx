import { Clock } from 'lucide-react'

import type { Cycle, Issue } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'
import { EstimateIcon } from '@/components/issue/estimate-icon'
import { formatEstimatePoints } from '@/lib/estimates'
import type { CycleStats } from './cycle-model'
import { CycleBreakdown } from './cycle-breakdown'
import { CyclePanelSection } from './cycle-panel'
import { cyclePlanningDays, type CyclePlanningSettings } from './cycle-planning-model'
import './cycle-graph.css'

/**
 * Cycle details card: "Planning" — Capacity, Days to start / remaining and Scope. The breakdown
 * tabs follow unless the Progress section above already carries them.
 */
export function CyclePlanning({ cycle, issues, stats, settingsFor, breakdown = true }: { cycle: Cycle; issues: readonly Issue[]; stats: CycleStats; settingsFor: CyclePlanningSettings; breakdown?: boolean }) {
  const { t } = useI18n()
  return <CyclePanelSection className="cycle-planning" title={t('Planning')} collapseLabel="Collapse planning section" expandLabel="Expand planning section">
    <dl className="cycle-stat-grid">
      <div className="cycle-stat is-capacity"><dt><i/>{t('Capacity')}</dt><dd>{stats.capacityPercent}%</dd></div>
      <div className="cycle-stat is-days"><dt><Clock aria-hidden="true"/>{t(cycle.status === 'current' ? 'Days remaining' : 'Days to start')}</dt><dd>{cyclePlanningDays(cycle)}</dd></div>
      <div className="cycle-stat is-scope"><dt><i/>{t('Scope')}</dt><dd className="cycle-planning-scope">{stats.estimates && <EstimateIcon size={14}/>}<span>{formatEstimatePoints(stats.scope)}</span></dd></div>
    </dl>
    {breakdown && issues.length > 0 && <CycleBreakdown issues={issues} estimates={stats.estimates} settingsFor={settingsFor}/>}
  </CyclePanelSection>
}

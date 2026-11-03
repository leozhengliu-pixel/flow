import { useMemo, useState } from 'react'

import type { Issue } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'
import { EstimateIcon } from '@/components/issue/estimate-icon'
import { formatEstimatePoints } from '@/lib/estimates'
import { cyclePlanningRows, type CyclePlanningSettings, type CyclePlanningTab } from './cycle-planning-model'
import { CycleCapacityRing } from './cycle-timeline'

const TABS: { id: CyclePlanningTab; label: string }[] = [
  { id: 'assignees', label: 'Assignees' },
  { id: 'labels', label: 'Labels' },
  { id: 'priority', label: 'Priority' },
  { id: 'projects', label: 'Projects' },
  { id: 'teams', label: 'Teams' },
]

/**
 * The cycle's scope split by assignee, label, priority, project or team. With `progress` each row
 * also says how much of it is done ("42% of 8"), as in a started cycle's Progress section; without
 * it a row is just its scope, as in Planning.
 */
export function CycleBreakdown({ issues, estimates, settingsFor, progress = false }: { issues: readonly Issue[]; estimates: boolean; settingsFor: CyclePlanningSettings; progress?: boolean }) {
  const { t } = useI18n()
  const [tab, setTab] = useState<CyclePlanningTab>('assignees')
  const rows = useMemo(() => cyclePlanningRows(issues, tab, settingsFor), [issues, settingsFor, tab])
  const points = (value: number) => <>{estimates && <EstimateIcon size={14}/>}<span>{formatEstimatePoints(value)}</span></>
  return <div className="cycle-breakdown cycle-planning-breakdown">
    <div className="cycle-breakdown-tabs" role="tablist" aria-label={t('Breakdown')}>
      {TABS.map(item => <button key={item.id} type="button" role="tab" aria-selected={tab === item.id} onClick={() => setTab(item.id)}>{t(item.label)}</button>)}
    </div>
    <div role="tabpanel" className="cycle-breakdown-rows">
      {rows.map(row => <div key={row.id || 'none'} className="cycle-breakdown-row cycle-planning-row">
        <span className="cycle-breakdown-name">{row.icon}<span data-i18n-ignore={row.id && tab !== 'priority' ? true : undefined}>{row.id && tab !== 'priority' ? row.name : t(row.name)}</span></span>
        {progress && <span className="cycle-breakdown-done"><CycleCapacityRing ratio={row.value ? row.completed / row.value : 0}/><span>{row.value ? Math.round(row.completed / row.value * 100) : 0}% {t('of')}</span></span>}
        <span className="cycle-planning-value">{points(row.value)}</span>
      </div>)}
    </div>
  </div>
}

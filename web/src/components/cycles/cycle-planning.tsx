import { useMemo, useState } from 'react'

import type { Cycle, Issue } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'
import { EstimateIcon } from '@/components/issue/estimate-icon'
import { formatEstimatePoints } from '@/lib/estimates'
import type { CycleStats } from './cycle-model'
import { cyclePlanningDays, cyclePlanningRows, type CyclePlanningSettings, type CyclePlanningTab } from './cycle-planning-model'
import './cycle-graph.css'

const TABS: { id: CyclePlanningTab; label: string }[] = [
  { id: 'assignees', label: 'Assignees' },
  { id: 'labels', label: 'Labels' },
  { id: 'priority', label: 'Priority' },
  { id: 'projects', label: 'Projects' },
  { id: 'teams', label: 'Teams' },
]

/** Cycle details sidebar: "Planning" — Capacity, Days to start / remaining, Scope and the breakdown tabs. */
export function CyclePlanning({ cycle, issues, stats, settingsFor }: { cycle: Cycle; issues: readonly Issue[]; stats: CycleStats; settingsFor: CyclePlanningSettings }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(true)
  const [tab, setTab] = useState<CyclePlanningTab>('assignees')
  const rows = useMemo(() => cyclePlanningRows(issues, tab, settingsFor), [issues, settingsFor, tab])
  const points = (value: number) => <>{stats.estimates && <EstimateIcon size={14}/>}<span>{formatEstimatePoints(value)}</span></>
  return <section className="cycle-planning">
    <button aria-expanded={open} aria-label={t(open ? 'Collapse planning section' : 'Expand planning section')} onClick={() => setOpen(value => !value)}>{t('Planning')} <span>▾</span></button>
    {open && <>
      <dl>
        <div><dt>{t('Capacity')}</dt><dd>{stats.capacityPercent}%</dd></div>
        <div><dt>{t(cycle.status === 'current' ? 'Days remaining' : 'Days to start')}</dt><dd>{cyclePlanningDays(cycle)}</dd></div>
        <div><dt>{t('Scope')}</dt><dd className="cycle-planning-scope">{points(stats.scope)}</dd></div>
      </dl>
      {issues.length > 0 && <div className="cycle-planning-breakdown">
        <div className="cycle-breakdown-tabs" role="tablist" aria-label={t('Breakdown')}>
          {TABS.map(item => <button key={item.id} type="button" role="tab" aria-selected={tab === item.id} onClick={() => setTab(item.id)}>{t(item.label)}</button>)}
        </div>
        <div role="tabpanel">
          {rows.map(row => <div key={row.id || 'none'} className="cycle-breakdown-row cycle-planning-row">
            <span>{row.icon}<span data-i18n-ignore={row.id && tab !== 'priority' ? true : undefined}>{row.id && tab !== 'priority' ? row.name : t(row.name)}</span></span>
            <span className="cycle-planning-value">{points(row.value)}</span>
          </div>)}
        </div>
      </div>}
    </>}
  </section>
}

import { useState } from 'react'

import type { CycleGraphBreakdownRow, CycleGraphData } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'
import { CycleGraph } from './cycle-graph'
import { cycleGraphSeries, measureValue, type CycleGraphMeasure } from './cycle-graph-model'

type BreakdownTab = 'assignees' | 'labels' | 'projects'

/** Cycle details sidebar: Linear's Scope / Started / Completed stats, graph and breakdown. */
export function CycleProgress({ graph, data, measure, onMeasureChange }: { graph: CycleGraphData; data: Pick<BootstrapData, 'users' | 'labels' | 'projects'>; measure: CycleGraphMeasure; onMeasureChange: (measure: CycleGraphMeasure) => void }) {
  const { t } = useI18n()
  const [tab, setTab] = useState<BreakdownTab>('assignees')
  const effective: CycleGraphMeasure = graph.estimates ? measure : 'issues'
  const series = cycleGraphSeries(graph, effective)
  const round = (value: number) => Math.round(value * 10) / 10
  const scopeChange = [series.added ? `+${round(series.added)}` : '', series.removed ? `−${round(series.removed)}` : ''].filter(Boolean).join(' ')
  const name = (row: CycleGraphBreakdownRow) => {
    if (tab === 'assignees') return row.id ? data.users.find(user => user.id === row.id)?.displayName ?? t('Unknown') : t('No assignee')
    if (tab === 'labels') return data.labels.find(label => label.id === row.id)?.name ?? t('Unknown')
    return row.id ? data.projects.find(project => project.id === row.id)?.name ?? t('Unknown') : t('No project')
  }
  const rows = graph.breakdown[tab] ?? []
  return (
    <div className="cycle-progress" data-cycle-progress="true">
      <div className="cycle-progress-stats">
        <div><small>{t('Scope')}</small><strong>{round(series.scope)}</strong>{scopeChange ? <em aria-label={t('Scope changes')}>{scopeChange}</em> : null}</div>
        <div><small>{t('Started')}</small><strong>{round(series.started)}</strong><em>{series.startedPercent}%</em></div>
        <div><small>{t('Completed')}</small><strong>{round(series.completed)}</strong><em>{series.completedPercent}%</em></div>
      </div>
      <CycleGraph graph={graph} measure={effective} onMeasureChange={onMeasureChange} compact/>
      <div className="cycle-breakdown-tabs" role="tablist" aria-label={t('Breakdown')}>
        {(['assignees', 'labels', 'projects'] as const).map(value => (
          <button key={value} type="button" role="tab" aria-selected={tab === value} onClick={() => setTab(value)}>{t(value === 'assignees' ? 'Assignees' : value === 'labels' ? 'Labels' : 'Projects')}</button>
        ))}
      </div>
      <div role="tabpanel">
        {rows.length ? rows.map(row => {
          const scope = measureValue(row, 'scope', effective)
          const completed = measureValue(row, 'completed', effective)
          return (
            <div key={row.id || 'none'} className="cycle-breakdown-row">
              <span data-i18n-ignore>{name(row)}</span>
              <span>{scope ? Math.round(completed / scope * 100) : 0}% {t('of')} {round(scope)}</span>
            </div>
          )
        }) : <p className="cycle-breakdown-empty">{t('No matching issues')}</p>}
      </div>
    </div>
  )
}

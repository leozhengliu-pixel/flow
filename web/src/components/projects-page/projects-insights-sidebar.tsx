import type { ProjectPageItem } from './projects-data-view'
import './projects-page.css'

export type ProjectInsightFilter = { kind: 'health' | 'lead', value: string } | null
export type ProjectInsightMode = 'health' | 'initiatives' | 'leads'

type ProjectInsightRow = {
  className?: string
  count: number
  initials?: string
  label: string
  value: string
}

export function ProjectsInsightsSidebar({ activeFilter, mode, onChangeFilter, onChangeMode, projects }: {
  activeFilter: ProjectInsightFilter
  mode: ProjectInsightMode
  onChangeFilter: (filter: ProjectInsightFilter) => void
  onChangeMode: (mode: ProjectInsightMode) => void
  projects: ProjectPageItem[]
}) {
  const rows: ProjectInsightRow[] = mode === 'health' ? healthRows(projects) : mode === 'leads' ? leadRows(projects) : []
  const filterKind = mode === 'health' ? 'health' : 'lead'
  return <aside aria-label="Project insights" className="lp-project-insights">
    <div aria-label="Project insight grouping" className="lp-project-insights__tabs" role="tablist">
      <button aria-selected={mode === 'health'} onClick={() => onChangeMode('health')} role="tab" type="button">Health</button>
      <button aria-selected={mode === 'initiatives'} onClick={() => onChangeMode('initiatives')} role="tab" type="button">Initiatives</button>
      <button aria-selected={mode === 'leads'} onClick={() => onChangeMode('leads')} role="tab" type="button">Leads</button>
    </div>
    {mode === 'initiatives' ? <div className="lp-project-insights__empty">No initiatives</div> : <div className="lp-project-insights__rows">
      {rows.map(row => {
        const active = activeFilter?.kind === filterKind && activeFilter.value === row.value
        return <button aria-pressed={active} key={row.value} onClick={() => onChangeFilter(active ? null : { kind: filterKind, value: row.value })} type="button">
          <span className={`lp-project-insights__marker ${row.className ?? ''}`}>{row.initials ?? ''}</span>
          <span>{row.label}</span>
          <span aria-label="Project count">{row.count}</span>
        </button>
      })}
    </div>}
  </aside>
}

function healthRows(projects: ProjectPageItem[]) {
  const definitions = [
    { value: 'no-update', label: 'No update expected', className: 'is-no-update' },
    { value: 'on-track', label: 'On track', className: 'is-on-track' },
    { value: 'at-risk', label: 'At risk', className: 'is-at-risk' },
    { value: 'off-track', label: 'Off track', className: 'is-off-track' },
  ]
  return definitions.map(item => ({ ...item, count: projects.filter(project => project.health === item.value).length })).filter(item => item.count)
}

function leadRows(projects: ProjectPageItem[]) {
  const leads = new Map<string, { label: string, initials: string, count: number }>()
  for (const project of projects) {
    const value = project.lead?.id ?? ''
    const current = leads.get(value) ?? { label: project.lead?.name ?? 'No lead', initials: project.lead?.initials ?? initials(project.lead?.name), count: 0 }
    current.count += 1
    leads.set(value, current)
  }
  return [...leads.entries()].map(([value, item]) => ({ value, ...item }))
}

function initials(name?: string) {
  if (!name) return '—'
  return name.split(/\s|@/).filter(Boolean).slice(0, 2).map(value => value[0]?.toUpperCase()).join('')
}

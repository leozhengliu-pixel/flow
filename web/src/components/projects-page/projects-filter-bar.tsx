import type { ReactNode } from 'react'
import { AppliedFilterBar } from '@/components/filter/applied-filter-bar'
import bandStyles from '@/components/issue-explorer/saved-view-filter-band.module.css'
import { filterOperatorChoices } from '@/components/my-issues/my-issues-filter-types'
import { useI18n } from '@/i18n/i18n'
import type { ProjectFilter, ProjectFilterField, ProjectFilterOption } from './projects-filter-model'

export function ProjectsFilterBar({ band = false, commands, filters, options, onAdd, onChange, onClear, onRemove, onSave }: {
  /** Linear's grey band for unsaved filters on a saved view. */
  band?: boolean
  commands?: ReactNode
  filters: ProjectFilter[]
  options: Partial<Record<ProjectFilterField, ProjectFilterOption[]>>
  onAdd: () => void
  onChange: (filter: ProjectFilter) => void
  onClear: () => void
  onRemove: (id: string) => void
  onSave?: () => void
}) {
  const { t } = useI18n()
  // Linear's operator labels: several values read "is any of".
  const items = filters.map(filter => ({ ...filter, operatorChoices: filterOperatorChoices('status', filter.values.map(value => value.id)).filter((choice): choice is { operator: ProjectFilter['operator']; label: string } => choice.operator === 'is' || choice.operator === 'isNot') }))
  return <AppliedFilterBar ariaLabel="Applied project filters" className={band ? bandStyles.band : undefined} commands={commands} compact={band} wrap={band} countLabel={count => count === 1 ? 'project' : 'projects'} filters={items} onAdd={onAdd} onClear={onClear} onOperatorChange={(filter, operator) => onChange({ ...filters.find(item => item.id === filter.id)!, operator })} onRemove={filter => onRemove(filter.id)} onSave={onSave} onValuesChange={(filter, values) => values.length ? onChange({ ...filters.find(item => item.id === filter.id)!, values }) : onRemove(filter.id)} optionsFor={filter => options[filter.field] ?? []} translate={t}/>
}

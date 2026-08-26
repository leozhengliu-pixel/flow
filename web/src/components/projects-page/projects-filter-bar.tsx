import { AppliedFilterBar } from '@/components/filter/applied-filter-bar'
import type { ProjectFilter, ProjectFilterField, ProjectFilterOption } from './projects-filter-model'

export function ProjectsFilterBar({ filters, options, onAdd, onChange, onClear, onRemove, onSave }: {
  filters: ProjectFilter[]
  options: Partial<Record<ProjectFilterField, ProjectFilterOption[]>>
  onAdd: () => void
  onChange: (filter: ProjectFilter) => void
  onClear: () => void
  onRemove: (id: string) => void
  onSave?: () => void
}) {
  return <AppliedFilterBar ariaLabel="Applied project filters" countLabel={count => count === 1 ? 'project' : 'projects'} filters={filters} onAdd={onAdd} onClear={onClear} onOperatorChange={(filter, operator) => onChange({ ...filter, operator })} onRemove={filter => onRemove(filter.id)} onSave={onSave} onValuesChange={(filter, values) => values.length ? onChange({ ...filter, values }) : onRemove(filter.id)} optionsFor={filter => options[filter.field] ?? []}/>
}

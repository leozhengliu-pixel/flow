import { AppliedFilterBar, type AppliedFilterItem } from '@/components/filter/applied-filter-bar'
import { useI18n } from '@/i18n/i18n'
import { filterValues, type MyIssuesAppliedFilter, type MyIssuesFilterOperator } from './my-issues-filter-types'
import type { MyIssuesFilterOption } from './my-issues-surface'

export type { MyIssuesAppliedFilter, MyIssuesFilterOperator } from './my-issues-filter-types'

type NormalizedFilter = AppliedFilterItem<MyIssuesFilterOption> & { field: MyIssuesAppliedFilter['field'] }

export function MyIssuesFilterBar({ filters, filterOptions, saveState = 'idle', onAdd, onClear, onOperatorChange, onRemove, onSave, onValuesChange }: {
  filters: MyIssuesAppliedFilter[]
  filterOptions?: (filter: MyIssuesAppliedFilter) => MyIssuesFilterOption[] | undefined
  saveState?: 'idle' | 'saving' | 'saved' | 'error'
  onAdd?: () => void
  onClear: () => void
  onOperatorChange?: (id: string, operator: MyIssuesFilterOperator) => void
  onRemove: (id: string) => void
  onSave?: () => void
  onValuesChange?: (id: string, options: MyIssuesFilterOption[]) => void
}) {
  const { t } = useI18n()
  const normalized: NormalizedFilter[] = filters.map(filter => ({ ...filter, values: filterValues(filter).map(value => ({ id: value.value, label: value.valueLabel, color: value.color })) }))
  const original = (id: string) => filters.find(filter => filter.id === id)!
  return <AppliedFilterBar ariaLabel="Applied filters" countLabel={count => t(count === 1 ? 'issue' : 'issues')} fieldVisual={filter => <AppliedFilterGlyph field={filter.field}/>} filters={normalized} onAdd={onAdd ?? (() => {})} onClear={onClear} onOperatorChange={(filter, operator) => onOperatorChange?.(filter.id, operator)} onRemove={filter => onRemove(filter.id)} onSave={onSave} onValuesChange={(filter, values) => onValuesChange?.(filter.id, values)} optionsFor={filter => filterOptions?.(original(filter.id)) ?? []} saveState={saveState} translate={t}/>
}

function AppliedFilterGlyph({ field }: { field: MyIssuesAppliedFilter['field'] }) {
  if (field === 'priority') return <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true"><rect x="1" y="8" width="3" height="5" rx="1"/><rect x="5.5" y="5" width="3" height="8" rx="1"/><rect x="10" y="2" width="3" height="11" rx="1"/></svg>
  return <i/>
}

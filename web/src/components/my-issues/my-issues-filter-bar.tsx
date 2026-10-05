import type { ReactNode } from 'react'
import { AppliedFilterBar } from '@/components/filter/applied-filter-bar'
import { useI18n } from '@/i18n/i18n'
import { FilterGlyph } from '@/components/issue/filter-glyph'
import type { MyIssuesAppliedFilter, MyIssuesFilterOperator } from './my-issues-filter-types'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from './my-issues-surface'
import { FilterFieldIcon, OptionMark } from './my-issues-filter-menu'
import { chipValueOptions, filterChipItem, type NormalizedFilter } from './my-issues-filter-chips'

export type { MyIssuesAppliedFilter, MyIssuesFilterOperator } from './my-issues-filter-types'


const FIELD_LABELS: Partial<Record<MyIssuesFilterKey, string>> = { status: 'Status', assignee: 'Assignee', agent: 'Agent', creator: 'Creator', priority: 'Priority', labels: 'Labels', project: 'Project', cycle: 'Cycle', subscribers: 'Subscribers', dates: 'Dates', relations: 'Relations', links: 'Links', initiative: 'Initiative' }

export function FilterFieldGlyph({ field }: { field: MyIssuesFilterKey }) {
  return <FilterGlyph label={FIELD_LABELS[field] ?? field} fallback={<FilterFieldIcon field={field}/>}/>
}

export function MyIssuesFilterBar({ className, commands, compact, wrap, filters, filterOptions, renderFilter, saveState = 'idle', showEmpty, onAdd, onClear, onOperatorChange, onRemove, onSave, onValuesChange }: {
  className?: string
  commands?: ReactNode
  compact?: boolean
  wrap?: boolean
  filters: MyIssuesAppliedFilter[]
  filterOptions?: (filter: MyIssuesAppliedFilter) => MyIssuesFilterOption[] | undefined
  /** Custom chip (the advanced-filter chip); undefined renders the default chip. */
  renderFilter?: (filter: MyIssuesAppliedFilter) => ReactNode | undefined
  saveState?: 'idle' | 'saving' | 'saved' | 'error'
  showEmpty?: boolean
  onAdd?: () => void
  onClear: () => void
  onOperatorChange?: (id: string, operator: MyIssuesFilterOperator) => void
  onRemove: (id: string) => void
  onSave?: () => void
  onValuesChange?: (id: string, options: MyIssuesFilterOption[]) => void
}) {
  const { t } = useI18n()
  const original = (id: string) => filters.find(filter => filter.id === id)!
  const normalized: NormalizedFilter[] = filters.map(filter => filter.field === 'advanced' ? { id: filter.id, field: filter.field, fieldLabel: filter.fieldLabel, operator: filter.operator, values: [] } : filterChipItem(filter, filterOptions?.(filter)))
  return <AppliedFilterBar ariaLabel="Applied filters" className={className} commands={commands} compact={compact} wrap={wrap} countLabel={count => t(count === 1 ? 'issue' : 'issues')} fieldVisual={filter => <FilterFieldGlyph field={filter.field}/>} filters={normalized} onAdd={onAdd ?? (() => {})} onClear={onClear} onOperatorChange={(filter, operator) => onOperatorChange?.(filter.id, operator)} onRemove={filter => onRemove(filter.id)} onSave={onSave} onValuesChange={(filter, values) => onValuesChange?.(filter.id, values)} optionsFor={filter => chipValueOptions(filter.fieldLabel, filterOptions?.(original(filter.id))).map(option => ({ ...option, icon: <OptionMark field={filter.field} option={option}/> }))} renderFilter={renderFilter ? filter => renderFilter(original(filter.id)) : undefined} saveState={saveState} showEmpty={showEmpty} translate={t}/>
}

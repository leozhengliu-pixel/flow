import { useState, type ReactNode } from 'react'
import { AppliedFilterBar } from '@/components/filter/applied-filter-bar'
import { useI18n } from '@/i18n/i18n'
import { FilterGlyph } from '@/components/issue/filter-glyph'
import type { MyIssuesAppliedFilter, MyIssuesFilterOperator } from './my-issues-filter-types'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from './my-issues-surface'
import { FilterFieldIcon, OptionMark } from './my-issues-filter-menu'
import { chipValueOptions, filterChipItem, type NormalizedFilter } from './my-issues-filter-chips'
import { filterValues, isComparableDateValue, isReleasedDateValue } from './my-issues-filter-types'
import { DateFilterDialog } from '@/components/issue-explorer/date-filter-dialog'
import { DateAfterGlyph, DateBeforeGlyph, DateOnGlyph } from '@/components/issue-explorer/date-operator-glyphs'
import { customDateLabel, dateFilterValueLabel, isCustomDateValue, parseDateFilterValue, type DateFilterKind, type DateFilterOperator } from '@/components/issue-explorer/issue-date-filter'

export type { MyIssuesAppliedFilter, MyIssuesFilterOperator } from './my-issues-filter-types'


const FIELD_LABELS: Partial<Record<MyIssuesFilterKey, string>> = { team: 'Team', status: 'Status', assignee: 'Assignee', agent: 'Agent', creator: 'Creator', priority: 'Priority', labels: 'Labels', project: 'Project', cycle: 'Cycle', subscribers: 'Subscribers', dates: 'Dates', relations: 'Relations', links: 'Links', initiative: 'Initiative' }

export function FilterFieldGlyph({ field }: { field: MyIssuesFilterKey }) {
  return <FilterGlyph label={FIELD_LABELS[field] ?? field} fallback={<FilterFieldIcon field={field}/>}/>
}

export function MyIssuesFilterBar({ className, commands, compact, wrap, filters, filterOptions, renderFilter, saveLabel = 'Save', saveState = 'idle', showEmpty, onAdd, onClear, onOperatorChange, onRemove, onSave, onValuesChange }: {
  className?: string
  commands?: ReactNode
  compact?: boolean
  wrap?: boolean
  filters: MyIssuesAppliedFilter[]
  filterOptions?: (filter: MyIssuesAppliedFilter) => MyIssuesFilterOption[] | undefined
  /** Custom chip (the advanced-filter chip); undefined renders the default chip. */
  renderFilter?: (filter: MyIssuesAppliedFilter) => ReactNode | undefined
  saveLabel?: string
  saveState?: 'idle' | 'saving' | 'saved' | 'error'
  showEmpty?: boolean
  onAdd?: () => void
  onClear: () => void
  onOperatorChange?: (id: string, operator: MyIssuesFilterOperator) => void
  onRemove: (id: string) => void
  onSave?: () => void
  onValuesChange?: (id: string, options: MyIssuesFilterOption[]) => void
}) {
  const { t, locale } = useI18n()
  const [editingDate, setEditingDate] = useState<string>()
  const original = (id: string) => filters.find(filter => filter.id === id)!
  const normalized: NormalizedFilter[] = filters.map(filter => filter.field === 'advanced' ? { id: filter.id, field: filter.field, fieldLabel: filter.fieldLabel, operator: filter.operator, values: [] } : withDateEditing(translateDateValues(filterChipItem(filter, filterOptions?.(filter)), t, locale), () => setEditingDate(filter.id)))
  const editing = editingDate ? filters.find(filter => filter.id === editingDate) : undefined
  const editingValue = editing ? filterValues(editing)[0]?.value : undefined
  const editingParsed = editingValue ? parseDateFilterValue(editingValue) : undefined
  return <><AppliedFilterBar ariaLabel="Applied filters" className={className} commands={commands} compact={compact} wrap={wrap} countLabel={count => t(count === 1 ? 'issue' : 'issues')} fieldVisual={filter => <FilterFieldGlyph field={filter.field}/>} filters={normalized} onAdd={onAdd ?? (() => {})} onClear={onClear} onOperatorChange={(filter, operator) => onOperatorChange?.(filter.id, operator)} onRemove={filter => onRemove(filter.id)} onSave={onSave} onValuesChange={(filter, values) => onValuesChange?.(filter.id, values)} optionsFor={filter => chipValueOptions(filter.fieldLabel, filterOptions?.(original(filter.id))).map(option => ({ ...option, icon: <OptionMark field={filter.field} option={option}/> }))} renderFilter={renderFilter ? filter => renderFilter(original(filter.id)) : undefined} saveLabel={saveLabel} saveState={saveState} showEmpty={showEmpty} translate={t}/>
    {editing && editingParsed && <DateFilterDialog open fieldLabel={editing.fieldLabel} kind={editingParsed.kind as DateFilterKind} value={editingValue!.slice(editingValue!.indexOf(':') + 1)} operator={editing.operator as DateFilterOperator} onClose={() => setEditingDate(undefined)} onApply={(value, operator) => {
      const id = `${editingParsed.kind}:${value}`
      onValuesChange?.(editing.id, [{ id, label: dateFilterValueLabel(id) ?? value, dateOperator: operator }])
      setEditingDate(undefined)
    }}/>}
  </>
}

/** Date presets are Flow's own copy ("1 week ago", "No released date"): chips show them translated; custom days and periods in the app language. */
function translateDateValues(item: NormalizedFilter, t: (source: string) => string, locale = 'en-US'): NormalizedFilter {
  const dated = item.field === 'dates' || item.values.every(value => isComparableDateValue(value.id) || isReleasedDateValue(value.id))
  if (!dated || !item.values.length) return item
  return { ...item, values: item.values.map(value => {
    const parsed = parseDateFilterValue(value.id)
    return { ...value, label: parsed?.date || parsed?.period ? customDateLabel(parsed, Date.now(), locale) ?? value.label : t(value.label) }
  }) }
}

const DATE_OPERATOR_GLYPHS = { dateOn: DateOnGlyph, dateBefore: DateBeforeGlyph, dateAfter: DateAfterGlyph }

/** The reference app: a custom day or period reopens the date dialog from its chip, and its operators carry calendar marks. */
function withDateEditing(item: NormalizedFilter, open: () => void): NormalizedFilter {
  const operatorChoices = item.operatorChoices?.map(choice => {
    const Glyph = 'glyph' in choice && choice.glyph ? DATE_OPERATOR_GLYPHS[choice.glyph as keyof typeof DATE_OPERATOR_GLYPHS] : undefined
    return Glyph ? { ...choice, icon: <Glyph/> } : choice
  })
  const custom = (item.field === 'dates' || item.field === 'releases') && item.values.length === 1 && isCustomDateValue(item.values[0].id)
  return { ...item, operatorChoices, onValueClick: custom ? open : undefined }
}

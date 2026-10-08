import type { ReactNode } from 'react'
import type { AppliedFilterItem } from '@/components/filter/applied-filter-bar'
import { valueSummaryLabel } from '@/components/issue-explorer/advanced-filter'
import { dateFilterValueLabel } from '@/components/issue-explorer/issue-date-filter'
import { filterOperatorChoices, filterValues, type MyIssuesAppliedFilter, type MyIssuesFilterOperator } from './my-issues-filter-types'
import type { MyIssuesFilterOption } from './my-issues-surface'
import { OptionMark } from './my-issues-filter-menu'
import { agentSessionFilterValue } from '@/lib/agent-members'

export type ChipOption = MyIssuesFilterOption & { icon?: ReactNode }
export type NormalizedFilter = AppliedFilterItem<ChipOption, MyIssuesFilterOperator> & { field: MyIssuesAppliedFilter['field'] }

/** Flatten nested option menus (Dates › Due date › …) so stored values find their option. */
export function flattenFilterOptions(options: MyIssuesFilterOption[] = []): MyIssuesFilterOption[] {
  return options.flatMap(option => option.children?.length ? [option, ...flattenFilterOptions(option.children)] : [option])
}

/** Chip data shared by the filter bar and the advanced editor: Linear operators, value icons, "2 priorities". */
export function filterChipItem(filter: Pick<MyIssuesAppliedFilter, 'id' | 'field' | 'fieldLabel' | 'operator' | 'operatorLabel' | 'negativeOperatorLabel' | 'value' | 'valueLabel' | 'color' | 'values'>, options: MyIssuesFilterOption[] | undefined): NormalizedFilter {
  const known = new Map(flattenFilterOptions(options).map(option => [option.id, option]))
  const values = filterValues(filter as MyIssuesAppliedFilter).map(value => {
    // Legacy Agent Session values (Awaiting input) show as the Linear state they now match.
    const legacy = filter.field === 'agentSession' && !known.has(value.value) ? known.get(agentSessionFilterValue(value.value) ?? '') : undefined
    const option = known.get(value.value) ?? (legacy && { ...legacy, id: value.value })
    const base: MyIssuesFilterOption = option ?? { id: value.value, label: filter.field === 'dates' ? dateFilterValueLabel(value.value) ?? value.valueLabel : value.valueLabel, color: value.color }
    return { ...base, label: base.label || value.valueLabel, icon: <OptionMark field={filter.field} option={base}/> }
  })
  const ids = values.map(value => value.id)
  return { id: filter.id, field: filter.field, fieldLabel: filter.fieldLabel, operator: filter.operator, operatorLabel: filter.operatorLabel, negativeOperatorLabel: filter.negativeOperatorLabel, values, operatorChoices: filterOperatorChoices(filter.field, ids, filter), valueSummary: values.length > 1 ? valueSummaryLabel(filter.field, values) : undefined }
}

/** Values a chip's picker offers: leaf options, narrowed to the chip's own sub-field (Due date, Project status…). */
export function chipValueOptions(fieldLabel: string, options: MyIssuesFilterOption[] = []): MyIssuesFilterOption[] {
  // Number blocks (Customer count…) have no value list: the chip keeps its typed number.
  if (flattenFilterOptions(options).some(option => option.numberInput && option.label === fieldLabel)) return []
  const leaves = flattenFilterOptions(options).filter(option => !option.children?.length && !option.numberInput && !option.textConditionPrefix && option.id !== 'content-prompt')
  const own = leaves.filter(option => option.filterLabel === fieldLabel)
  return own.length ? own : leaves
}


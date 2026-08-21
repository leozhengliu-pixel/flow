import type { MyIssuesFilterKey, MyIssuesFilterOption } from './my-issues-surface'

export type MyIssuesFilterOperator = 'is' | 'isNot'
export interface MyIssuesFilterValue { value: string; valueLabel: string; color?: string }
export interface MyIssuesAppliedFilter {
  id: string
  field: MyIssuesFilterKey
  fieldLabel: string
  operator: MyIssuesFilterOperator
  value: string
  valueLabel: string
  color?: string
  values?: MyIssuesFilterValue[]
}

export function filterValues(filter: MyIssuesAppliedFilter): MyIssuesFilterValue[] {
  const persistedValues = Array.isArray(filter.values) ? filter.values as unknown[] : []
  const values = persistedValues.flatMap(value => {
    if (typeof value === 'string') return [{ value, valueLabel: value }]
    if (!value || typeof value !== 'object') return []
    const item = value as Partial<MyIssuesFilterValue>
    return typeof item.value === 'string' ? [{ value: item.value, valueLabel: item.valueLabel ?? item.value, color: item.color }] : []
  })
  return values.length ? values : [{ value: filter.value, valueLabel: filter.valueLabel, color: filter.color }]
}

export function toggleFilterOption(filters: MyIssuesAppliedFilter[], field: MyIssuesFilterKey, fieldLabel: string, option: MyIssuesFilterOption): MyIssuesAppliedFilter[] {
  filters = consolidateFilters(filters)
  const existing = filters.find(filter => filter.field === field && filter.operator === 'is')
  if (!existing) return [...filters, fromOption(field, fieldLabel, option)]
  const current = filterValues(existing)
  const values = current.some(value => value.value === option.id)
    ? current.filter(value => value.value !== option.id)
    : [...current, { value: option.id, valueLabel: option.label, color: option.color }]
  if (!values.length) return filters.filter(filter => filter.id !== existing.id)
  const first = values[0]
  return filters.map(filter => filter.id === existing.id ? { ...filter, ...first, values } : filter)
}

export function consolidateFilters(filters: MyIssuesAppliedFilter[]) {
  const result: MyIssuesAppliedFilter[] = []
  for (const filter of filters) {
    const existingIndex = result.findIndex(item => item.field === filter.field && item.operator === filter.operator)
    if (existingIndex < 0) { result.push(filter); continue }
    const existing = result[existingIndex]
    const values = [...filterValues(existing)]
    for (const value of filterValues(filter)) if (!values.some(current => current.value === value.value)) values.push(value)
    result[existingIndex] = { ...existing, ...values[0], values }
  }
  return result
}

export function replaceFilterValues(filter: MyIssuesAppliedFilter, options: MyIssuesFilterOption[]): MyIssuesAppliedFilter | undefined {
  if (!options.length) return
  const values = options.map(option => ({ value: option.id, valueLabel: option.label, color: option.color }))
  return { ...filter, ...values[0], values }
}

export function updateFilterOperator(filters: MyIssuesAppliedFilter[], id: string, operator: MyIssuesFilterOperator) {
  return filters.map(filter => filter.id === id ? { ...filter, operator } : filter)
}

export function updateFilterValues(filters: MyIssuesAppliedFilter[], id: string, options: MyIssuesFilterOption[]) {
  const target = filters.find(filter => filter.id === id)
  if (!target) return filters
  const replacement = replaceFilterValues(target, options)
  return replacement ? filters.map(filter => filter.id === id ? replacement : filter) : filters.filter(filter => filter.id !== id)
}

function fromOption(field: MyIssuesFilterKey, fieldLabel: string, option: MyIssuesFilterOption): MyIssuesAppliedFilter {
  const value = { value: option.id, valueLabel: option.label, color: option.color }
  return { id: `${field}-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, field, fieldLabel, operator: 'is', ...value, values: [value] }
}

import type { MyIssuesFilterKey, MyIssuesFilterOption } from './my-issues-surface'
import { combineModelFilters } from '@/components/filter/filter-block-helper'
import { filterToQueryNode, type IssueFilterQueryContext } from '@/components/issue-explorer/issue-filter-query'
import { CUSTOMER_NUMBER_COMPARISONS, customerNumberQueryValue, isCustomerNumberComparison, parseCustomerNumberValue, type CustomerNumberComparison } from '@/components/issue-explorer/customer-filter'

/**
 * Linear's operator sets: is / is not (several values read "is any of"), labels add "include all of" /
 * "exclude if all", dates compare "before" / "after". `isNot` and `excludesAll` negate.
 */
export type MyIssuesFilterOperator = 'is' | 'isNot' | 'includesAll' | 'excludesAll' | 'before' | 'after' | CustomerNumberComparison
export interface MyIssuesFilterValue { value: string; valueLabel: string; color?: string }

/** Advanced filter tree (Linear "Advanced filter"): top level → group → nested group, at most 3 levels. */
export type AdvancedFilterConjunction = 'and' | 'or'
export interface AdvancedFilterCondition { id: string; field: MyIssuesFilterKey; fieldLabel: string; operator: MyIssuesFilterOperator; values: MyIssuesFilterValue[] }
export interface AdvancedFilterGroup { id: string; conjunction: AdvancedFilterConjunction; items: AdvancedFilterNode[] }
export type AdvancedFilterNode = AdvancedFilterCondition | AdvancedFilterGroup
export const ADVANCED_FILTER_MAX_DEPTH = 3

export interface MyIssuesAppliedFilter {
  id: string
  field: MyIssuesFilterKey
  fieldLabel: string
  operator: MyIssuesFilterOperator
  value: string
  valueLabel: string
  color?: string
  operatorLabel?: string
  negativeOperatorLabel?: string
  values?: MyIssuesFilterValue[]
  /** Present on advanced-filter chips (`field: 'advanced'`); combined with the other chips by AND. */
  tree?: AdvancedFilterGroup
}

/** JSON-compatible query AST sent to the server-backed issue list endpoint. */
export interface IssueQueryAstNode {
  [key: string]: unknown
  and?: IssueQueryAstNode[]
  or?: IssueQueryAstNode[]
  field?: string
  operator?: string
  values?: string[]
}

const QUERY_FIELDS: Partial<Record<MyIssuesFilterKey, string>> = {
  assignee: 'assigneeId', creator: 'creatorId', labels: 'labelId', project: 'projectId',
  projectMilestone: 'projectMilestoneId',
  projectProperties: 'project', status: 'status', priority: 'priority', cycle: 'cycleId',
  subscribers: 'subscriberId', externalSource: 'externalSource', autoClosed: 'autoClosed',
  template: 'templateId', relations: 'relation', links: 'links', content: 'content',
  initiative: 'initiativeId', releases: 'releaseId', customers: 'customerId', dates: 'dateFilter',
}

/** Convert the existing filter-bar state into a composable AND expression (FilterBlockHelper). */
export function issueFiltersToQueryAst(filters: MyIssuesAppliedFilter[], context?: IssueFilterQueryContext): IssueQueryAstNode {
  // With workspace context every filter-menu field is translated to the server vocabulary.
  if (context) return simplifyQueryNode({ and: filters.map(filter => filterToQueryNode(filter, context)) }, true)
  const leaves = filters.map(filter => {
    // Number filters carry their comparison in the value the server parses (`customer-count:gte:3`).
    const comparison = isCustomerNumberComparison(filter.operator) ? filter.operator : undefined
    return {
      field: QUERY_FIELDS[filter.field] ?? filter.field,
      operator: comparison ? 'is' : filter.operator,
      values: filterValues(filter).map(item => comparison ? customerNumberQueryValue(item.value, comparison) : item.value),
    }
  })
  if (!leaves.length) return { and: [] }
  const combined = combineModelFilters('and', leaves)
  // Keep a stable `and` root for REST list callers that nest this node.
  return combined.and ? combined : { and: [combined] }
}

/**
 * Flatten same-kind nesting, unwrap single-child groups and double negation so nested advanced
 * filters stay inside the server's depth (8) and node (100) budgets. Keeps a stable `and` root.
 */
export function simplifyQueryNode(node: IssueQueryAstNode, keepAndRoot = false): IssueQueryAstNode {
  const simplify = (current: IssueQueryAstNode): IssueQueryAstNode => {
    if (current.not) {
      const inner = simplify(current.not as IssueQueryAstNode)
      return inner.not ? inner.not as IssueQueryAstNode : { not: inner }
    }
    for (const key of ['and', 'or'] as const) {
      const children = current[key]
      if (!children) continue
      const flat: IssueQueryAstNode[] = []
      for (const child of children.map(simplify)) {
        if (child[key] && Object.keys(child).length === 1) flat.push(...child[key]!)
        else flat.push(child)
      }
      if (flat.length === 1) return flat[0]
      return { [key]: flat }
    }
    return current
  }
  const result = simplify(node)
  if (!keepAndRoot) return result
  if (result.and && Object.keys(result).length === 1) return result
  return { and: Object.keys(result).length ? [result] : [] }
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
  const effectiveLabel = option.filterLabel ?? fieldLabel
  // Number filters hold one comparison (Linear allowMultiSelect: false): a new number replaces the chip.
  if (option.comparison) {
    const replacement = fromOption(field, effectiveLabel, option)
    const current = filters.find(filter => filter.field === field && filter.fieldLabel === effectiveLabel && isCustomerNumberComparison(filter.operator))
    return current ? filters.map(filter => filter.id === current.id ? { ...replacement, id: current.id } : filter) : [...filters, replacement]
  }
  // Date filters hold one comparison ("Due date before 1 week from now"): picking another preset replaces it.
  const singleValue = field === 'dates'
  const existing = filters.find(filter => filter.field === field && filter.fieldLabel === effectiveLabel && (singleValue || filter.operator === 'is'))
  if (!existing) return [...filters, fromOption(field, effectiveLabel, option)]
  if (singleValue) {
    if (filterValues(existing).some(value => value.value === option.id)) return filters.filter(filter => filter.id !== existing.id)
    const replacement = fromOption(field, effectiveLabel, option)
    return filters.map(filter => filter.id === existing.id ? { ...replacement, id: existing.id } : filter)
  }
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
    if (filter.field === 'advanced' || filter.field === 'dates' || isCustomerNumberComparison(filter.operator)) { result.push(filter); continue }
    const existingIndex = result.findIndex(item => item.field === filter.field && item.fieldLabel === filter.fieldLabel && item.operator === filter.operator)
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
  return { ...filter, ...values[0], values, operator: normalizeFilterOperator(filter.field, filter.operator, values.map(value => value.value)) }
}

export function updateFilterOperator(filters: MyIssuesAppliedFilter[], id: string, operator: MyIssuesFilterOperator) {
  return filters.map(filter => filter.id === id ? { ...filter, operator } : filter)
}

export interface FilterOperatorChoice { operator: MyIssuesFilterOperator; label: string }

const DATE_VALUE = /^(due|created|updated|started|completed|triaged|status):(?:[+-]\d+[dwmy]|\d{4}-\d{2}-\d{2})$/

/** Whether a dates value compares against a point in time (so it offers before / after). */
export function isComparableDateValue(value: string) { return DATE_VALUE.test(value) }

/** Default comparison for a dates value: future presets read "before", past presets "after" (Linear). */
export function defaultDateOperator(value: string): 'before' | 'after' {
  const match = value.match(/^(\w+):([+-])?/)
  if (!match) return 'after'
  if (match[1] === 'status') return 'before'
  if (match[2]) return match[2] === '+' ? 'before' : 'after'
  return match[1] === 'due' ? 'before' : 'after'
}

/**
 * The operator menu for a chip or condition, per Linear: one value "is"/"is not", several "is any of";
 * labels "include"/"do not include", several values add "include all of" and "exclude if all";
 * point-in-time dates "before"/"after".
 */
export function filterOperatorChoices(field: MyIssuesFilterKey, values: string[], labels?: { operatorLabel?: string; negativeOperatorLabel?: string }): FilterOperatorChoice[] {
  if (labels?.operatorLabel) return [{ operator: 'is', label: labels.operatorLabel }, { operator: 'isNot', label: labels.negativeOperatorLabel ?? 'is not' }]
  if (field === 'customers' && values.length > 0 && values.every(value => parseCustomerNumberValue(value))) return CUSTOMER_NUMBER_COMPARISONS.map(choice => ({ operator: choice.value, label: choice.label }))
  if (field === 'dates' && values.length > 0 && values.every(isComparableDateValue)) {
    return values.every(value => value.startsWith('status:'))
      ? [{ operator: 'before', label: 'more than' }, { operator: 'after', label: 'less than' }]
      : [{ operator: 'before', label: 'before' }, { operator: 'after', label: 'after' }]
  }
  if (field === 'labels') return values.length > 1
    ? [{ operator: 'includesAll', label: 'include all of' }, { operator: 'is', label: 'include any of' }, { operator: 'isNot', label: 'exclude if any of' }, { operator: 'excludesAll', label: 'exclude if all' }]
    : [{ operator: 'is', label: 'include' }, { operator: 'isNot', label: 'do not include' }]
  return [{ operator: 'is', label: values.length > 1 ? 'is any of' : 'is' }, { operator: 'isNot', label: 'is not' }]
}

/** Keep the operator valid for the current values (for example "include all of" back to "include" at one value). */
export function normalizeFilterOperator(field: MyIssuesFilterKey, operator: MyIssuesFilterOperator, values: string[]): MyIssuesFilterOperator {
  const choices = filterOperatorChoices(field, values)
  if (choices.some(choice => choice.operator === operator)) return operator
  if (operator === 'excludesAll' || operator === 'isNot') return choices.some(choice => choice.operator === 'isNot') ? 'isNot' : choices[0].operator
  if ((operator === 'is' || operator === 'includesAll') && choices[0].operator === 'before') return values[0] ? defaultDateOperator(values[0]) : 'before'
  return choices.some(choice => choice.operator === 'is') ? 'is' : choices[0].operator
}

export function filterOperatorLabel(filter: Pick<MyIssuesAppliedFilter, 'field' | 'operator' | 'operatorLabel' | 'negativeOperatorLabel'>, values: string[]) {
  const choices = filterOperatorChoices(filter.field, values, filter)
  const operator = filter.field === 'dates' && filter.operator === 'is' && values[0] && isComparableDateValue(values[0]) ? defaultDateOperator(values[0]) : filter.operator
  return choices.find(choice => choice.operator === operator)?.label ?? choices[0].label
}

export function updateFilterValues(filters: MyIssuesAppliedFilter[], id: string, options: MyIssuesFilterOption[]) {
  const target = filters.find(filter => filter.id === id)
  if (!target) return filters
  const replacement = replaceFilterValues(target, options)
  return replacement ? filters.map(filter => filter.id === id ? replacement : filter) : filters.filter(filter => filter.id !== id)
}

function fromOption(field: MyIssuesFilterKey, fieldLabel: string, option: MyIssuesFilterOption): MyIssuesAppliedFilter {
  const value = { value: option.id, valueLabel: option.label, color: option.color }
  const operator: MyIssuesFilterOperator = option.comparison ?? (field === 'dates' && isComparableDateValue(option.id) ? defaultDateOperator(option.id) : 'is')
  return { id: filterId(field), field, fieldLabel, operator, operatorLabel: option.operatorLabel, negativeOperatorLabel: option.negativeOperatorLabel, ...value, values: [value] }
}

export function filterId(prefix: string) { return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 7)}` }

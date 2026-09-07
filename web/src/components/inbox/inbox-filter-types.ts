import type { ReactNode } from 'react'

/** The predicates exposed by Flow's Inbox filter picker. */
export type InboxFilterProperty =
  | 'notificationType'
  | 'from'
  | 'project'
  | 'initiative'
  | 'issuePriority'
  | 'issueStatusType'
  | 'reviewStatus'

export type InboxFilterOperator = 'is' | 'isNot'

export interface InboxFilterOption {
  id: string
  label: string
  color?: string
  avatarUrl?: string
  /** Optional live-result total supplied by the Inbox projection. */
  count?: number
  keywords?: string
  disabled?: boolean
  icon?: ReactNode
  /** Entity names are never translated by the UI compatibility translator. */
  i18nIgnore?: boolean
}

export const INBOX_REVIEW_STATUS_OPTIONS = [
  { id: 'draft', label: 'Draft', color: 'var(--status-neutral)' },
  { id: 'open', label: 'Open', color: 'var(--data-vis-4)' },
  { id: 'inReview', label: 'In review', color: 'var(--data-vis-3)' },
  { id: 'approved', label: 'Approved', color: 'var(--semantic-success)' },
  { id: 'merged', label: 'Merged', color: 'var(--accent-primary)' },
  { id: 'closed', label: 'Closed', color: 'var(--status-neutral)' },
] as const

export interface InboxFilterValue {
  value: string
  valueLabel: string
  color?: string
  avatarUrl?: string
}

/** A predicate block with one or more values. Multiple blocks may use the same property. */
export interface InboxFilterCondition {
  id: string
  property: InboxFilterProperty
  operator: InboxFilterOperator
  values: InboxFilterValue[]
}

export type InboxFilterOptions = Partial<Record<InboxFilterProperty, InboxFilterOption[]>>

/** Remove empty conditions and duplicate values without collapsing independent predicates. */
export function normalizeInboxFilters(filters: InboxFilterCondition[]): InboxFilterCondition[] {
  return filters.flatMap(filter => {
    const values = dedupeValues(filter.values)
    if (!values.length) return []
    return [{
      ...filter,
      operator: filter.operator === 'isNot' ? 'isNot' as const : 'is' as const,
      values,
    }]
  })
}

/** Add a new predicate block, even when another block already uses the property. */
export function appendInboxFilterValue(filters: InboxFilterCondition[], property: InboxFilterProperty, option: InboxFilterOption): InboxFilterCondition[] {
  return [...normalizeInboxFilters(filters), {
    id: createFilterId(property),
    property,
    operator: 'is',
    values: [toInboxFilterValue(option)],
  }]
}

/** Toggle one option while preserving the filter's existing operator. */
export function toggleInboxFilterValue(
  filters: InboxFilterCondition[],
  property: InboxFilterProperty,
  option: InboxFilterOption,
): InboxFilterCondition[] {
  const normalized = normalizeInboxFilters(filters)
  const targetIndex = normalized.findIndex(filter => filter.property === property)
  if (targetIndex < 0) {
    return [...normalized, {
      id: createFilterId(property),
      property,
      operator: 'is',
      values: [toInboxFilterValue(option)],
    }]
  }

  const target = normalized[targetIndex]
  const selected = target.values.some(value => value.value === option.id)
  const values = selected
    ? target.values.filter(value => value.value !== option.id)
    : [...target.values, toInboxFilterValue(option)]

  if (!values.length) return normalized.filter((_, index) => index !== targetIndex)
  return normalized.map((filter, index) => index === targetIndex ? { ...filter, values } : filter)
}

/** Toggle a value in one existing predicate block without touching sibling blocks. */
export function toggleInboxFilterConditionValue(filters: InboxFilterCondition[], conditionId: string, option: InboxFilterOption): InboxFilterCondition[] {
  const normalized = normalizeInboxFilters(filters)
  const target = normalized.find(filter => filter.id === conditionId)
  if (!target) return normalized
  const selected = target.values.some(value => value.value === option.id)
  const values = selected
    ? target.values.filter(value => value.value !== option.id)
    : [...target.values, toInboxFilterValue(option)]
  if (!values.length) return normalized.filter(filter => filter.id !== conditionId)
  return normalized.map(filter => filter.id === conditionId ? { ...filter, values } : filter)
}

export function updateInboxFilterOperator(
  filters: InboxFilterCondition[],
  id: string,
  operator: InboxFilterOperator,
): InboxFilterCondition[] {
  return normalizeInboxFilters(filters).map(filter => filter.id === id ? { ...filter, operator } : filter)
}

export function removeInboxFilter(filters: InboxFilterCondition[], id: string): InboxFilterCondition[] {
  return normalizeInboxFilters(filters).filter(filter => filter.id !== id)
}

function toInboxFilterValue(option: InboxFilterOption): InboxFilterValue {
  return {
    value: option.id,
    valueLabel: option.label,
    color: option.color,
    avatarUrl: option.avatarUrl,
  }
}

function dedupeValues(values: InboxFilterValue[]): InboxFilterValue[] {
  const seen = new Set<string>()
  return values.filter(value => {
    if (!value.value || seen.has(value.value)) return false
    seen.add(value.value)
    return true
  })
}

function createFilterId(property: InboxFilterProperty) {
  return `${property}-${globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`}`
}

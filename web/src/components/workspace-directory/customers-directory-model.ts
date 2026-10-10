import { useEffect, useState } from 'react'
import { findCustomerStatus, findCustomerTier } from '@/components/customer/customer-status-lookup'
import type { Customer, CustomerRequest, CustomerStatus, CustomerTier } from '@/types/flow'

/** Linear's customers list: ordering keys (Display options › Ordering, in its order). */
export const customerOrderings = ['requests', 'created', 'name', 'revenue', 'size', 'status', 'tier'] as const
export type CustomerOrdering = typeof customerOrderings[number]
/** Display properties chips, in Linear's order. */
export const customerColumns = ['requests', 'revenue', 'size', 'owner', 'status', 'tier', 'domains', 'source'] as const
export type CustomerColumn = typeof customerColumns[number]
/** Column order in the rows and the column header (Linear's CustomerListColumns). */
export const customerRowColumns: readonly CustomerColumn[] = ['requests', 'revenue', 'size', 'status', 'tier', 'owner', 'domains', 'source']
export type SortDirection = 'asc' | 'desc'

export interface CustomerDirectoryPreferences {
  ordering: CustomerOrdering
  /** Unset means the ordering's default direction (Linear's viewOrderingDirection). */
  direction?: SortDirection
  columns: CustomerColumn[]
}

export const defaultCustomerPreferences: CustomerDirectoryPreferences = {
  ordering: 'created',
  columns: ['requests', 'revenue', 'size', 'owner', 'status', 'tier'],
}

/** Linear: names, statuses and tiers sort ascending by default; dates and numbers newest/largest first. */
export function defaultCustomerDirection(ordering: CustomerOrdering): SortDirection {
  return ordering === 'name' || ordering === 'status' || ordering === 'tier' ? 'asc' : 'desc'
}

export function customerDirection(preferences: CustomerDirectoryPreferences): SortDirection {
  return preferences.direction ?? defaultCustomerDirection(preferences.ordering)
}

export function customerPreferencesKey(workspaceId: string, userId: string) {
  return `flow.customers.preferences:${workspaceId}:${userId}`
}

function readPreferences(key: string): CustomerDirectoryPreferences {
  try {
    const stored = JSON.parse(localStorage.getItem(key) ?? 'null') as Partial<CustomerDirectoryPreferences> | null
    if (!stored) return defaultCustomerPreferences
    const ordering = customerOrderings.includes(stored.ordering as CustomerOrdering) ? stored.ordering as CustomerOrdering : defaultCustomerPreferences.ordering
    const direction = stored.direction === 'asc' || stored.direction === 'desc' ? stored.direction : undefined
    const columns = Array.isArray(stored.columns) ? customerColumns.filter(column => stored.columns!.includes(column)) : defaultCustomerPreferences.columns
    return { ordering, direction, columns }
  } catch {
    return defaultCustomerPreferences
  }
}

/** Per-viewer list preferences (ordering, direction, visible properties), kept in local storage. */
export function useCustomerDirectoryPreferences(workspaceId: string, userId: string) {
  const key = customerPreferencesKey(workspaceId, userId)
  const [preferences, setPreferences] = useState(() => readPreferences(key))
  useEffect(() => {
    try { localStorage.setItem(key, JSON.stringify(preferences)) } catch { /* Storage can be unavailable in private sessions. */ }
  }, [key, preferences])
  return [preferences, setPreferences] as const
}

/** Linear's empty-state "Documentation" link (Flow's customer requests guide). */
export const CUSTOMER_DOCS_URL = 'https://flow.app/docs/customer-requests'

export const NO_OWNER = '__no_owner__'
export const CURRENT_USER = '__current_user__'

export type ListOperator = 'is' | 'isNot'
export type NumberOperator = 'gte' | 'lte' | 'eq' | 'neq'
export const numberOperators: Array<{ id: NumberOperator; label: string }> = [
  { id: 'gte', label: 'greater than or equals' },
  { id: 'lte', label: 'less than or equals' },
  { id: 'eq', label: 'equals' },
  { id: 'neq', label: 'not equals' },
]
export type ListFilter = { operator: ListOperator; values: string[] }
export type NumberFilter = { operator: NumberOperator; value: number }
export interface CustomerFilters {
  owner?: ListFilter
  status?: ListFilter
  tier?: ListFilter
  /** Annual revenue (monthly display values are converted when the filter is made). */
  revenue?: NumberFilter
  size?: NumberFilter
  advanced: boolean
  conjunction: 'and' | 'or'
}
export type CustomerFilterField = 'owner' | 'status' | 'tier' | 'revenue' | 'size'
export const customerFilterFields: CustomerFilterField[] = ['owner', 'status', 'tier', 'revenue', 'size']
export const emptyCustomerFilters = (): CustomerFilters => ({ advanced: false, conjunction: 'and' })

export function activeCustomerFilterFields(filters: CustomerFilters) {
  return customerFilterFields.filter(field => {
    const value = filters[field]
    return value !== undefined && (!('values' in value) || value.values.length > 0)
  })
}

export type CustomerFilterContext = { viewerId: string; statuses: readonly CustomerStatus[]; tiers: readonly CustomerTier[] }

function matchesList(filter: ListFilter, values: Array<string | undefined>) {
  const hit = filter.values.some(value => values.includes(value))
  return filter.operator === 'is' ? hit : !hit
}

function matchesNumber(filter: NumberFilter, value: number | undefined) {
  if (value === undefined || value === null) return false
  if (filter.operator === 'gte') return value >= filter.value
  if (filter.operator === 'lte') return value <= filter.value
  if (filter.operator === 'eq') return value === filter.value
  return value !== filter.value
}

export function customerStatusId(customer: Customer, statuses: readonly CustomerStatus[]) {
  return findCustomerStatus(statuses, customer.status)?.id ?? customer.status
}

export function customerTierId(customer: Customer, tiers: readonly CustomerTier[]) {
  return customer.tier ? findCustomerTier(tiers, customer.tier)?.id ?? customer.tier : undefined
}

function matchesField(customer: Customer, field: CustomerFilterField, filters: CustomerFilters, context: CustomerFilterContext) {
  if (field === 'owner') {
    const owner = customer.ownerId || undefined
    return matchesList(filters.owner!, [owner ?? NO_OWNER, owner && owner === context.viewerId ? CURRENT_USER : undefined])
  }
  if (field === 'status') return matchesList(filters.status!, [customerStatusId(customer, context.statuses)])
  if (field === 'tier') return matchesList(filters.tier!, [customerTierId(customer, context.tiers)])
  if (field === 'revenue') return matchesNumber(filters.revenue!, customer.annualRevenue || undefined)
  return matchesNumber(filters.size!, customer.size || undefined)
}

export function matchesCustomerFilters(customer: Customer, filters: CustomerFilters, context: CustomerFilterContext) {
  const fields = activeCustomerFilterFields(filters)
  if (!fields.length) return true
  return filters.advanced && filters.conjunction === 'or'
    ? fields.some(field => matchesField(customer, field, filters, context))
    : fields.every(field => matchesField(customer, field, filters, context))
}

function fold(value: string) {
  return value.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLocaleLowerCase()
}

/** Linear's "Find by name or domain…": name contains (ignoring case and accents) or any domain contains. */
export function matchesCustomerSearch(customer: Customer, query: string) {
  const needle = query.trim()
  if (!needle) return true
  return fold(customer.name).includes(fold(needle)) || customer.domains.some(domain => domain.toLocaleLowerCase().includes(needle.toLocaleLowerCase()))
}

/** Active (not archived) requests per customer — Linear's approximateNeedCount. */
export function customerRequestCounts(requests: readonly Pick<CustomerRequest, 'customerId' | 'archivedAt'>[]) {
  const counts = new Map<string, number>()
  for (const request of requests) if (!request.archivedAt) counts.set(request.customerId, (counts.get(request.customerId) ?? 0) + 1)
  return counts
}

export function sortCustomers(customers: readonly Customer[], preferences: CustomerDirectoryPreferences, context: { counts: Map<string, number>; statuses: readonly CustomerStatus[]; tiers: readonly CustomerTier[] }) {
  const direction = customerDirection(preferences) === 'asc' ? 1 : -1
  const key = (customer: Customer): number | string | undefined => {
    switch (preferences.ordering) {
      case 'name': return customer.name.toLocaleLowerCase()
      case 'created': return new Date(customer.createdAt).getTime()
      case 'requests': return context.counts.get(customer.id) ?? 0
      case 'revenue': return customer.annualRevenue || undefined
      case 'size': return customer.size || undefined
      case 'status': return findCustomerStatus(context.statuses, customer.status)?.position
      case 'tier': return customer.tier ? findCustomerTier(context.tiers, customer.tier)?.position : undefined
    }
  }
  return [...customers].sort((left, right) => {
    const a = key(left), b = key(right)
    // Customers without a value go last whichever way the list is ordered.
    if (a === undefined || b === undefined) {
      if (a !== b) return a === undefined ? 1 : -1
    } else if (a !== b) return (typeof a === 'string' && typeof b === 'string' ? a.localeCompare(b) : (a as number) - (b as number)) * direction
    return left.name.localeCompare(right.name)
  })
}

/** Linear's number filter input: digits with optional separators, or a K/M suffix ("10k", "1.5M"). */
export function parseFilterNumber(value: string) {
  const text = value.trim()
  if (!text) return undefined
  const suffixed = text.match(/^(\d+(?:\.\d+)?)\s*(k|m)$/i)
  if (suffixed) return Math.round(parseFloat(suffixed[1]) * (suffixed[2].toLowerCase() === 'k' ? 1e3 : 1e6))
  const parsed = parseInt(text.replace(/[.,$\s]/g, ''), 10)
  return Number.isNaN(parsed) ? undefined : parsed
}

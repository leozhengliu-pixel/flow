import { describe, expect, it } from 'vitest'
import type { Customer, CustomerStatus, CustomerTier } from '@/types/flow'
import {
  CURRENT_USER, customerDirection, customerRequestCounts, defaultCustomerPreferences, emptyCustomerFilters, matchesCustomerFilters,
  matchesCustomerSearch, NO_OWNER, parseFilterNumber, sortCustomers, type CustomerFilters,
} from './customers-directory-model'

const statuses = [
  { id: 'st-active', name: 'Active', color: '#4cb782', position: 0 },
  { id: 'st-prospect', name: 'Prospect', color: '#5e6ad2', position: 1 },
] as CustomerStatus[]
const tiers = [{ id: 'tier-ent', name: 'Enterprise', color: '#000', position: 0 }] as CustomerTier[]
const customer = (overrides: Partial<Customer>): Customer => ({ id: 'c', name: 'Customer', status: 'active', domains: [], createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-01-01T00:00:00.000Z', ...overrides })
const acme = customer({ id: 'acme', name: 'Acme', ownerId: 'viewer', status: 'active', tier: 'tier-ent', annualRevenue: 120000, size: 50, domains: ['acme.com'], createdAt: '2026-01-02T00:00:00.000Z' })
const globex = customer({ id: 'globex', name: 'Globex', status: 'st-prospect', annualRevenue: 5000, createdAt: '2026-03-01T00:00:00.000Z' })
const ecole = customer({ id: 'ecole', name: 'École Café', domains: ['ecole.fr'], createdAt: '2026-02-01T00:00:00.000Z' })
const context = { viewerId: 'viewer', statuses, tiers }
const withFilters = (filters: Partial<CustomerFilters>) => ({ ...emptyCustomerFilters(), ...filters })

describe('customers list model (Linear CustomersPage)', () => {
  it('finds by name (ignoring accents) or domain', () => {
    expect(matchesCustomerSearch(ecole, 'ecole caf')).toBe(true)
    expect(matchesCustomerSearch(acme, 'ACME.c')).toBe(true)
    expect(matchesCustomerSearch(globex, 'acme')).toBe(false)
  })

  it('filters owner by No owner / Current user, status and tier by id or legacy name', () => {
    expect(matchesCustomerFilters(acme, withFilters({ owner: { operator: 'is', values: [CURRENT_USER] } }), context)).toBe(true)
    expect(matchesCustomerFilters(globex, withFilters({ owner: { operator: 'is', values: [NO_OWNER] } }), context)).toBe(true)
    expect(matchesCustomerFilters(acme, withFilters({ owner: { operator: 'isNot', values: [CURRENT_USER] } }), context)).toBe(false)
    expect(matchesCustomerFilters(acme, withFilters({ status: { operator: 'is', values: ['st-active'] } }), context)).toBe(true)
    expect(matchesCustomerFilters(globex, withFilters({ status: { operator: 'isNot', values: ['st-active'] } }), context)).toBe(true)
    expect(matchesCustomerFilters(acme, withFilters({ tier: { operator: 'is', values: ['tier-ent'] } }), context)).toBe(true)
    expect(matchesCustomerFilters(globex, withFilters({ tier: { operator: 'is', values: ['tier-ent'] } }), context)).toBe(false)
  })

  it('compares revenue and size with Linear\'s four operators; customers without a value never match', () => {
    expect(matchesCustomerFilters(acme, withFilters({ revenue: { operator: 'gte', value: 100000 } }), context)).toBe(true)
    expect(matchesCustomerFilters(globex, withFilters({ revenue: { operator: 'lte', value: 5000 } }), context)).toBe(true)
    expect(matchesCustomerFilters(globex, withFilters({ revenue: { operator: 'neq', value: 5000 } }), context)).toBe(false)
    expect(matchesCustomerFilters(ecole, withFilters({ size: { operator: 'neq', value: 1 } }), context)).toBe(false)
    expect(matchesCustomerFilters(acme, withFilters({ size: { operator: 'eq', value: 50 } }), context)).toBe(true)
  })

  it('matches any filter with the advanced "any filter" conjunction', () => {
    const filters = withFilters({ advanced: true, conjunction: 'or', status: { operator: 'is', values: ['st-prospect'] }, size: { operator: 'eq', value: 50 } })
    expect([acme, globex, ecole].filter(item => matchesCustomerFilters(item, filters, context)).map(item => item.id)).toEqual(['acme', 'globex'])
  })

  it('orders newest first by default, names A–Z, and keeps missing values last either way', () => {
    const counts = customerRequestCounts([{ customerId: 'globex' }, { customerId: 'globex' }, { customerId: 'acme' }, { customerId: 'acme', archivedAt: '2026-01-01' }])
    const sort = (ordering: typeof defaultCustomerPreferences.ordering, direction?: 'asc' | 'desc') => sortCustomers([acme, globex, ecole], { ...defaultCustomerPreferences, ordering, direction }, { counts, statuses, tiers }).map(item => item.id)
    expect(customerDirection(defaultCustomerPreferences)).toBe('desc')
    expect(sort('created')).toEqual(['globex', 'ecole', 'acme'])
    expect(sort('name')).toEqual(['acme', 'ecole', 'globex'])
    expect(sort('requests')).toEqual(['globex', 'acme', 'ecole'])
    expect(sort('revenue')).toEqual(['acme', 'globex', 'ecole'])
    expect(sort('revenue', 'asc')).toEqual(['globex', 'acme', 'ecole'])
    expect(sort('status')).toEqual(['acme', 'ecole', 'globex'])
    expect(sort('tier')).toEqual(['acme', 'ecole', 'globex'])
  })

  it('parses filter numbers like Linear (separators, K and M suffixes)', () => {
    expect(parseFilterNumber('1,000')).toBe(1000)
    expect(parseFilterNumber('$25.000')).toBe(25000)
    expect(parseFilterNumber('10k')).toBe(10000)
    expect(parseFilterNumber('1.5M')).toBe(1500000)
    expect(parseFilterNumber('abc')).toBeUndefined()
  })
})

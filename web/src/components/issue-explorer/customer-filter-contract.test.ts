import { describe, expect, it } from 'vitest'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import type { CustomerRequest } from '@/types/flow'
import { filterOperatorChoices, issueFiltersToQueryAst, toggleFilterOption, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import { applyExplorerFilters, explorerPropertyOptions } from './issue-explorer-model'

describe('customer filter contract', () => {
  const issues = ['one', 'two', 'unknown', 'archived'].map(id => makeIssue({ id }))
  const request = (id: string, issueId: string, customerId: string, archivedAt?: string): CustomerRequest => ({ id, issueId, customerId, archivedAt, body: 'Request', source: 'manual', creator: viewer, attachments: [], createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z' })
  const data = makeBootstrap({ issues, customers: [
    { id: 'c1', name: 'First', ownerId: viewer.id, status: 'active', tier: 'gold', annualRevenue: 1000, size: 12, domains: [], createdAt: '', updatedAt: '' },
    { id: 'c2', name: 'Second', status: 'trial', tier: 'silver', annualRevenue: 0, size: 0, domains: [], createdAt: '', updatedAt: '' },
  ], customerRequests: [request('r1', 'one', 'c1'), { ...request('r2', 'one', 'c1'), priority: 1 }, request('r3', 'one', 'c2'), request('r4', 'two', 'c2'), request('r5', 'unknown', 'missing'), request('r6', 'unknown', 'missing2'), request('r7', 'archived', 'c1', '2026-09-02T00:00:00Z')] })

  it.each<[string, string[]]>([
    ['customer:c1', ['one']], ['customer:c2', ['one', 'two']], ['customer:', ['unknown']],
    ['customer-count:0', ['archived']], ['customer-count:1', ['two', 'unknown']], ['customer-count:2+', ['one']],
    ['customer-owner:', ['two']], [`customer-owner:${viewer.id}`, ['one']],
    ['customer-status:active', ['one']], ['customer-tier:silver', ['one', 'two']],
    ['customer-revenue:', ['two']], ['customer-revenue:any', ['one']], ['customer-size:', ['two']], ['customer-size:any', ['one']],
  ])('matches %s before and after conversion to the query AST', (value, expected) => {
    const filter: MyIssuesAppliedFilter = { id: 'customer-filter', field: 'customers', fieldLabel: 'Customers', operator: 'is', value, valueLabel: value }
    expect(applyExplorerFilters(issues, [filter], data).map(issue => issue.id)).toEqual(expected)
    expect(issueFiltersToQueryAst([filter])).toEqual({ and: [{ field: 'customerId', operator: 'is', values: [value] }] })
    expect(applyExplorerFilters(issues, [{ ...filter, operator: 'isNot' }], data).map(issue => issue.id)).toEqual(issues.map(issue => issue.id).filter(id => !expected.includes(id)))
  })

  it('offers Linear\'s Customers sub-menu without match counts', () => {
    const options = explorerPropertyOptions(data).customers
    expect(options.map(option => option.label)).toEqual(['Customer name', 'Customer count', 'Important customer count', 'Customer owner', 'Customer status', 'Customer tier', 'Customer revenue', 'Customer size'])
    expect(options.filter(option => option.numberInput).map(option => option.numberInput?.placeholder)).toEqual(['Enter customer count…', 'Enter important customer count…', 'Enter customer revenue…', 'Enter customer size…'])
    expect(options.flatMap(option => option.children ?? []).every(option => option.count === undefined)).toBe(true)
    expect(options[0].children?.map(option => option.label)).toEqual(['Unknown customer', 'First', 'Second'])
  })

  it.each<[MyIssuesAppliedFilter['operator'], string, string[]]>([
    ['gte', 'customer-count:2', ['one']], ['eq', 'customer-count:1', ['two', 'unknown']], ['lte', 'customer-count:0', ['archived']],
    ['gte', 'customer-important-count:1', ['one']], ['neq', 'customer-important-count:1', ['two', 'unknown', 'archived']],
    ['gte', 'customer-revenue:500', ['one']], ['lte', 'customer-revenue:0', ['one', 'two']],
    ['gte', 'customer-size:10', ['one']], ['eq', 'customer-size:0', ['one', 'two']],
  ])('compares %s %s like the server', (operator, value, expected) => {
    const filter: MyIssuesAppliedFilter = { id: 'customer-number', field: 'customers', fieldLabel: 'Customer count', operator, value, valueLabel: value }
    expect(applyExplorerFilters(issues, [filter], data).map(issue => issue.id)).toEqual(expected)
    expect(issueFiltersToQueryAst([filter])).toEqual({ and: [{ field: 'customerId', operator: 'is', values: [value.replace(/:(\d+)$/, `:${operator}:$1`)] }] })
    expect(filterOperatorChoices('customers', [value]).map(choice => choice.label)).toEqual(['greater than or equals', 'less than or equals', 'equals', 'not equals'])
  })

  it('replaces a number chip instead of adding values', () => {
    const first = toggleFilterOption([], 'customers', 'Customers', { id: 'customer-count:2', label: '2', filterLabel: 'Customer count', comparison: 'gte' })
    const next = toggleFilterOption(first, 'customers', 'Customers', { id: 'customer-count:5', label: '5', filterLabel: 'Customer count', comparison: 'lte' })
    expect(next).toHaveLength(1)
    expect(next[0]).toMatchObject({ operator: 'lte', value: 'customer-count:5', fieldLabel: 'Customer count' })
  })
})

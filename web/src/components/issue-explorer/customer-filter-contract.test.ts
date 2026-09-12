import { describe, expect, it } from 'vitest'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import type { CustomerRequest } from '@/types/flow'
import { issueFiltersToQueryAst, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import { applyExplorerFilters, explorerPropertyOptions } from './issue-explorer-model'

describe('customer filter contract', () => {
  const issues = ['one', 'two', 'unknown', 'archived'].map(id => makeIssue({ id }))
  const request = (id: string, issueId: string, customerId: string, archivedAt?: string): CustomerRequest => ({ id, issueId, customerId, archivedAt, body: 'Request', source: 'manual', creator: viewer, attachments: [], createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z' })
  const data = makeBootstrap({ issues, customers: [
    { id: 'c1', name: 'First', ownerId: viewer.id, status: 'active', tier: 'gold', annualRevenue: 1000, size: 12, domains: [], createdAt: '', updatedAt: '' },
    { id: 'c2', name: 'Second', status: 'trial', tier: 'silver', annualRevenue: 0, size: 0, domains: [], createdAt: '', updatedAt: '' },
  ], customerRequests: [request('r1', 'one', 'c1'), request('r2', 'one', 'c1'), request('r3', 'one', 'c2'), request('r4', 'two', 'c2'), request('r5', 'unknown', 'missing'), request('r6', 'unknown', 'missing2'), request('r7', 'archived', 'c1', '2026-09-02T00:00:00Z')] })

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

  it('counts distinct customers and excludes archived requests in menu options', () => {
    const counts = explorerPropertyOptions(data).customers.find(option => option.id === 'customer-count')?.children
    expect(counts?.map(option => [option.id, option.count])).toEqual([['customer-count:0', 1], ['customer-count:1', 2], ['customer-count:2+', 1]])
  })
})

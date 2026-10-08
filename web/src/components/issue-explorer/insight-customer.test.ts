import { describe, expect, it } from 'vitest'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import type { CustomerRequest } from '@/types/flow'
import { acrossAllLabel, DEFAULT_INSIGHTS, emptyDimensionLabel, parseInsightsConfig } from './insight-config'
import { buildInsightData, UNKNOWN_CUSTOMER_ID } from './insight-data'
import { dimensionOptions } from './insight-options'
import { issueToExplorerRow } from './issue-explorer-model'

const request = (id: string, issueId: string, customerId: string): CustomerRequest => ({ id, issueId, customerId, body: 'Request', source: 'manual', creator: viewer, attachments: [], createdAt: '', updatedAt: '' })
const issues = ['one', 'two', 'three'].map(id => makeIssue({ id }))
const data = makeBootstrap({ issues, customers: [
  { id: 'b', name: 'Beta', status: 'active', domains: [], createdAt: '', updatedAt: '' },
  { id: 'a', name: 'Acme', status: 'active', domains: [], createdAt: '', updatedAt: '' },
], customerRequests: [request('r1', 'one', 'a'), request('r2', 'one', 'b'), request('r3', 'two', 'a'), request('r4', 'two', 'deleted')] })
const rows = issues.map(issue => issueToExplorerRow(issue, 'ws', issues, data))

describe('Customer insight dimension', () => {
  it('is offered after Label (and label groups), with Linear\'s empty and "across all" labels', () => {
    const ids = dimensionOptions(data, true).map(option => option.id)
    expect(ids.indexOf('customer')).toBeGreaterThan(ids.indexOf('label'))
    expect(ids.indexOf('customer')).toBe(ids.indexOf('template') - 1)
    expect(emptyDimensionLabel('customer')).toBe('No customer')
    expect(acrossAllLabel('customer')).toBe('Across all customers')
    expect(parseInsightsConfig({ slice: 'customer', hideUnknownCustomer: true })).toMatchObject({ slice: 'customer', hideUnknownCustomer: true })
  })

  it('counts an issue once per requesting customer, alphabetically, and can hide Unknown customer', () => {
    const config = { ...DEFAULT_INSIGHTS, slice: 'customer' as const }
    expect(buildInsightData(rows, config, data).rows.map(row => [row.label, row.total])).toEqual([['Acme', 2], ['Beta', 1], ['Unknown customer', 1], ['No customer', 1]])
    const hidden = buildInsightData(rows, { ...config, hideUnknownCustomer: true }, data).rows
    expect(hidden.some(row => row.id === UNKNOWN_CUSTOMER_ID)).toBe(false)
  })
})

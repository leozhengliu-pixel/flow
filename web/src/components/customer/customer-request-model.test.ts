import { describe, expect, it } from 'vitest'
import type { Customer, CustomerRequest } from '@/types/flow'
import { applyRequestOverlay, compactRelativeTime, customersSummary, groupRequestsByCustomer, looksLikeUrl, normalizeSourceUrl, orderRequestGroups, requestSubtitle } from './customer-request-model'
import { customerPickOptions } from './customer-request-model'

const customer = (id: string, name: string, annualRevenue?: number) => ({ id, name, annualRevenue, status: 'active', domains: [], createdAt: '', updatedAt: '' }) as Customer
const request = (id: string, customerId: string, createdAt: string, extra: Partial<CustomerRequest> = {}) => ({ id, customerId, body: `Body ${id}`, source: 'manual', attachments: [], createdAt, updatedAt: createdAt, ...extra }) as CustomerRequest

describe('customer request model', () => {
  const customers = new Map([customer('a', 'Acme', 10), customer('g', 'Globex', 500)].map(item => [item.id, item]))

  it('groups requests into one row per customer, newest first, and keeps customer-less requests apart', () => {
    const groups = groupRequestsByCustomer([
      request('1', 'a', '2026-10-01T00:00:00Z'),
      request('2', 'a', '2026-10-03T00:00:00Z'),
      request('3', '', '2026-10-02T00:00:00Z'),
      request('4', '', '2026-10-04T00:00:00Z'),
    ], customers)
    expect(groups.map(group => [group.primary.id, group.additional.map(item => item.id)])).toEqual([['4', []], ['2', ['1']], ['3', []]])
    expect(groups[1].customer?.name).toBe('Acme')
  })

  it('orders by created date, customer name or revenue, optionally important first', () => {
    const groups = groupRequestsByCustomer([request('1', 'a', '2026-10-02T00:00:00Z'), request('2', 'g', '2026-10-01T00:00:00Z', { priority: 1 }), request('3', '', '2026-10-03T00:00:00Z')], customers)
    expect(orderRequestGroups(groups, { ordering: 'createdAt', direction: 'desc', importantFirst: false }).map(group => group.primary.id)).toEqual(['3', '1', '2'])
    expect(orderRequestGroups(groups, { ordering: 'createdAt', direction: 'desc', importantFirst: true }).map(group => group.primary.id)).toEqual(['2', '3', '1'])
    expect(orderRequestGroups(groups, { ordering: 'customerName', direction: 'asc', importantFirst: false }).map(group => group.primary.id)).toEqual(['1', '2', '3'])
    expect(orderRequestGroups(groups, { ordering: 'customerRevenue', direction: 'desc', importantFirst: false }).map(group => group.primary.id)).toEqual(['2', '1', '3'])
  })

  it('previews the request on one line, falling back to the source host', () => {
    expect(requestSubtitle({ body: 'Line one\n\nline two', sourceUrl: '' })).toBe('Line one line two')
    expect(requestSubtitle({ body: '  ', sourceUrl: 'https://support.acme.com/t/1' })).toBe('support.acme.com')
  })

  it('detects and normalizes source links', () => {
    expect(looksLikeUrl('acme.com/ticket/1')).toBe(true)
    expect(looksLikeUrl('Acme Corp')).toBe(false)
    expect(normalizeSourceUrl('acme.com/x')).toBe('https://acme.com/x')
    expect(normalizeSourceUrl('not a url')).toBeUndefined()
    expect(normalizeSourceUrl('')).toBe('')
  })

  it('formats row dates like Linear (no suffix)', () => {
    const now = Date.parse('2026-10-07T12:00:00Z')
    expect(compactRelativeTime('2026-10-07T11:59:40Z', now)).toBe('now')
    expect(compactRelativeTime('2026-10-07T11:55:00Z', now)).toBe('5m')
    expect(compactRelativeTime('2026-10-07T09:00:00Z', now)).toBe('3h')
    expect(compactRelativeTime('2026-10-05T12:00:00Z', now)).toBe('2d')
    expect(compactRelativeTime('2026-09-16T12:00:00Z', now)).toBe('3w')
    expect(compactRelativeTime('2026-05-07T12:00:00Z', now)).toBe('5mo')
    expect(compactRelativeTime('2024-10-07T12:00:00Z', now)).toBe('2y')
  })

  it('spells out three customer names and counts the rest', () => {
    expect(customersSummary(['A', 'B', 'C', 'D'])).toEqual({ names: ['A', 'B', 'C', 'D'], rest: 0 })
    expect(customersSummary(['A', 'B', 'C', 'D', 'E'])).toEqual({ names: ['A', 'B', 'C'], rest: 2 })
  })

  it('overlays local writes until the workspace snapshot catches up', () => {
    const base = [request('1', 'a', '2026-10-01T00:00:00Z', { issueId: 'i1' })]
    const moved = { ...base[0], issueId: 'i2', updatedAt: '2026-10-02T00:00:00Z' }
    const created = request('2', 'a', '2026-10-03T00:00:00Z', { issueId: 'i1' })
    const overlay = { upserts: new Map([[moved.id, moved], [created.id, created]]), removed: new Set<string>() }
    expect(applyRequestOverlay(base, overlay, item => item.issueId === 'i1').map(item => item.id)).toEqual(['2'])
    expect(applyRequestOverlay(base, { upserts: new Map(), removed: new Set(['1']) }, () => true)).toEqual([])
  })
})

describe('"Select customer…" options', () => {
  const list = [customer('g', 'Globex'), customer('a', 'Acme')]
  it('offers Unknown customer then customers by name', () => {
    expect(customerPickOptions(list, '').map(option => option.label)).toEqual(['Unknown customer', 'Acme', 'Globex'])
  })
  it('asks to type a name when there are no customers', () => {
    expect(customerPickOptions([], '').map(option => [option.kind, option.label])).toEqual([['hint', 'Type a name to create your first customer']])
  })
  it('filters and offers Create new customer, or Add link as source for links', () => {
    const typed = customerPickOptions(list, 'acm')
    expect(typed.map(option => option.label)).toEqual(['Acme', 'Create new customer: "acm"'])
    expect(typed[1].pick).toEqual({ pendingCustomerName: 'acm' })
    const link = customerPickOptions(list, 'support.acme.com/t/9')
    expect(link.at(-1)).toMatchObject({ kind: 'source', label: 'Add link as source', pick: { sourceUrl: 'https://support.acme.com/t/9' } })
  })
})

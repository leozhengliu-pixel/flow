import { fireEvent, render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { customerFilterOptions } from '@/components/issue-explorer/issue-explorer-model'
import { ProjectsDataView, type ProjectPageItem } from './projects-data-view'
import { ProjectCustomerFilterValues } from './project-customer-filter'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })
beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))

const project: ProjectPageItem = {
  id: 'project-1', name: 'Project one', health: 'on-track', priority: 'high', issueCount: 2, progress: 40, status: 'In Progress',
  customers: [{ id: 'c1', name: 'Acme', annualRevenue: 240000 }, { id: 'c2', name: 'Beta', annualRevenue: 0 }], customerCount: 2, importantCustomerIds: ['c1'],
  customerSettings: { customerRevenueFormat: 'monthly', customerRevenueCurrency: 'EUR' },
}

describe('project Customers properties', () => {
  it('renders Linear\'s Customers and Revenue columns in the workspace currency and unit', () => {
    render(<I18nProvider><ProjectsDataView groups={[{ id: 'group', name: 'In Progress', projects: [project] }]} visibleProperties={['Customers', 'Customer revenue']}/></I18nProvider>)
    expect(screen.getByRole('columnheader', { name: /Customers/ })).toBeVisible()
    expect(screen.getByRole('columnheader', { name: /Revenue/ }).textContent).toBe('Revenue (€/mo)')
    const row = screen.getByRole('row', { name: 'Project one' })
    expect(row.querySelector('[data-customer-chip]')?.textContent).toContain('2')
    expect(within(row).getByLabelText(/Customer revenue/).textContent).toMatch(/€20(\.0)?K/)
  })
})

describe('project Customers filter', () => {
  const data = makeBootstrap({ customers: [{ id: 'c1', name: 'Acme', status: 'active', domains: [], createdAt: '', updatedAt: '' }] })
  it('lists the eight blocks and applies a number comparison', () => {
    const onSelect = vi.fn()
    render(<I18nProvider><ProjectCustomerFilterValues options={customerFilterOptions(data)} onSelect={onSelect}/></I18nProvider>)
    const blocks = within(screen.getByRole('dialog', { name: 'Customers filters' })).getAllByRole('option')
    expect(blocks.map(block => block.textContent?.replace('▶', ''))).toEqual(['Customer name', 'Customer count', 'Important customer count', 'Customer owner', 'Customer status', 'Customer tier', 'Customer revenue', 'Customer size'])
    fireEvent.pointerMove(blocks[1])
    fireEvent.change(screen.getByRole('textbox', { name: 'Enter customer count…' }), { target: { value: '2' } })
    fireEvent.click(screen.getByRole('option', { name: /^less than or equals\s*2$/ }))
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 'customer-count:2', filterLabel: 'Customer count', comparison: 'lte' }))
    fireEvent.pointerMove(blocks[0])
    fireEvent.click(screen.getByRole('option', { name: /Acme/ }))
    expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'customer:c1', filterLabel: 'Customer name' }))
  })
})

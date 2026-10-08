import { fireEvent, render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { customerFilterOptions } from '@/components/issue-explorer/issue-explorer-model'
import { MyIssuesFilterMenu } from './my-issues-filter-menu'
import type { MyIssuesFilterKey } from './my-issues-surface'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })
beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))

const data = makeBootstrap({ customers: [{ id: 'c1', name: 'Acme', status: 'active', domains: [], createdAt: '', updatedAt: '' }] })
data.workspaceSettings.featureSettings = { ...data.workspaceSettings.featureSettings, customerRevenueFormat: 'monthly', customerRevenueCurrency: 'EUR' }
const options = (field: MyIssuesFilterKey) => field === 'customers' ? customerFilterOptions(data) : undefined

describe('Customers filter sub-menu', () => {
  it("lists Linear's eight customer blocks with their icons and no search band", async () => {
    render(<I18nProvider><MyIssuesFilterMenu open trigger={<button type="button">Filter</button>} options={options} onOpenChange={vi.fn()} onToggle={vi.fn()}/></I18nProvider>)
    fireEvent.mouseMove(await screen.findByRole('option', { name: /^Customers/ }))
    const menu = await screen.findByRole('listbox', { name: 'Customers' })
    const rows = within(menu).getAllByRole('option')
    expect(rows.map(row => row.textContent?.replace('▶', ''))).toEqual(['Customer name', 'Customer count', 'Important customer count', 'Customer owner', 'Customer status', 'Customer tier', 'Customer revenue', 'Customer size'])
    for (const row of rows) expect(row.querySelector('svg, .customer-status-icon'), row.textContent ?? '').not.toBeNull()
    expect(menu.closest('[data-level="1"]')?.querySelector('[data-hidden]')).not.toBeNull()
  })

  it('offers the four comparisons for a typed customer count and applies one', async () => {
    const onToggle = vi.fn()
    render(<I18nProvider><MyIssuesFilterMenu open trigger={<button type="button">Filter</button>} options={options} onOpenChange={vi.fn()} onToggle={onToggle}/></I18nProvider>)
    fireEvent.mouseMove(await screen.findByRole('option', { name: /^Customers/ }))
    fireEvent.mouseMove(within(await screen.findByRole('listbox', { name: 'Customers' })).getByRole('option', { name: /Customer count/ }))
    const input = await screen.findByRole('searchbox', { name: 'Enter customer count…' })
    expect(screen.queryByRole('listbox', { name: 'Customer count' })).not.toBeInTheDocument()
    fireEvent.change(input, { target: { value: '3' } })
    const rows = within(screen.getByRole('listbox', { name: 'Customer count' })).getAllByRole('option')
    expect(rows.map(row => row.textContent)).toEqual(['greater than or equals 3', 'less than or equals 3', 'equals 3', 'not equals 3'])
    fireEvent.click(rows[0])
    expect(onToggle).toHaveBeenCalledWith('customers', expect.objectContaining({ id: 'customer-count:3', filterLabel: 'Customer count', comparison: 'gte' }))
  })

  it('stores revenue typed in the monthly unit as annual revenue, labelled in the workspace currency', async () => {
    const onToggle = vi.fn()
    render(<I18nProvider><MyIssuesFilterMenu open trigger={<button type="button">Filter</button>} options={options} onOpenChange={vi.fn()} onToggle={onToggle}/></I18nProvider>)
    fireEvent.mouseMove(await screen.findByRole('option', { name: /^Customers/ }))
    fireEvent.mouseMove(within(await screen.findByRole('listbox', { name: 'Customers' })).getByRole('option', { name: /Customer revenue/ }))
    fireEvent.change(await screen.findByRole('searchbox', { name: 'Enter customer revenue…' }), { target: { value: '1K' } })
    fireEvent.click(within(screen.getByRole('listbox', { name: 'Customer revenue' })).getAllByRole('option')[2])
    expect(onToggle).toHaveBeenCalledWith('customers', expect.objectContaining({ id: 'customer-revenue:12000', comparison: 'eq', label: expect.stringContaining('1,000') }))
    expect(onToggle.mock.calls[0][1].label).toMatch(/€/)
  })
})

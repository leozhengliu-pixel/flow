import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrap } from '@/test/fixtures'
import type { Customer, CustomerRequest } from '@/types/flow'
import { IssueCustomerRequests } from './issue-customer-requests'
import { isAddCustomerRequestShortcut, requestIssueCustomerRequest } from './customer-request-events'

const updateCustomerRequest = vi.fn()
const deleteCustomerRequest = vi.fn()
const createCustomerRequest = vi.fn()
const confirmAction = vi.fn()

vi.mock('@/lib/api', () => ({
  updateCustomerRequest: (...args: unknown[]) => updateCustomerRequest(...args),
  deleteCustomerRequest: (...args: unknown[]) => deleteCustomerRequest(...args),
  createCustomerRequest: (...args: unknown[]) => createCustomerRequest(...args),
  uploadCustomerRequestAttachment: vi.fn(),
  createIssue: vi.fn(),
  listIssueRecords: vi.fn().mockResolvedValue({ items: [] }),
}))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: (...args: unknown[]) => confirmAction(...args) }))
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))

// cmdk measures its list.
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
Element.prototype.scrollIntoView ??= function scrollIntoView() {}

const customers = [
  { id: 'c1', name: 'Acme', status: 'active', domains: [], createdAt: '', updatedAt: '' },
  { id: 'c2', name: 'Globex', status: 'active', domains: [], createdAt: '', updatedAt: '' },
] as Customer[]
const creator = { id: 'u1', name: 'Ada', displayName: 'Ada', email: 'ada@example.com', active: true }
const request = (id: string, customerId: string, createdAt: string, extra: Partial<CustomerRequest> = {}) =>
  ({ id, customerId, body: `Need ${id}`, source: 'manual', creator, issueId: 'issue-1', attachments: [], createdAt, updatedAt: createdAt, ...extra }) as CustomerRequest

function renderSection(requests: CustomerRequest[]) {
  const data = makeBootstrap({ customers, customerRequests: requests })
  return render(<MemoryRouter><I18nProvider><TooltipProvider><IssueCustomerRequests data={data} issueId="issue-1" requests={requests}/></TooltipProvider></I18nProvider></MemoryRouter>)
}

describe('IssueCustomerRequests', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
  })

  it('stays hidden until the issue has a request', () => {
    renderSection([])
    expect(screen.queryByRole('region', { name: 'Customers' })).toBeNull()
  })

  it('shows one row per customer with the count, important mark, preview and date', () => {
    renderSection([
      request('r1', 'c1', '2026-10-01T00:00:00Z'),
      request('r2', 'c1', '2026-10-02T00:00:00Z', { priority: 1 }),
      request('r3', 'c2', '2026-09-01T00:00:00Z', { body: 'Audit\nlog' }),
    ])
    const section = screen.getByRole('region', { name: 'Customers' })
    expect(within(section).getByRole('button', { name: 'Collapse customers section' })).toHaveAttribute('aria-expanded', 'true')
    expect(within(section).getByRole('button', { name: 'Display options' })).toBeInTheDocument()
    expect(within(section).getByRole('button', { name: 'Add customer request' })).toBeInTheDocument()
    const rows = within(section).getAllByRole('group')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText('Acme')).toBeInTheDocument()
    expect(within(rows[0]).getByText('Need r2')).toHaveClass('customer-request-row__preview')
    expect(within(rows[0]).getByText('2')).toHaveClass('customer-request-row__count')
    expect(within(rows[0]).getByLabelText('Important')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Audit log')).toBeInTheDocument()
  })

  it('expands a row to the requests with who added them', async () => {
    renderSection([request('r1', 'c1', '2026-10-01T00:00:00Z'), request('r2', 'c1', '2026-10-02T00:00:00Z')])
    const row = screen.getByRole('group', { name: 'Acme' })
    fireEvent.click(row.querySelector('.customer-request-row__header')!)
    expect(row).toHaveClass('is-expanded')
    expect(within(row).getAllByText('Ada')).toHaveLength(2)
    expect(within(row).getByText('Need r1').closest('.customer-request-row__body')).not.toBeNull()
  })

  it('collapses the section to a customer count', async () => {
    const user = userEvent.setup()
    renderSection([request('r1', 'c1', '2026-10-01T00:00:00Z'), request('r2', 'c2', '2026-10-02T00:00:00Z')])
    await user.click(screen.getByRole('button', { name: 'Collapse customers section' }))
    expect(screen.getByRole('button', { name: 'Expand customers section' })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('group')).toBeNull()
    expect(document.querySelector('.issue-customer-requests__count')).toHaveTextContent('2')
  })

  it('opens "Select customer…" from the issue menu event and then the composer for the pick', async () => {
    const user = userEvent.setup()
    renderSection([])
    let handled = false
    act(() => { handled = requestIssueCustomerRequest('issue-1') })
    expect(handled).toBe(true)
    const input = await screen.findByPlaceholderText('Select customer…')
    expect(screen.getByRole('option', { name: 'Unknown customer' })).toBeInTheDocument()
    await user.type(input, 'Glob')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Search customers' })).toHaveTextContent('Globex'))
    expect(screen.getByRole('region', { name: 'Customers' })).toBeInTheDocument()
    expect(requestIssueCustomerRequest('other-issue')).toBe(false)
  })

  it('offers Linear’s request menu and deletes after confirming', async () => {
    const user = userEvent.setup()
    confirmAction.mockResolvedValue(true)
    deleteCustomerRequest.mockResolvedValue(undefined)
    renderSection([request('r1', 'c1', '2026-10-01T00:00:00Z', { sourceUrl: 'https://support.acme.com/t/1' })])
    await user.click(screen.getByRole('button', { name: 'Request options' }))
    const menu = await screen.findByRole('menu')
    const labels = within(menu).getAllByRole('menuitem').map(item => item.textContent)
    expect(labels).toEqual(['Mark as important', 'Move to…▶', 'Copy▶', 'Create issue from request…', 'Edit request', 'New request from Acme', 'Change customer…▶', 'Open customer', 'Delete'])
    await user.click(within(menu).getByRole('menuitem', { name: 'Delete' }))
    await waitFor(() => expect(confirmAction).toHaveBeenCalledWith('Delete customer request from Acme', expect.objectContaining({ description: 'You cannot undo this action.', confirmLabel: 'Delete' })))
    await waitFor(() => expect(deleteCustomerRequest).toHaveBeenCalledWith('r1'))
    await waitFor(() => expect(screen.queryByRole('group')).toBeNull())
  })

  it('marks a request as important from its menu', async () => {
    const user = userEvent.setup()
    updateCustomerRequest.mockImplementation(async (_id, input) => ({ ...request('r1', 'c1', '2026-10-01T00:00:00Z'), ...input, updatedAt: '2026-10-05T00:00:00Z' }))
    renderSection([request('r1', 'c1', '2026-10-01T00:00:00Z')])
    await user.click(screen.getByRole('button', { name: 'Request options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Mark as important' }))
    await waitFor(() => expect(updateCustomerRequest).toHaveBeenCalledWith('r1', { priority: 1 }))
    await waitFor(() => expect(screen.getByRole('group', { name: 'Acme' }).querySelector('.customer-request-row__important')).not.toBeNull())
  })

  it('uses ⌃R on macOS and Ctrl+Alt+R elsewhere', () => {
    expect(isAddCustomerRequestShortcut(new KeyboardEvent('keydown', { key: 'r', ctrlKey: true }), true)).toBe(true)
    expect(isAddCustomerRequestShortcut(new KeyboardEvent('keydown', { key: 'r', metaKey: true }), true)).toBe(false)
    expect(isAddCustomerRequestShortcut(new KeyboardEvent('keydown', { key: 'r', ctrlKey: true, altKey: true }), false)).toBe(true)
    expect(isAddCustomerRequestShortcut(new KeyboardEvent('keydown', { key: 'r', ctrlKey: true }), false)).toBe(false)
  })
})

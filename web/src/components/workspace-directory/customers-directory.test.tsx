import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, Customer } from '@/types/flow'
import { CustomersDirectory } from './customers-directory'
import { CUSTOMER_DOCS_URL } from './customers-directory-model'
import { WorkspaceDirectoryPage } from './workspace-directory-page'

const statuses = [
  { id: 'st-active', name: 'Active', color: '#4cb782', position: 0, createdAt: '', updatedAt: '' },
  { id: 'st-churned', name: 'Churned', color: '#f2c94c', position: 1, createdAt: '', updatedAt: '' },
]
const customers: Customer[] = [
  { id: 'acme', name: 'Acme', ownerId: viewer.id, status: 'active', annualRevenue: 1250000, size: 240, domains: ['acme.com', 'acme.io'], createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '' },
  { id: 'globex', name: 'Globex', status: 'st-churned', annualRevenue: 5000, domains: ['globex.com'], createdAt: '2026-02-01T00:00:00.000Z', updatedAt: '' },
]

function customerData(overrides: Partial<BootstrapData> = {}) {
  return makeBootstrap({
    users: [viewer, teammate],
    customers,
    customerStatuses: statuses,
    customerTiers: [],
    customerRequests: [{ id: 'r1', customerId: 'acme' }, { id: 'r2', customerId: 'acme' }, { id: 'r3', customerId: 'globex', archivedAt: '2026-01-01' }],
    favorites: [],
    subscriptions: [],
    ...overrides,
  } as unknown as Partial<BootstrapData>)
}

function renderDirectory(data = customerData()) {
  const props = { onCreate: vi.fn(), onUpdate: vi.fn().mockResolvedValue(undefined), onDelete: vi.fn().mockResolvedValue(undefined), onOpen: vi.fn() }
  render(<I18nProvider><TooltipProvider><MemoryRouter><CustomersDirectory data={data} {...props}/></MemoryRouter></TooltipProvider></I18nProvider>)
  return props
}

const rowFor = (name: string) => screen.getByText(name, { selector: '.customers-name' }).closest('.customers-row') as HTMLElement

beforeEach(() => localStorage.clear())

describe('Customers page (Linear CustomersPage)', () => {
  it('shows Linear\'s empty state with its illustration, copy and actions', async () => {
    const user = userEvent.setup()
    const props = renderDirectory(customerData({ customers: [] }))
    expect(screen.getByText('Customers', { selector: '.customers-empty__title' })).toBeVisible()
    expect(screen.getByText('Add organizations using your product to track their feature requests and use attributes like revenue and size to prioritize development.')).toBeVisible()
    const illustration = document.querySelector('.customers-empty-illustration')!
    expect(illustration.getAttribute('viewBox')).toBe('0 0 128 122')
    expect(illustration.querySelectorAll('path')).toHaveLength(40)
    expect(screen.getByRole('link', { name: 'Documentation' })).toHaveAttribute('href', CUSTOMER_DOCS_URL)
    await user.click(screen.getByRole('button', { name: 'Create new customer' }))
    expect(props.onCreate).toHaveBeenCalled()
    expect(document.querySelector('.customers-columns')).toBeNull()
  })

  it('lists customers with Linear\'s default columns, newest first', () => {
    renderDirectory()
    const header = document.querySelector('.customers-columns') as HTMLElement
    expect([...header.children].map(cell => cell.textContent)).toEqual(['Name', 'Requests', 'Annual revenue', 'Size', 'Status', 'Tier', 'Owner'])
    expect([...document.querySelectorAll('.customers-name')].map(node => node.textContent)).toEqual(['Globex', 'Acme'])
    const acme = rowFor('Acme')
    expect(acme).toHaveAttribute('href', '/workspace/customer/acme-acme')
    expect(within(acme).getByText('2')).toHaveClass('customers-count')
    expect(within(acme).getByText('$1.3M')).toBeVisible()
    expect(within(acme).getByText('240')).toBeVisible()
    expect(within(acme).getByRole('button', { name: 'Change customer status' })).toHaveTextContent('Active')
    expect(within(acme).getByRole('button', { name: 'Owner: Viewer' })).toBeVisible()
    // Archived requests don't count, and empty counts render nothing.
    expect(rowFor('Globex').querySelector('.customers-count')).toBeNull()
    expect(within(rowFor('Globex')).getByRole('button', { name: 'Change customer status' })).toHaveTextContent('Churned')
  })

  it('finds by name or domain and orders from the column header', async () => {
    const user = userEvent.setup()
    renderDirectory()
    await user.type(screen.getByRole('searchbox', { name: 'Find by name or domain…' }), 'acme.io')
    expect([...document.querySelectorAll('.customers-name')].map(node => node.textContent)).toEqual(['Acme'])
    await user.clear(screen.getByRole('searchbox', { name: 'Find by name or domain…' }))
    await user.click(screen.getByRole('button', { name: 'Order by Name' }))
    expect([...document.querySelectorAll('.customers-name')].map(node => node.textContent)).toEqual(['Acme', 'Globex'])
    await user.click(screen.getByRole('button', { name: 'Order by Name, sorted ascending' }))
    expect([...document.querySelectorAll('.customers-name')].map(node => node.textContent)).toEqual(['Globex', 'Acme'])
    expect(JSON.parse(localStorage.getItem('flow.customers.preferences:workspace-1:user-1')!)).toMatchObject({ ordering: 'name', direction: 'desc' })
  })

  it('offers Linear\'s ordering keys and display properties, and toggles columns', async () => {
    const user = userEvent.setup()
    renderDirectory()
    await user.click(screen.getByRole('button', { name: 'Display options' }))
    const properties = [...document.querySelectorAll('.workspace-directory-display-menu__properties button')]
    expect(properties.map(button => button.textContent)).toEqual(['Requests', 'Annual revenue', 'Size', 'Owner', 'Status', 'Tier', 'Domains', 'Data source'])
    expect(properties.map(button => button.getAttribute('aria-pressed'))).toEqual(['true', 'true', 'true', 'true', 'true', 'true', 'false', 'false'])
    await user.click(screen.getByRole('button', { name: 'Domains' }))
    expect(within(rowFor('Acme')).getByText('acme.com, acme.io')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Choose ordering' }))
    expect(screen.getAllByRole('menuitem').map(item => item.textContent)).toEqual(['Request count', 'Created', 'Name', 'Annual revenue', 'Size', 'Status', 'Tier'])
  })

  it('filters by revenue with Linear\'s operators and shows a segmented chip', async () => {
    const user = userEvent.setup()
    renderDirectory()
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    await user.click(await screen.findByRole('menuitem', { name: /Revenue/ }))
    const input = await screen.findByPlaceholderText('Enter revenue…')
    fireEvent.change(input, { target: { value: '10k' } })
    expect(screen.getAllByRole('menuitem').filter(item => item.textContent?.includes('$10,000')).map(item => item.textContent)).toEqual([
      'greater than or equals $10,000', 'less than or equals $10,000', 'equals $10,000', 'not equals $10,000',
    ])
    fireEvent.keyDown(input, { key: 'Enter' })
    expect([...document.querySelectorAll('.customers-name')].map(node => node.textContent)).toEqual(['Acme'])
    const chip = document.querySelector('.customers-filter-chip') as HTMLElement
    expect(chip).toHaveTextContent('Revenuegreater than or equals$10,000')
    await user.click(within(chip).getByRole('button', { name: 'Revenue operator' }))
    await user.click(await screen.findByRole('menuitem', { name: /less than or equals/ }))
    expect([...document.querySelectorAll('.customers-name')].map(node => node.textContent)).toEqual(['Globex'])
    await user.click(screen.getByRole('button', { name: 'Clear all filters' }))
    expect(document.querySelectorAll('.customers-name')).toHaveLength(2)
    expect(document.querySelector('.customers-filter-bar')).toBeNull()
  })

  it('opens the row on click and offers Linear\'s customer actions on right-click', async () => {
    const user = userEvent.setup()
    const props = renderDirectory()
    await user.click(screen.getByText('Acme', { selector: '.customers-name' }))
    expect(props.onOpen).toHaveBeenCalledWith(customers[0])
    fireEvent.contextMenu(rowFor('Acme'))
    const menu = await screen.findByRole('menu', { name: 'Customer actions' })
    expect(within(menu).getAllByRole('menuitem').map(item => item.textContent?.replace(/[⌘▶].*$/, ''))).toEqual(['Edit…', 'Favorite', 'Copy…', 'Subscribe', 'Merge with…', 'Delete'])
    await user.click(within(menu).getByRole('menuitem', { name: 'Delete' }))
    expect(props.onDelete).toHaveBeenCalledWith(customers[0])
  })

  it('merges a customer into another from "Merge with…"', async () => {
    const user = userEvent.setup()
    renderDirectory()
    fireEvent.contextMenu(rowFor('Acme'))
    await user.click(await screen.findByRole('menuitem', { name: /Merge with…/ }))
    const search = await screen.findByPlaceholderText('Search for customer to merge with…')
    await user.type(search, 'glob')
    await user.click(await screen.findByRole('menuitem', { name: /Globex/ }))
    expect(await screen.findByRole('dialog')).toBeVisible()
  })

  it('edits a customer from the row menu with Linear\'s dialog', async () => {
    renderDirectory()
    fireEvent.contextMenu(rowFor('Acme'))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Edit…' }))
    expect(await screen.findByRole('dialog', { name: 'Edit customer' })).toBeVisible()
    expect(screen.getByPlaceholderText('Customer name')).toHaveValue('Acme')
  })

  it('changes status and owner from the row pickers', async () => {
    const user = userEvent.setup()
    const props = renderDirectory()
    await user.click(within(rowFor('Acme')).getByRole('button', { name: 'Change customer status' }))
    await user.click(await screen.findByRole('menuitem', { name: /Churned/ }))
    await waitFor(() => expect(props.onUpdate).toHaveBeenCalledWith(customers[0], { status: 'st-churned' }))
    await user.click(within(rowFor('Globex')).getByRole('button', { name: 'Change customer owner' }))
    await user.click(await screen.findByRole('menuitem', { name: /Teammate/ }))
    await waitFor(() => expect(props.onUpdate).toHaveBeenCalledWith(customers[1], { ownerId: teammate.id }))
  })

  it('shows the page header like Linear: plain title, ghost "New customer", no match count', () => {
    const noop = vi.fn()
    render(<I18nProvider><TooltipProvider><MemoryRouter><WorkspaceDirectoryPage
      kind="customers" data={customerData()} onOpenSidebar={noop} onNavigateTeamMembers={noop} onNavigateMember={noop} onNavigateTeam={noop}
      onNavigateTeamProjects={noop} onNavigateTeamCycles={noop} onNavigateTeamsSettings={noop} onNewTeam={noop}
      onCreateCustomer={vi.fn()} onUpdateCustomer={vi.fn()} onDeleteCustomer={vi.fn()} onOpenCustomer={noop} onReload={vi.fn().mockResolvedValue(undefined)}
    /></MemoryRouter></TooltipProvider></I18nProvider>)
    const header = screen.getByRole('banner')
    expect(within(header).getByRole('heading', { name: 'Customers' })).toBeVisible()
    expect(within(header).getByRole('button', { name: 'New customer' })).toHaveClass('is-ghost')
    expect(header.querySelector('small')).toBeNull()
    expect(document.querySelector('main')).toHaveClass('workspace-directory--customers')
  })
})

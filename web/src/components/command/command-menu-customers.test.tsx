import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, Customer } from '@/types/flow'
import { CommandMenu } from './command-menu'

const api = vi.hoisted(() => ({ searchWorkspace: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api }))

const acme = { id: 'acme', name: 'Acme', status: 'active', domains: ['acme.com'], createdAt: '', updatedAt: '' } as Customer
const globex = { id: 'globex', name: 'Globex', status: 'active', domains: [], createdAt: '', updatedAt: '' } as Customer
const data = makeBootstrap({ customers: [acme, globex] } as Partial<BootstrapData>)

function setup(props: { customers?: boolean; initialCustomerPicker?: boolean } = {}) {
  const handlers = { onNavigateCustomers: vi.fn(), onGoToCustomers: vi.fn(), onOpenCustomer: vi.fn(), onOpenChange: vi.fn() }
  const noop = vi.fn()
  render(<MemoryRouter><I18nProvider><CommandMenu open data={data} initialCustomerPicker={props.initialCustomerPicker} onOpenChange={handlers.onOpenChange}
    onCreateIssue={noop} onCreateIssueTemplate={noop} onCreateProject={noop} onCreateView={noop} onCreateInitiative={noop} onSearchWorkspace={noop}
    onNavigateInbox={noop} onNavigateMyIssues={noop} onNavigateProjects={noop} onNavigateInitiatives={noop} onNavigateViews={noop} onNavigateMembers={noop}
    onNavigateCustomers={handlers.onNavigateCustomers} onGoToCustomers={props.customers === false ? undefined : handlers.onGoToCustomers}
    onOpenCustomer={handlers.onOpenCustomer} onNavigateAgent={noop} onOpenResult={noop}/></I18nProvider></MemoryRouter>)
  return { ...handlers, user: userEvent.setup() }
}

describe('customer commands (Linear: Create new customer…, Go to customers, Open customer…)', () => {
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    api.searchWorkspace.mockResolvedValue({ results: [] })
  })
  afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks() })

  it('lists the customer commands with Linear\'s names and shortcuts', async () => {
    const { user, onGoToCustomers, onNavigateCustomers } = setup()
    const go = screen.getByRole('option', { name: /Go to customers/ })
    expect(go).toHaveTextContent('GthenQ')
    expect(screen.getByRole('option', { name: /Open customer…/ })).toHaveTextContent('OthenQ')
    await user.click(go)
    expect(onGoToCustomers).toHaveBeenCalled()
    await user.click(screen.getByRole('option', { name: /Create new customer…/ }))
    expect(onNavigateCustomers).toHaveBeenCalled()
  })

  it('picks a customer from the "Open customer…" page', async () => {
    const { user, onOpenCustomer } = setup()
    await user.click(screen.getByRole('option', { name: /Open customer…/ }))
    expect(screen.getByPlaceholderText('Open customer…')).toBeVisible()
    await user.type(screen.getByPlaceholderText('Open customer…'), 'acme.com')
    expect(screen.queryByRole('option', { name: 'Globex' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('option', { name: 'Acme' }))
    expect(onOpenCustomer).toHaveBeenCalledWith(acme)
  })

  it('opens straight on the picker for the O then Q shortcut', () => {
    setup({ initialCustomerPicker: true })
    expect(screen.getByPlaceholderText('Open customer…')).toBeVisible()
    expect(screen.getByRole('option', { name: 'Globex' })).toBeVisible()
  })

  it('hides customer commands when customer requests are unavailable', () => {
    setup({ customers: false })
    expect(screen.queryByRole('option', { name: /customer/i })).not.toBeInTheDocument()
  })
})

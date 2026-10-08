import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, backlog, completed, viewer } from '@/test/fixtures'
import type { BootstrapData, Customer, CustomerRequest } from '@/types/flow'

const api = vi.hoisted(() => ({
  createIssue: vi.fn(),
  createCustomerRequest: vi.fn(),
  updateCustomerRequest: vi.fn(),
  listIssueRecords: vi.fn(),
  uploadCustomerRequestAttachment: vi.fn(),
  addSubscription: vi.fn(),
  removeSubscription: vi.fn(),
  updateCustomer: vi.fn(),
  deleteCustomer: vi.fn(),
  deleteCustomerRequest: vi.fn(),
  deleteCustomerRequestAttachment: vi.fn(),
  mergeCustomer: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
vi.mock('@/lib/resource-preferences', () => ({ refreshResourcePreferences: vi.fn().mockResolvedValue(undefined) }))

import { CustomerDetailPage } from './customer-detail-page'
import { customerRequestIssueState, customerRequestIssueTeam, DEFAULT_CUSTOMER_PAGE_VIEW, groupCustomerNeeds, orderCustomerNeedGroups, passesCompletedWindow, sectionCustomerNeedGroups, shortRelativeTime, statusTypeOf } from './customer-page-model'

const customer: Customer = {
  id: 'customer-1', name: 'Acme', status: 'Active', tier: 'Enterprise', annualRevenue: 1_200_000, size: 240,
  ownerId: viewer.id, domains: ['acme.test', 'acme.io'], createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-01-01T00:00:00.000Z',
}

function need(overrides: Partial<CustomerRequest>): CustomerRequest {
  return { id: 'need', customerId: customer.id, body: '', source: 'manual', creator: viewer, attachments: [], createdAt: '2026-09-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z', ...overrides }
}

const doneIssue = makeIssue({ id: 'issue-2', identifier: 'TST-2', title: 'Shipped thing', state: completed, completedAt: '2026-01-01T00:00:00.000Z' })

function fixture(overrides: Partial<BootstrapData> = {}) {
  return makeBootstrap({
    customers: [customer, { ...customer, id: 'customer-2', name: 'Globex', domains: [] }],
    customerStatuses: [{ id: 'status-active', name: 'Active', color: '#4cb782', position: 0 }, { id: 'status-churned', name: 'Churned', color: '#f2c94c', position: 1 }],
    customerTiers: [{ id: 'tier-1', name: 'Enterprise', color: '#5e6ad2', position: 0 }],
    customerRequests: [
      need({ id: 'need-1', issueId: 'issue-1', body: 'We need SSO', priority: 1, createdAt: '2026-09-02T00:00:00.000Z' }),
      need({ id: 'need-2', issueId: 'issue-1', body: 'And SCIM', createdAt: '2026-09-03T00:00:00.000Z' }),
      need({ id: 'need-3', issueId: 'issue-2', createdAt: '2026-09-04T00:00:00.000Z' }),
      need({ id: 'need-4', projectId: 'project-1', body: 'Bundle it', archivedAt: '2026-09-05T00:00:00.000Z' }),
    ],
    issues: [makeIssue(), doneIssue],
    favorites: [],
    subscriptions: [],
    teamSettings: {},
    ...overrides,
  } as Partial<BootstrapData>)
}

function renderPage(data = fixture()) {
  const onReload = vi.fn().mockResolvedValue(undefined)
  const onBack = vi.fn()
  render(<MemoryRouter><I18nProvider><TooltipProvider><CustomerDetailPage data={data} customer={data.customers.find(item => item.id === customer.id)!} onBack={onBack} onReload={onReload}/></TooltipProvider></I18nProvider></MemoryRouter>)
  return { onReload, onBack }
}

beforeEach(() => {
  localStorage.clear()
  Object.values(api).forEach(fn => fn.mockReset())
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
})

describe('customer page model (Linear CustomerNeedListProvider, customer type)', () => {
  const data = fixture()
  const lookup = (id: string) => data.issues.find(issue => issue.id === id)
  const project = (id: string) => data.projects.find(item => item.id === id)

  it('groups requests by their issue or project with the newest request first', () => {
    const groups = groupCustomerNeeds(data.customerRequests, lookup, project)
    expect(groups.map(group => group.key)).toEqual(['issue:issue-2', 'issue:issue-1', 'project:project-1'])
    const sso = groups.find(group => group.key === 'issue:issue-1')!
    expect(sso.primary.id).toBe('need-2')
    expect(sso.additional.map(item => item.id)).toEqual(['need-1'])
  })

  it('orders important requests first, then by created date or status type', () => {
    const groups = groupCustomerNeeds(data.customerRequests, lookup, project)
    expect(orderCustomerNeedGroups(groups, DEFAULT_CUSTOMER_PAGE_VIEW).map(group => group.key)).toEqual(['issue:issue-1', 'issue:issue-2', 'project:project-1'])
    expect(orderCustomerNeedGroups(groups, { ordering: 'statusType', importantFirst: false }).map(group => statusTypeOf(group))).toEqual(['started', 'started', 'completed'])
    expect(sectionCustomerNeedGroups(orderCustomerNeedGroups(groups, DEFAULT_CUSTOMER_PAGE_VIEW), 'statusType').map(section => [section.title, section.groups.length])).toEqual([['Started', 2], ['Completed', 1]])
  })

  it('maps untriaged backlog issues of triage teams to Triage and hides old completed work', () => {
    const triage = makeIssue({ state: backlog })
    expect(statusTypeOf({ issue: triage }, { teamSettings: { 'team-1': { triageEnabled: true } } } as never)).toBe('triage')
    expect(statusTypeOf({ issue: { ...triage, triagedAt: '2026-01-01' } }, { teamSettings: { 'team-1': { triageEnabled: true } } } as never)).toBe('backlog')
    expect(passesCompletedWindow({ issue: doneIssue }, 'all')).toBe(true)
    expect(passesCompletedWindow({ issue: doneIssue }, 'week', Date.parse('2026-01-03T00:00:00Z'))).toBe(true)
    expect(passesCompletedWindow({ issue: doneIssue }, 'day', Date.parse('2026-01-03T00:00:00Z'))).toBe(false)
    expect(passesCompletedWindow({ issue: doneIssue }, 'none')).toBe(false)
  })

  it('formats Linear\'s short relative time', () => {
    const now = Date.parse('2026-10-07T12:00:00Z')
    expect(shortRelativeTime('2026-10-07T11:59:40Z', now)).toBe('now')
    expect(shortRelativeTime('2026-10-07T11:55:00Z', now)).toBe('5min')
    expect(shortRelativeTime('2026-10-07T07:00:00Z', now)).toBe('5h')
    expect(shortRelativeTime('2026-10-04T12:00:00Z', now)).toBe('3d')
    expect(shortRelativeTime('2026-09-23T12:00:00Z', now)).toBe('2w')
  })

  it('routes new issues to the default team\'s triage / backlog state', () => {
    const team2 = { id: 'team-2', key: 'TWO', name: 'Two', color: '#000' }
    const routed = fixture({ teams: [data.teams[0], team2], states: [backlog, { ...backlog, id: 'state-two-backlog', teamId: 'team-2' }], workspaceSettings: { ...data.workspaceSettings, featureSettings: { ...data.workspaceSettings.featureSettings, customerDefaultTeamId: 'team-2' } } } as never)
    expect(customerRequestIssueTeam(routed)?.id).toBe('team-2')
    expect(customerRequestIssueState(routed, 'team-2')?.id).toBe('state-two-backlog')
  })
})

describe('CustomerDetailPage (Linear customer page)', () => {
  it('renders the breadcrumb header, identity and the property row', () => {
    renderPage()
    const header = document.querySelector('.customer-page__header') as HTMLElement
    expect(within(header).getByRole('link', { name: 'Customers' })).toHaveAttribute('href', '/workspace/customers')
    expect(within(header).getByRole('switch', { name: 'Add to favorites' })).toBeVisible()
    expect(within(header).getByRole('button', { name: 'Open customer menu' })).toBeVisible()
    expect(within(header).getByRole('button', { name: 'Copy page URL' })).toBeVisible()
    expect(within(header).getByRole('button', { name: 'Setup customer notifications' })).toBeVisible()
    expect(screen.getByRole('heading', { level: 1, name: 'Acme' })).toBeVisible()
    expect(screen.getByText('acme.test +1')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Edit customer' })).toBeInTheDocument()
    const labels = [...document.querySelectorAll('.customer-top__label')].map(node => node.textContent)
    expect(labels).toEqual(['Status', 'Tier', 'Revenue', 'Size', 'Owner'])
    expect(document.querySelector('.customer-top__suffix')).toHaveTextContent('/yr')
    expect(screen.getByText('240')).toBeVisible()
  })

  it('lists one row per issue with request count, important badge and hides archived requests', () => {
    renderPage()
    const rows = screen.getAllByRole('listitem')
    expect(rows.map(row => row.querySelector('.customer-need__title')?.textContent)).toEqual(['Test issue', 'Shipped thing'])
    expect(within(rows[0]).getByText('2')).toBeVisible()
    expect(rows[0].querySelector('.customer-need__important')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Show 1 archived request' })).toBeVisible()
    expect(document.querySelector('.customer-needs__count')).toHaveTextContent('2')
  })

  it('expands a row into its requests and shows archived requests on demand', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getAllByRole('listitem')[0].querySelector('.customer-need__row') as HTMLElement)
    expect(screen.getByText('We need SSO')).toBeVisible()
    expect(screen.getByText('And SCIM')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Show 1 archived request' }))
    expect(screen.getAllByRole('listitem')).toHaveLength(3)
    expect(screen.getByText('Archived')).toBeVisible()
  })

  it('shows Linear\'s empty state with the add-request shortcut', () => {
    renderPage(fixture({ customerRequests: [] }))
    expect(screen.getByText('Customer requests')).toBeVisible()
    expect(screen.getByText('No customer requests created yet. Use a supported integration to automatically create requests, or create one manually.')).toBeVisible()
    expect(screen.getByRole('link', { name: 'Documentation' })).toHaveAttribute('href', 'https://flow.app/docs/customer-requests')
    expect(document.querySelectorAll('.customer-needs__keys kbd').length).toBeGreaterThan(1)
  })

  it('adds a request on a new issue routed to the default team backlog', async () => {
    const user = userEvent.setup()
    api.createIssue.mockResolvedValue({ ...makeIssue({ id: 'issue-new', identifier: 'TST-9', title: 'Customer request from Acme' }) })
    api.createCustomerRequest.mockResolvedValue(need({ id: 'need-new', issueId: 'issue-new' }))
    const { onReload } = renderPage()
    await user.click(screen.getByRole('button', { name: 'Add request' }))
    expect(screen.getByText('This request will be added to')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Search issues or projects…' })).toHaveTextContent('Customer request from Acme')
    await user.type(screen.getByRole('textbox', { name: 'Note' }), 'Needs audit logs')
    await user.keyboard('{Control>}{Enter}{/Control}')
    await waitFor(() => expect(api.createCustomerRequest).toHaveBeenCalled())
    expect(api.createIssue).toHaveBeenCalledWith(expect.objectContaining({ title: 'Customer request from Acme', teamId: 'team-1', stateId: 'state-backlog', priority: 0 }))
    expect(api.createCustomerRequest).toHaveBeenCalledWith(expect.objectContaining({ customerId: 'customer-1', body: 'Needs audit logs', issueId: 'issue-new' }))
    await waitFor(() => expect(onReload).toHaveBeenCalledWith(['issue-new']))
  })

  it('requires details or a source when the request creates a new issue', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('button', { name: 'Add request' }))
    await user.click(within(document.querySelector('form.customer-need-composer') as HTMLElement).getByRole('button', { name: 'Add request' }))
    expect(api.createIssue).not.toHaveBeenCalled()
    expect(api.createCustomerRequest).not.toHaveBeenCalled()
  })

  it('opens the composer with Ctrl R', () => {
    renderPage()
    act(() => { fireEvent.keyDown(window, { key: 'r', code: 'KeyR', ctrlKey: true, altKey: !/Mac/.test(navigator.platform) }) })
    expect(document.querySelector('form.customer-need-composer')).not.toBeNull()
  })

  it('fetches issues the paged bootstrap does not hold, by id', async () => {
    const data = fixture({ issues: [] })
    api.listIssueRecords.mockResolvedValue({ items: [makeIssue(), doneIssue], hasMore: false, total: 2 })
    renderPage(data)
    await waitFor(() => expect(screen.getByText('Test issue')).toBeVisible())
    expect(api.listIssueRecords).toHaveBeenCalledWith(expect.objectContaining({ filter: { field: 'id', operator: 'in', values: ['issue-1', 'issue-2'] }, archived: 'all' }))
  })
})

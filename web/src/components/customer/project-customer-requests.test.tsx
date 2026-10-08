import { act, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrap } from '@/test/fixtures'
import type { Customer, CustomerRequest, Issue, Project } from '@/types/flow'
import { ProjectCustomerRequestsLauncher, ProjectCustomerRequestsPage, ProjectCustomersRow } from './project-customer-requests'
import { requestProjectCustomerRequest } from './customer-request-events'

vi.mock('@/lib/api', () => ({ createCustomerRequest: vi.fn(), updateCustomerRequest: vi.fn(), deleteCustomerRequest: vi.fn(), uploadCustomerRequestAttachment: vi.fn(), createIssue: vi.fn(), listIssueRecords: vi.fn().mockResolvedValue({ items: [] }) }))
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
Element.prototype.scrollIntoView ??= function scrollIntoView() {}

const customers = ['Acme', 'Globex', 'Initech', 'Hooli', 'Umbrella'].map((name, index) => ({ id: `c${index}`, name, status: 'active', domains: [], annualRevenue: 1000 - index, createdAt: '', updatedAt: '' })) as Customer[]
const request = (id: string, customerId: string, extra: Partial<CustomerRequest> = {}) => ({ id, customerId, body: `Need ${id}`, source: 'manual', attachments: [], createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z', ...extra }) as CustomerRequest

function setup(requests: CustomerRequest[]) {
  const data = makeBootstrap({ customers, customerRequests: requests })
  const project = { ...data.projects[0], id: 'project-1', slugId: 'project-one', name: 'Project one' } as Project
  return { data, project }
}
const wrap = (node: React.ReactNode) => <MemoryRouter><I18nProvider><TooltipProvider>{node}</TooltipProvider></I18nProvider></MemoryRouter>

describe('project customer requests', () => {
  it('shows "Add customer request" on the overview while the project has none', () => {
    const { data, project } = setup([])
    render(wrap(<ProjectCustomersRow data={data} project={project} issueIds={new Set()}/>))
    expect(screen.getByRole('heading', { name: 'Customers' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add customer request' })).toBeInTheDocument()
  })

  it('summarizes the customers of the project and its issues, linking to the Customers tab', () => {
    const { data, project } = setup([
      request('r1', 'c0', { projectId: 'project-1' }),
      request('r2', 'c1', { issueId: 'issue-in-project' }),
      request('r3', 'c2', { projectId: 'project-1' }),
      request('r4', 'c3', { projectId: 'project-1' }),
      request('r5', 'c4', { projectId: 'project-1' }),
      request('r6', 'c4', { projectId: 'other-project' }),
    ])
    render(wrap(<ProjectCustomersRow data={data} project={project} issueIds={new Set(['issue-in-project'])}/>))
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', expect.stringContaining('/project/project-one/requests'))
    expect(link).toHaveTextContent('Acme, Globex, Initech and 2 others')
    expect(screen.getByRole('button', { name: 'Add customer need' })).toBeInTheDocument()
  })

  it('renders Linear’s empty Customers tab', () => {
    const { data, project } = setup([])
    render(wrap(<ProjectCustomerRequestsPage data={data} project={project} issues={[]}/>))
    expect(screen.getByText('Customer requests')).toBeInTheDocument()
    expect(screen.getByText(/No customer requests created yet/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add customer request to project' })).toHaveTextContent('Add request')
    expect(screen.getByRole('link', { name: 'Documentation' })).toHaveAttribute('target', '_blank')
  })

  it('lists one row per customer with an issue link for requests on the project’s issues', () => {
    const issue = { id: 'issue-in-project', identifier: 'ENG-7', title: 'SSO', state: { id: 's', name: 'Todo', color: '#ccc', type: 'unstarted' } } as unknown as Issue
    const { data, project } = setup([request('r1', 'c0', { projectId: 'project-1', priority: 1 }), request('r2', 'c1', { issueId: 'issue-in-project' })])
    render(wrap(<ProjectCustomerRequestsPage data={data} project={project} issues={[issue]}/>))
    const rows = screen.getAllByRole('group')
    expect(rows.map(row => within(row).getAllByText(/Acme|Globex/)[0].textContent)).toEqual(['Acme', 'Globex'])
    expect(document.querySelector('.project-customer-requests__count')).toHaveTextContent('2')
    expect(document.querySelector('.project-customer-requests__important')).toHaveTextContent('1')
  })

  it('starts a request from ⌘K: "Select customer…" then the Customers tab', async () => {
    const { data, project } = setup([])
    const onOpenRequests = vi.fn()
    render(wrap(<ProjectCustomerRequestsLauncher data={data} project={project} onOpenRequests={onOpenRequests}/>))
    let handled = false
    act(() => { handled = requestProjectCustomerRequest('project-1') })
    expect(handled).toBe(true)
    expect(await screen.findByPlaceholderText('Select customer…')).toBeInTheDocument()
  })
})

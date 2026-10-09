import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { TooltipProvider } from '@/components/ui/tooltip'
import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeIssue, viewer } from '@/test/fixtures'
import type { BootstrapData, Customer, CustomerRequest } from '@/types/flow'
import { requestSubtitle } from './customer-request-model'
import { EmbeddedCustomerNeedRow } from './embedded-customer-need-row'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn().mockResolvedValue({ items: [] }), createCustomerRequest: vi.fn(), updateCustomerRequest: vi.fn(), deleteCustomerRequest: vi.fn(), uploadCustomerRequestAttachment: vi.fn(), createIssue: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver

const customer = { id: 'customer-1234567890ab', name: 'Acme Corp', status: 'active', domains: [], createdAt: '', updatedAt: '' } as unknown as Customer
const need = (body: string, id = 'r1') => ({ id, customerId: customer.id, body, source: 'manual', attachments: [], creator: viewer, createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z' }) as CustomerRequest

function renderRow(data: BootstrapData, requests: CustomerRequest[], defaultExpanded = true) {
  const [primary, ...additional] = requests
  return render(<I18nProvider><MemoryRouter><TooltipProvider><WorkspaceStoreProvider account={null} data={data} session={null}>
    <EmbeddedCustomerNeedRow data={data} group={{ key: 'k', primary, additional, customer }} variant="customerPage" defaultExpanded={defaultExpanded}/>
  </WorkspaceStoreProvider></TooltipProvider></MemoryRouter></I18nProvider>)
}

beforeEach(() => {
  for (const mock of Object.values(api)) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [] })
  resetAgentRecordCache()
})

describe('customer request bodies', () => {
  it('renders issue, project, document and person references as chips and keeps line breaks', async () => {
    const { container } = renderRow(mentionFixture(), [need(`Needs [TST-1](${mentionUrls.issue}) for [Project one](${mentionUrls.project})\nSee [Launch plan](${mentionUrls.document}) cc @viewer`)])
    const body = container.querySelector('.customer-request-row__body') as HTMLElement
    await waitFor(() => expect(body.querySelector('a[data-agent-entity="issue"]')).toHaveTextContent('TST-1 Test issue'))
    expect(body.querySelector('a[data-agent-entity="issue"]')).toHaveAttribute('href', mentionUrls.issue)
    expect(body.querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one')
    expect(body.querySelector('a[data-agent-entity="document"]')).toHaveAttribute('href', mentionUrls.document)
    expect(body.querySelector('a[data-agent-entity="user"]')).toHaveTextContent('@Viewer')
    expect(body.textContent).not.toContain('](')
    expect(body.textContent).toContain('cc @Viewer')
  })

  it('fetches an issue the paged client does not hold', async () => {
    api.fetchIssueRecord.mockResolvedValue(makeIssue({ id: '0a1b2c3d-1111-2222-3333-444455556666', identifier: 'TST-9', title: 'Remote issue' }))
    const { container } = renderRow(mentionFixture({ issueCollectionPaged: true, issues: [], projects: [] }), [need('Blocked by [TST-9](/workspace/issue/TST-9/remote)')])
    await waitFor(() => expect(container.querySelector('.customer-request-row__body a[data-agent-entity="issue"]')).toHaveTextContent('TST-9 Remote issue'))
    expect(api.fetchIssueRecord).toHaveBeenCalledTimes(1)
  })

  it('shows an inaccessible issue with its stored label, marked unavailable', async () => {
    api.fetchIssueRecord.mockRejectedValue(new Error('forbidden'))
    const { container } = renderRow(mentionFixture({ issueCollectionPaged: true, issues: [], projects: [] }), [need('See [TST-404](/workspace/issue/TST-404/gone)')])
    await waitFor(() => expect(container.querySelector('.customer-request-row__body a[data-agent-entity="issue"]')).toHaveAttribute('data-mention-state', 'missing'))
    expect(container.querySelector('.customer-request-row__body a[data-agent-entity="issue"]')).toHaveTextContent('TST-404')
  })

  it('previews a collapsed request as plain text, never as link syntax', () => {
    const body = `Needs [TST-1](${mentionUrls.issue}) and **bold**`
    expect(requestSubtitle({ body, sourceUrl: undefined })).toBe('Needs TST-1 and bold')
    renderRow(mentionFixture(), [need(body)], false)
    expect(screen.getByText('Needs TST-1 and bold')).toHaveClass('customer-request-row__preview')
  })
})

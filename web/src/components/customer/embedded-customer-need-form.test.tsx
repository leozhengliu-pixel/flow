import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { EmbeddedCustomerNeedForm } from './embedded-customer-need-form'
import { importantFromPriority, priorityFromImportant } from './customer-request-model'
import type { BootstrapData, CustomerRequest } from '@/types/flow'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrap } from '@/test/fixtures'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'

const createCustomerRequest = vi.fn()
const updateCustomerRequest = vi.fn()
const uploadCustomerRequestAttachment = vi.fn()
const confirmAction = vi.fn()
const toastError = vi.fn()

vi.mock('@/lib/api', () => ({
  fetchIssueRecord: vi.fn(),
  listProjectRecords: vi.fn(),
  listIssueRecords: vi.fn().mockResolvedValue({ items: [], hasMore: false, total: 0 }),
  realtimeClientId: () => 'need-form-test',
  createCustomerRequest: (...args: unknown[]) => createCustomerRequest(...args),
  updateCustomerRequest: (...args: unknown[]) => updateCustomerRequest(...args),
  uploadCustomerRequestAttachment: (...args: unknown[]) => uploadCustomerRequestAttachment(...args),
}))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: (...args: unknown[]) => confirmAction(...args) }))
vi.mock('sonner', () => ({ toast: { error: (...args: unknown[]) => toastError(...args), success: vi.fn() } }))

const data = {
  customers: [
    { id: 'c1', name: 'Acme', status: 'Active', domains: ['acme.com'] },
    { id: 'c2', name: 'Globex', status: 'Active', domains: [] },
  ],
  viewer: { id: 'u1', name: 'Ada', displayName: 'Ada', email: 'a@b.c', active: true },
} as unknown as BootstrapData

const created = { id: 'r1', customerId: 'c1', body: 'Need', source: 'manual', attachments: [], createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z' }

function renderForm(props: Partial<Parameters<typeof EmbeddedCustomerNeedForm>[0]> = {}) {
  return render(<MentionShell data={makeBootstrap()}><I18nProvider><TooltipProvider><EmbeddedCustomerNeedForm data={data} host="issuePage" issueId="issue-1" {...props}/></TooltipProvider></I18nProvider></MentionShell>)
}

describe('EmbeddedCustomerNeedForm', () => {
  afterEach(() => { vi.unstubAllGlobals() })
  beforeEach(() => {
    stubEditorEnvironment()
    vi.clearAllMocks()
    createCustomerRequest.mockResolvedValue(created)
    updateCustomerRequest.mockResolvedValue({ ...created, body: 'Edited' })
  })

  it('renders Linear’s composer: customer button, request text, Source, attach, Cancel and Create', async () => {
    renderForm({ initialCustomerId: 'c1', onCancel: vi.fn() })
    expect(screen.getByRole('button', { name: 'Search customers' })).toHaveTextContent('Acme')
    const body = await screen.findByRole('textbox', { name: 'Request' })
    expect(document.querySelector('[data-placeholder="Add request details"]')).not.toBeNull()
    await waitFor(() => expect(body).toHaveFocus())
    expect(screen.getByRole('button', { name: 'Add source' })).toHaveTextContent('Source')
    expect(screen.getByRole('button', { name: 'Attach images, files, or videos' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Discard' })).toHaveTextContent('Cancel')
    expect(screen.getByRole('button', { name: 'Create' })).toHaveAttribute('type', 'submit')
  })

  it('creates the request for the picked customer with ⌘↵', async () => {
    const user = userEvent.setup()
    const onCreated = vi.fn()
    renderForm({ initialCustomerId: 'c1', onCreated })
    await user.click(await screen.findByRole('textbox', { name: 'Request' }))
    await user.keyboard('Need SSO{Meta>}{Enter}{/Meta}')
    await waitFor(() => expect(createCustomerRequest).toHaveBeenCalledWith({
      customerId: 'c1',
      customerName: undefined,
      body: 'Need SSO',
      source: 'manual',
      sourceUrl: undefined,
      issueId: 'issue-1',
      projectId: undefined,
    }))
    expect(onCreated).toHaveBeenCalledWith(created)
  })

  it('creates a typed customer together with the request (deferred creation)', async () => {
    renderForm({ pendingCustomerName: 'Initech' })
    expect(screen.getByRole('button', { name: 'Search customers' })).toHaveTextContent('Initech')
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(createCustomerRequest).toHaveBeenCalledWith(expect.objectContaining({ customerId: undefined, customerName: 'Initech', body: '' })))
  })

  it('needs a customer, a request or a source', async () => {
    renderForm()
    expect(screen.getByRole('button', { name: 'Search customers' })).toHaveTextContent('Customer')
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Please select a customer, provide a request, or specify a source.'))
    expect(createCustomerRequest).not.toHaveBeenCalled()
  })

  it('creates an Unknown customer request from text alone, for a project', async () => {
    const user = userEvent.setup()
    renderForm({ issueId: undefined, projectId: 'project-1', host: 'projectPage' })
    await user.click(await screen.findByRole('textbox', { name: 'Request' }))
    await user.keyboard('From a call')
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(createCustomerRequest).toHaveBeenCalledWith(expect.objectContaining({ customerId: undefined, customerName: undefined, body: 'From a call', projectId: 'project-1', issueId: undefined })))
  })

  it('adds a source link from the Source popover', async () => {
    const user = userEvent.setup()
    renderForm({ initialCustomerId: 'c1' })
    await user.click(screen.getByRole('button', { name: 'Add source' }))
    const input = await screen.findByRole('textbox', { name: 'Source URL' })
    expect(input).toHaveAttribute('placeholder', 'Paste link…')
    await user.type(input, 'support.acme.com/t/1{Enter}')
    expect(screen.getByRole('button', { name: 'Add source' })).toHaveTextContent('via support.acme.com')
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(createCustomerRequest).toHaveBeenCalledWith(expect.objectContaining({ sourceUrl: 'https://support.acme.com/t/1' })))
  })

  it('discards without asking when nothing changed and asks once something was typed', async () => {
    const user = userEvent.setup()
    const onCancel = vi.fn()
    renderForm({ initialCustomerId: 'c1', onCancel })
    const body = await screen.findByRole('textbox', { name: 'Request' })
    await user.click(body)
    await user.keyboard('{Escape}')
    await waitFor(() => expect(onCancel).toHaveBeenCalledTimes(1))
    expect(confirmAction).not.toHaveBeenCalled()

    confirmAction.mockResolvedValue(false)
    await user.click(body)
    await user.keyboard('Draft')
    fireEvent.click(screen.getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(confirmAction).toHaveBeenCalledWith('Discard this request?', expect.objectContaining({ description: 'Confirm that you want to discard this customer request.', confirmLabel: 'Discard' })))
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('searches customers and offers Create new customer for typed text', async () => {
    const user = userEvent.setup()
    renderForm()
    await user.click(screen.getByRole('button', { name: 'Search customers' }))
    const search = await screen.findByRole('textbox', { name: 'Search customers…' })
    expect(screen.getByRole('menuitem', { name: /Acme/ })).toBeInTheDocument()
    await user.type(search, 'Hooli')
    await user.click(await screen.findByRole('menuitem', { name: 'Create new customer: "Hooli"' }))
    expect(screen.getByRole('button', { name: 'Search customers' })).toHaveTextContent('Hooli')
  })

  it('edits an existing request without a customer button and saves', async () => {
    const user = userEvent.setup()
    const onSaved = vi.fn()
    const request = { ...created, body: "Old text", sourceUrl: "" } as unknown as CustomerRequest
    renderForm({ request, onSaved, onCancel: vi.fn() })
    expect(screen.queryByRole('button', { name: 'Search customers' })).toBeNull()
    const body = await screen.findByRole('textbox', { name: 'Request' })
    expect(body).toHaveTextContent('Old text')
    await user.click(body)
    await user.keyboard('{Control>}a{/Control}Edited')
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(updateCustomerRequest).toHaveBeenCalledWith('r1', { body: 'Edited', sourceUrl: '' }))
    expect(onSaved).toHaveBeenCalled()
  })

  it('saves a mention as a markdown link, turns a pasted Flow URL into a chip, and shows the chip when the request is edited', async () => {
    const user = userEvent.setup()
    const form = (props: Partial<Parameters<typeof EmbeddedCustomerNeedForm>[0]> = {}) => <MentionShell data={mentionFixture()}><I18nProvider><TooltipProvider><EmbeddedCustomerNeedForm data={data} host="issuePage" issueId="issue-1" initialCustomerId="c1" {...props}/></TooltipProvider></I18nProvider></MentionShell>
    const first = render(form())
    const box = await screen.findByRole('textbox', { name: 'Request' })
    await user.click(box)
    await user.keyboard('Needs @Launch')
    await user.click(await screen.findByRole('option', { name: /Launch plan/ }))
    pasteText(box, `${window.location.origin}${mentionUrls.project}`)
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one'))
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(createCustomerRequest).toHaveBeenCalledTimes(1))
    const saved = createCustomerRequest.mock.calls[0][0].body as string
    expect(saved).toContain('[Launch plan](/workspace/document/plan-abc)')
    expect(saved).toContain('[Project one](/workspace/project/project-one/overview)')
    first.unmount()
    render(form({ request: { ...created, body: saved } as unknown as CustomerRequest, onCancel: vi.fn() }))
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
  })

  it('maps Important ↔ priority helpers', () => {
    expect(importantFromPriority(1)).toBe(true)
    expect(importantFromPriority(0)).toBe(false)
    expect(priorityFromImportant(true)).toBe(1)
    expect(priorityFromImportant(false)).toBeUndefined()
  })
})

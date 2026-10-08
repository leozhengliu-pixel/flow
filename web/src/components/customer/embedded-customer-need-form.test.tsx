import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  EmbeddedCustomerNeedForm,
  importantFromPriority,
  priorityFromImportant,
} from './embedded-customer-need-form'
import type { BootstrapData, CustomerRequest } from '@/types/flow'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'

const createCustomerRequest = vi.fn()
const updateCustomerRequest = vi.fn()
const uploadCustomerRequestAttachment = vi.fn()
const confirmAction = vi.fn()
const toastError = vi.fn()

vi.mock('@/lib/api', () => ({
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
  return render(<I18nProvider><TooltipProvider><EmbeddedCustomerNeedForm data={data} host="issuePage" issueId="issue-1" {...props}/></TooltipProvider></I18nProvider>)
}

describe('EmbeddedCustomerNeedForm', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    createCustomerRequest.mockResolvedValue(created)
    updateCustomerRequest.mockResolvedValue({ ...created, body: 'Edited' })
  })

  it('renders Linear’s composer: customer button, request text, Source, attach, Cancel and Create', () => {
    renderForm({ initialCustomerId: 'c1', onCancel: vi.fn() })
    expect(screen.getByRole('button', { name: 'Search customers' })).toHaveTextContent('Acme')
    const body = screen.getByRole('textbox', { name: 'Request' })
    expect(body).toHaveAttribute('placeholder', 'Add request details')
    expect(body).toHaveFocus()
    expect(screen.getByRole('button', { name: 'Add source' })).toHaveTextContent('Source')
    expect(screen.getByRole('button', { name: 'Attach images, files, or videos' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Discard' })).toHaveTextContent('Cancel')
    expect(screen.getByRole('button', { name: 'Create' })).toHaveAttribute('type', 'submit')
  })

  it('creates the request for the picked customer with ⌘↵', async () => {
    const onCreated = vi.fn()
    renderForm({ initialCustomerId: 'c1', onCreated })
    const body = screen.getByRole('textbox', { name: 'Request' })
    fireEvent.change(body, { target: { value: '  Need SSO  ' } })
    fireEvent.keyDown(body, { key: 'Enter', metaKey: true })
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
    renderForm({ issueId: undefined, projectId: 'project-1', host: 'projectPage' })
    fireEvent.change(screen.getByRole('textbox', { name: 'Request' }), { target: { value: 'From a call' } })
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
    const onCancel = vi.fn()
    renderForm({ initialCustomerId: 'c1', onCancel })
    const body = screen.getByRole('textbox', { name: 'Request' })
    fireEvent.keyDown(body, { key: 'Escape' })
    await waitFor(() => expect(onCancel).toHaveBeenCalledTimes(1))
    expect(confirmAction).not.toHaveBeenCalled()

    confirmAction.mockResolvedValue(false)
    fireEvent.change(body, { target: { value: 'Draft' } })
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
    const onSaved = vi.fn()
    const request = { ...created, body: "Old text", sourceUrl: "" } as unknown as CustomerRequest
    renderForm({ request, onSaved, onCancel: vi.fn() })
    expect(screen.queryByRole('button', { name: 'Search customers' })).toBeNull()
    const body = screen.getByRole('textbox', { name: 'Request' })
    expect(body).toHaveValue('Old text')
    fireEvent.change(body, { target: { value: 'Edited' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(updateCustomerRequest).toHaveBeenCalledWith('r1', { body: 'Edited', sourceUrl: '' }))
    expect(onSaved).toHaveBeenCalled()
  })

  it('maps Important ↔ priority helpers', () => {
    expect(importantFromPriority(1)).toBe(true)
    expect(importantFromPriority(0)).toBe(false)
    expect(priorityFromImportant(true)).toBe(1)
    expect(priorityFromImportant(false)).toBeUndefined()
  })
})

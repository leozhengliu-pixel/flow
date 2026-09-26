import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  EmbeddedCustomerNeedForm,
  importantFromPriority,
  priorityFromImportant,
} from './embedded-customer-need-form'
import type { BootstrapData } from '@/types/flow'
import { I18nProvider } from '@/i18n/i18n'

const createCustomerRequest = vi.fn()
const createCustomer = vi.fn()

vi.mock('@/lib/api', () => ({
  createCustomerRequest: (...args: unknown[]) => createCustomerRequest(...args),
  createCustomer: (...args: unknown[]) => createCustomer(...args),
}))

vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))

const data = {
  customers: [{ id: 'c1', name: 'Acme', status: 'Active', domains: [] }],
  viewer: { id: 'u1', name: 'Ada', displayName: 'Ada', email: 'a@b.c', active: true },
} as unknown as BootstrapData

describe('EmbeddedCustomerNeedForm', () => {
  beforeEach(() => {
    createCustomerRequest.mockReset()
    createCustomer.mockReset()
    createCustomerRequest.mockResolvedValue({ id: 'r1', customerId: 'c1', body: 'Need', source: 'manual' })
  })

  it('creates a request from the customer pill and request text', async () => {
    const onCreated = vi.fn()
    render(
      <I18nProvider>
        <EmbeddedCustomerNeedForm
          data={data}
          host="issuePage"
          issueId="issue-1"
          onCreated={onCreated}
        />
      </I18nProvider>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Customer' }))
    fireEvent.click(await screen.findByRole('option', { name: /Acme/ }))
    fireEvent.change(screen.getByLabelText('Request'), { target: { value: 'Need SSO' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(createCustomerRequest).toHaveBeenCalled())
    expect(createCustomerRequest).toHaveBeenCalledWith({
      customerId: 'c1',
      body: 'Need SSO',
      source: 'manual',
      sourceUrl: undefined,
      issueId: 'issue-1',
      projectId: undefined,
    })
    expect(onCreated).toHaveBeenCalled()
  })

  it('maps Important ↔ priority helpers', () => {
    expect(importantFromPriority(1)).toBe(true)
    expect(importantFromPriority(0)).toBe(false)
    expect(priorityFromImportant(true)).toBe(1)
    expect(priorityFromImportant(false)).toBeUndefined()
  })
})

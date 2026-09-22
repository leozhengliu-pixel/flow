import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  EmbeddedCustomerNeedForm,
  importantFromPriority,
  priorityFromImportant,
} from './embedded-customer-need-form'
import type { BootstrapData } from '@/types/flow'

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

  it('submits sourceUrl and Important as priority on issue host', async () => {
    const onCreated = vi.fn()
    render(
      <EmbeddedCustomerNeedForm
        data={data}
        host="issuePage"
        issueId="issue-1"
        onCreated={onCreated}
      />,
    )
    fireEvent.change(screen.getByLabelText('Customer'), { target: { value: 'c1' } })
    fireEvent.change(screen.getByLabelText('Request details'), { target: { value: 'Need SSO' } })
    fireEvent.change(screen.getByLabelText('Source URL'), { target: { value: 'https://slack.example/msg' } })
    fireEvent.click(screen.getByRole('button', { name: 'Important' }))
    fireEvent.click(screen.getByRole('button', { name: 'Add request' }))
    await waitFor(() => expect(createCustomerRequest).toHaveBeenCalled())
    expect(createCustomerRequest).toHaveBeenCalledWith({
      customerId: 'c1',
      body: 'Need SSO',
      source: 'manual',
      sourceUrl: 'https://slack.example/msg',
      issueId: 'issue-1',
      projectId: undefined,
      priority: 1,
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

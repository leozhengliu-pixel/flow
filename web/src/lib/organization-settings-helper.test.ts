import { beforeEach, expect, it, vi } from 'vitest'
import { cancelOrganizationDeletion } from './organization-settings-helper'
import { cancelWorkspaceDeletion } from './api'

vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  cancelWorkspaceDeletion: vi.fn(),
}))

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

import { toast } from 'sonner'

beforeEach(() => {
  vi.mocked(cancelWorkspaceDeletion).mockReset()
  vi.mocked(toast.success).mockReset()
  vi.mocked(toast.error).mockReset()
})

it('toasts success when canceling scheduled deletion', async () => {
  vi.mocked(cancelWorkspaceDeletion).mockResolvedValueOnce({
    id: 'ws',
    name: 'Workspace',
    urlKey: 'workspace',
  })
  const onSuccess = vi.fn()
  await cancelOrganizationDeletion('workspace', { onSuccess })
  expect(cancelWorkspaceDeletion).toHaveBeenCalledWith('workspace')
  expect(toast.success).toHaveBeenCalledWith(
    'Workspace deletion has been canceled',
    expect.objectContaining({ description: expect.stringContaining('no longer scheduled') }),
  )
  expect(onSuccess).toHaveBeenCalled()
})

it('toasts error when cancel fails', async () => {
  vi.mocked(cancelWorkspaceDeletion).mockRejectedValueOnce(new Error('denied'))
  const onError = vi.fn()
  await cancelOrganizationDeletion('workspace', { onError })
  expect(toast.error).toHaveBeenCalledWith(
    'Workspace deletion could not be canceled',
    expect.objectContaining({ description: 'denied' }),
  )
  expect(onError).toHaveBeenCalled()
})

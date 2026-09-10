import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { toast } from 'sonner'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { deleteWorkspaceLabel } from '@/lib/api'
import { DomainLabelsSettings } from './domain-settings'

vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), deleteWorkspaceLabel: vi.fn() }))
vi.mock('sonner', () => ({ toast: { error: vi.fn() } }))
beforeEach(() => { vi.clearAllMocks(); localStorage.setItem('flow:locale', 'en-US') })

function page(onReload: () => Promise<void>) {
  return <I18nProvider><DomainLabelsSettings resourceType="project" data={makeBootstrap({
    labels: [{ id: 'delete-me', name: 'Disposable', color: '#aabbcc', scope: 'Workspace', resourceType: 'project' }], labelGroups: [],
  })} onReload={onReload}/></I18nProvider>
}

async function confirmDeletion() {
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Open Disposable menu' }))
  await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
  await user.click(await screen.findByRole('button', { name: /^Delete$/ }))
}

it('closes and removes the acknowledged deletion without waiting for bootstrap refresh', async () => {
  let finish!: () => void
  vi.mocked(deleteWorkspaceLabel).mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve }))
  const reload = vi.fn(() => new Promise<void>(() => {}))
  render(page(reload))
  await confirmDeletion()
  expect(screen.getByRole('button', { name: /^Delete$/ })).toBeDisabled()
  expect(reload).not.toHaveBeenCalled()
  await act(async () => finish())
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(screen.queryByRole('button', { name: 'Open Disposable menu' })).not.toBeInTheDocument()
  expect(reload).toHaveBeenCalledOnce()
})

it('keeps failed deletion open and retryable without hiding the label', async () => {
  vi.mocked(deleteWorkspaceLabel).mockRejectedValueOnce(new Error('Delete denied'))
  const reload = vi.fn().mockResolvedValue(undefined)
  render(page(reload))
  await confirmDeletion()
  await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Delete denied'))
  expect(screen.getByRole('dialog')).toBeVisible()
  expect(screen.getByRole('button', { name: /^Delete$/ })).toBeEnabled()
  expect(reload).not.toHaveBeenCalled()
  vi.mocked(deleteWorkspaceLabel).mockResolvedValueOnce(undefined)
  await userEvent.setup().click(screen.getByRole('button', { name: /^Delete$/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(deleteWorkspaceLabel).toHaveBeenCalledTimes(2)
})

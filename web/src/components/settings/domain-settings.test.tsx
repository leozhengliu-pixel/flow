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

it('renders initiative labels with the initiative resource and usage column', async () => {
  render(<I18nProvider><DomainLabelsSettings resourceType="initiative" data={makeBootstrap({
    labels: [{ id: 'initiative-label', name: 'Strategy', color: '#123456', scope: 'Workspace', resourceType: 'initiative', issueCount: 1 }],
    initiatives: [{ id: 'initiative-1', name: 'Launch', labelIds: ['initiative-label'] }],
    labelGroups: [],
  } as never)} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)

  expect(screen.getByRole('heading', { name: 'Initiative labels' })).toBeVisible()
  expect(screen.getByDisplayValue('Strategy')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Order by Initiatives' })).toBeVisible()
})

it('omits Move to group when an ungrouped label has no valid destinations', async () => {
  render(<I18nProvider><DomainLabelsSettings resourceType="initiative" data={makeBootstrap({
    labels: [{ id: 'strategy', name: 'Strategy', color: '#123456', resourceType: 'initiative' }],
    initiatives: [],
    labelGroups: [],
  } as never)} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)

  await userEvent.setup().click(screen.getByRole('button', { name: 'Open Strategy menu' }))
  expect(screen.queryByRole('menuitem', { name: 'Move to group' })).not.toBeInTheDocument()
})

it('shows Remove from group when a grouped label has no other destination', async () => {
  render(<I18nProvider><DomainLabelsSettings resourceType="initiative" data={makeBootstrap({
    labels: [{ id: 'strategy', name: 'Strategy', color: '#123456', resourceType: 'initiative', groupId: 'strategy-group' }],
    initiatives: [],
    labelGroups: [{ id: 'strategy-group', name: 'Strategy type', color: '#123456', resourceType: 'initiative', createdAt: '2026-01-01T00:00:00.000Z' }],
  } as never)} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)

  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Open Strategy menu' }))
  await user.hover(screen.getByRole('menuitem', { name: 'Move to group' }))
  expect(await screen.findByRole('menuitem', { name: 'Remove from group' })).toBeVisible()
  expect(screen.queryByRole('menuitem', { name: 'Strategy type' })).not.toBeInTheDocument()
})

it('only lists active destination groups from the same label resource', async () => {
  render(<I18nProvider><DomainLabelsSettings resourceType="initiative" data={makeBootstrap({
    labels: [{ id: 'strategy', name: 'Strategy', color: '#123456', resourceType: 'initiative', groupId: 'strategy-group' }],
    initiatives: [],
    labelGroups: [
      { id: 'strategy-group', name: 'Strategy type', color: '#123456', resourceType: 'initiative', createdAt: '2026-01-01T00:00:00.000Z' },
      { id: 'delivery-group', name: 'Delivery type', color: '#654321', resourceType: 'initiative', createdAt: '2026-01-01T00:00:00.000Z' },
      { id: 'archived-group', name: 'Archived group', color: '#111111', resourceType: 'initiative', createdAt: '2026-01-01T00:00:00.000Z', archivedAt: '2026-02-01T00:00:00.000Z' },
      { id: 'project-group', name: 'Project group', color: '#222222', resourceType: 'project', createdAt: '2026-01-01T00:00:00.000Z' },
    ],
  } as never)} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)

  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Open Strategy menu' }))
  await user.hover(screen.getByRole('menuitem', { name: 'Move to group' }))
  expect(await screen.findByRole('menuitem', { name: 'Delivery type' })).toBeVisible()
  expect(screen.getByRole('menuitem', { name: 'Remove from group' })).toBeVisible()
  expect(screen.queryByRole('menuitem', { name: 'Strategy type' })).not.toBeInTheDocument()
  expect(screen.queryByRole('menuitem', { name: 'Archived group' })).not.toBeInTheDocument()
  expect(screen.queryByRole('menuitem', { name: 'Project group' })).not.toBeInTheDocument()
})

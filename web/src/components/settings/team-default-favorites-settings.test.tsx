import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { fetchTeamDefaultFavorites, replaceTeamDefaultFavorites } from '@/lib/api'
import { makeBootstrap } from '@/test/fixtures'
import type { TeamDefaultFavorite } from '@/types/flow'
import { DefaultFavoritesSettings } from './team-default-favorites-settings'

vi.mock('@/lib/api', () => ({ fetchTeamDefaultFavorites: vi.fn(), replaceTeamDefaultFavorites: vi.fn() }))
vi.mock('sonner', () => ({ toast: { success: vi.fn() } }))

const data = makeBootstrap({ savedViews: [], documents: [] })
const favorite = (resourceType = 'issue', resourceId = 'issue-1') => ({
  id: `${resourceType}:${resourceId}`, resourceType, resourceId, workspaceKey: 'workspace', teamId: 'team-1', position: 0, createdAt: '', updatedAt: '',
}) satisfies TeamDefaultFavorite

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.setItem('flow:locale', 'en-US')
  vi.mocked(fetchTeamDefaultFavorites).mockResolvedValue({ items: [] })
  vi.mocked(replaceTeamDefaultFavorites).mockImplementation(async (_id, items) => ({ items: items.map(item => favorite(item.resourceType, item.resourceId)) }))
})
function setup() {
  return render(<I18nProvider><DefaultFavoritesSettings data={data} team={data.teams[0]} /></I18nProvider>)
}
async function addIssue() {
  await userEvent.click(await screen.findByRole('button', { name: 'Add favorite' }))
  await userEvent.click(await screen.findByRole('option', { name: /Test issue/ }))
}
async function rowMenu(title: string) {
  await userEvent.click(screen.getByRole('button', { name: `More options: ${title}` }))
}
async function manualDialog() {
  await userEvent.click(screen.getByRole('button', { name: 'More options' }))
  await userEvent.click(await screen.findByRole('menuitem', { name: 'Add by ID' }))
  return screen.findByRole('dialog', { name: 'Add by ID' })
}

describe('team default favorites settings', () => {
  it('has a single unframed resource list with no raw ID form or redundant save button', async () => {
    const view = setup()
    await screen.findByText('No default favorites configured.')
    expect(screen.queryByRole('heading', { name: 'Default favorites' })).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Resource ID')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Save changes' })).not.toBeInTheDocument()
    expect(view.container.querySelector('.settings-card')).toBeNull()
  })

  it('adds directly from the shared picker, stages changes, and saves only resource references', async () => {
    setup()
    await screen.findByText('No default favorites configured.')
    await addIssue()
    const row = within(screen.getByRole('list')).getByRole('listitem')
    expect(row).toHaveTextContent('Test issue')
    expect(row).toHaveTextContent('TST-1')
    expect(row.querySelector('.team-defaults-icon > svg')).not.toBeNull()
    expect(replaceTeamDefaultFavorites).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(replaceTeamDefaultFavorites).toHaveBeenCalledWith('team-1', [{ resourceType: 'issue', resourceId: 'issue-1' }]))
    await waitFor(() => expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument())
  })

  it('reorders and removes items locally and cancel restores the saved order', async () => {
    vi.mocked(fetchTeamDefaultFavorites).mockResolvedValue({ items: [favorite(), favorite('project', 'project-1')] })
    setup()
    await screen.findByText('Project one')
    await rowMenu('Project one')
    await userEvent.click(screen.getByRole('menuitem', { name: 'Move up' }))
    expect(screen.getAllByRole('listitem')[0]).toHaveTextContent('Project one')
    await rowMenu('Test issue')
    await userEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    expect(screen.queryByText('Test issue')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.getAllByRole('listitem')[0]).toHaveTextContent('Test issue')
    expect(replaceTeamDefaultFavorites).not.toHaveBeenCalled()
  })

  it('searches by issue identifier and allows keyboard selection', async () => {
    setup()
    await screen.findByText('No default favorites configured.')
    await userEvent.click(screen.getByRole('button', { name: 'Add favorite' }))
    const search = await screen.findByRole('textbox', { name: 'Search resources…' })
    await userEvent.type(search, 'TST-1')
    expect(screen.getByRole('option', { name: /Test issue\s*TST-1/ })).toBeInTheDocument()
    await userEvent.keyboard('{Enter}')
    expect(within(screen.getByRole('list')).getByRole('listitem')).toHaveTextContent('Test issue')
    expect(replaceTeamDefaultFavorites).not.toHaveBeenCalled()
  })

  it('does not allow a failed load to overwrite existing configuration and supports retry', async () => {
    vi.mocked(fetchTeamDefaultFavorites).mockRejectedValueOnce(new Error('Unavailable'))
    setup()
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load default favorites')
    expect(screen.getByRole('button', { name: 'Add favorite' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'More options' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Save changes' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
    await screen.findByText('No default favorites configured.')
    expect(screen.getByRole('button', { name: 'Add favorite' })).toBeEnabled()
  })

  it('keeps edits after save failure and locks controls during an in-flight save', async () => {
    setup()
    await screen.findByText('No default favorites configured.')
    await addIssue()
    vi.mocked(replaceTeamDefaultFavorites).mockRejectedValueOnce(new Error('Save failed'))
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Save failed')
    let complete!: (result: { items: TeamDefaultFavorite[] }) => void
    vi.mocked(replaceTeamDefaultFavorites).mockImplementationOnce(() => new Promise(resolve => { complete = resolve }))
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(screen.getByRole('button', { name: 'Saving…' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Add favorite' })).toBeDisabled()
    await act(async () => complete({ items: [favorite()] }))
    expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument()
  })

  it('retains a secondary ID dialog with duplicate protection and keyboard submission', async () => {
    vi.mocked(fetchTeamDefaultFavorites).mockResolvedValue({ items: [favorite()] })
    setup()
    await screen.findByText('Test issue')
    const dialog = await manualDialog()
    const input = within(dialog).getByRole('textbox', { name: 'Resource ID' })
    await userEvent.type(input, 'issue-1')
    expect(within(dialog).getByRole('alert')).toHaveTextContent('This resource is already added.')
    expect(within(dialog).getByRole('button', { name: 'Add favorite' })).toBeDisabled()
    await userEvent.clear(input)
    await userEvent.type(input, 'unloaded:issue{Enter}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.getByText('unloaded:issue')).toBeInTheDocument()
    expect(replaceTeamDefaultFavorites).not.toHaveBeenCalled()
  })

  it('resets local changes when switching teams and ignores an old pending load', async () => {
    let finishOld!: (result: { items: TeamDefaultFavorite[] }) => void
    vi.mocked(fetchTeamDefaultFavorites).mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve }))
    const view = setup()
    const nextTeam = { ...data.teams[0], id: 'team-2', name: 'Second team' }
    view.rerender(<I18nProvider><DefaultFavoritesSettings data={data} team={nextTeam} /></I18nProvider>)
    await screen.findByText('No default favorites configured.')
    await act(async () => finishOld({ items: [favorite()] }))
    expect(screen.queryByText('Test issue')).not.toBeInTheDocument()
    expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument()
  })

  it('disables duplicate choices and enforces the server limit before adding', async () => {
    vi.mocked(fetchTeamDefaultFavorites).mockResolvedValue({ items: [favorite()] })
    const view = setup()
    await screen.findByText('Test issue')
    await userEvent.click(screen.getByRole('button', { name: 'Add favorite' }))
    expect(await screen.findByRole('option', { name: /Test issue/ })).toHaveAttribute('aria-disabled', 'true')
    view.unmount()
    vi.mocked(fetchTeamDefaultFavorites).mockResolvedValue({ items: Array.from({ length: 100 }, (_, i) => favorite('issue', `issue-${i}`)) })
    setup()
    await screen.findByText('Up to 100 default favorites are allowed.')
    expect(screen.getByRole('button', { name: 'Add favorite' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'More options' })).toBeDisabled()
  })

  it('renders Chinese controls without translating resource names', async () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    vi.mocked(fetchTeamDefaultFavorites).mockResolvedValue({ items: [favorite()] })
    setup()
    await screen.findByText('Test issue')
    expect(screen.getByRole('button', { name: '添加收藏' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /资源/ })).toBeInTheDocument()
  })
})

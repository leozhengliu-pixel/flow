import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { SidebarTeamMenu } from './sidebar-team-menu'
import type { BootstrapData } from '@/types/flow'

const mocks = vi.hoisted(() => ({ toggle: vi.fn(), subscribe: vi.fn(), unsubscribe: vi.fn(), membership: vi.fn(), refresh: vi.fn(), confirm: vi.fn(), error: vi.fn(), success: vi.fn() }))
vi.mock('@/lib/favorites', () => ({ toggleFavoriteFor: mocks.toggle }))
vi.mock('@/lib/api', () => ({ addSubscription: mocks.subscribe, removeSubscription: mocks.unsubscribe, setTeamMembership: mocks.membership }))
vi.mock('@/lib/resource-preferences', () => ({ refreshResourcePreferences: mocks.refresh }))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: mocks.confirm }))
vi.mock('sonner', () => ({ toast: { error: mocks.error, success: mocks.success } }))

function fixture() {
  const data = makeBootstrap({ favorites: [], subscriptions: [], teamSettings: {} })
  data.teams = [data.teams[0], { ...data.teams[0], id: 'team-2', key: 'TWO' }]
  data.teamMembers = data.teams.map(team => ({ teamId: team.id, userId: viewer.id, role: 'member', joinedAt: '' }))
  return data
}
function tree(data = fixture(), reload = vi.fn().mockResolvedValue(undefined)) {
  return <MemoryRouter><I18nProvider><SidebarTeamMenu data={data} team={data.teams[0]} onReload={reload}><button>Team menu</button></SidebarTeamMenu></I18nProvider></MemoryRouter>
}
beforeEach(() => { vi.resetAllMocks(); vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })); localStorage.setItem('flow:locale', 'en-US'); mocks.toggle.mockResolvedValue(undefined); mocks.refresh.mockResolvedValue(undefined); mocks.subscribe.mockResolvedValue(undefined); mocks.membership.mockResolvedValue(undefined) })
afterEach(() => { localStorage.removeItem('flow:locale') })

it('does not mount selectable items during the opening pointer gesture', async () => {
  const user = userEvent.setup()
  render(tree())
  const trigger = screen.getByRole('button', { name: 'Team menu' })
  await user.pointer({ target: trigger, keys: '[MouseLeft>]' })
  expect(screen.queryByRole('menu')).toBeNull()
  await user.pointer({ target: trigger, keys: '[/MouseLeft]' })
  expect(screen.getByRole('menuitem', { name: /Favorite/ })).toBeVisible()
  expect(mocks.toggle).not.toHaveBeenCalled()
  await user.click(screen.getByRole('menuitem', { name: /Favorite/ }))
  expect(mocks.toggle).toHaveBeenCalledTimes(1)
})

it('tracks external favorite changes and rollback without a second local state', async () => {
  const data = fixture(), user = userEvent.setup()
  const view = render(tree(data))
  await user.click(screen.getByRole('button', { name: 'Team menu' }))
  expect(screen.getByRole('menuitem', { name: /^Favorite/ })).toBeVisible()
  view.rerender(tree({ ...data, favorites: [{ id: 'favorite', userId: viewer.id, resourceType: 'team', resourceId: data.teams[0].id, position: 0, createdAt: '' }] }))
  expect(screen.getByRole('menuitem', { name: /^Unfavorite/ })).toBeVisible()
  view.rerender(tree(data))
  expect(screen.getByRole('menuitem', { name: /^Favorite/ })).toBeVisible()
})

it('shows typed filtering, omits unrelated groups, handles empty results and restores focus', async () => {
  const user = userEvent.setup()
  render(tree())
  const trigger = screen.getByRole('button', { name: 'Team menu' })
  await user.click(trigger)
  const input = screen.getByRole('textbox', { name: 'Filter…' })
  await user.type(input, 'archive')
  expect(input.parentElement).toHaveAttribute('data-searching', 'true')
  expect(screen.getAllByRole('menuitem')).toHaveLength(1)
  expect(screen.queryByRole('separator')).toBeNull()
  await user.clear(input)
  await user.type(input, 'zzzz')
  expect(screen.getByRole('status')).toHaveTextContent('No results')
  await user.keyboard('{Escape}')
  await waitFor(() => expect(trigger).toHaveFocus())
  await user.keyboard('{Enter}')
  expect(mocks.toggle).not.toHaveBeenCalled()
})

it('filters using the rendered Chinese action name', async () => {
  localStorage.setItem('flow:locale', 'zh-CN')
  const user = userEvent.setup()
  render(tree())
  await user.click(screen.getByRole('button', { name: '团队菜单' }))
  await user.type(screen.getByRole('textbox'), '收藏')
  expect(screen.getAllByRole('menuitem')).toHaveLength(1)
  expect(screen.getByRole('menuitem')).toHaveTextContent('收藏')
})

it('requires leave confirmation, refreshes on success and reports server rejection', async () => {
  const user = userEvent.setup(), reload = vi.fn().mockResolvedValue(undefined)
  render(tree(fixture(), reload))
  for (const decision of [false, true]) {
    mocks.confirm.mockResolvedValue(decision)
    await user.click(screen.getByRole('button', { name: 'Team menu' }))
    await user.click(screen.getByRole('menuitem', { name: 'Leave team…' }))
    await waitFor(() => expect(mocks.confirm).toHaveBeenCalled())
    if (!decision) expect(mocks.membership).not.toHaveBeenCalled()
  }
  await waitFor(() => expect(reload).toHaveBeenCalledOnce())
  mocks.membership.mockRejectedValue(new Error('The last owner cannot leave'))
  await user.click(screen.getByRole('button', { name: 'Team menu' }))
  await user.click(screen.getByRole('menuitem', { name: 'Leave team…' }))
  await waitFor(() => expect(mocks.error).toHaveBeenCalledWith('Could not update team membership', expect.objectContaining({ description: 'The last owner cannot leave' })))
})

it.each(['last-owner', 'managed', 'last-team', 'parent'] as const)('disables leave for %s using membership data', async reason => {
  const data = fixture()
  if (reason === 'last-owner') data.teamMembers[0].role = 'owner'
  if (reason === 'managed') data.teamMembers[0].managedSource = 'scim'
  if (reason === 'last-team') data.teamMembers.pop()
  if (reason === 'parent') data.teamSettings['team-2'] = { parentTeamId: data.teams[0].id } as BootstrapData['teamSettings'][string]
  render(tree(data))
  await userEvent.click(screen.getByRole('button', { name: 'Team menu' }))
  expect(screen.getByRole('menuitem', { name: 'Leave team…' })).toHaveAttribute('data-disabled')
})

it('opens subscriptions on hover and prevents overlapping saves, with failure feedback', async () => {
  const user = userEvent.setup()
  let reject!: (error: Error) => void
  mocks.subscribe.mockReturnValue(new Promise((_, no) => { reject = no }))
  render(tree())
  await user.click(screen.getByRole('button', { name: 'Team menu' }))
  await user.hover(screen.getByRole('menuitem', { name: 'Subscribe' }))
  const checkbox = await screen.findByRole('menuitemcheckbox', { name: 'An issue is added to the team' })
  act(() => checkbox.focus())
  await user.keyboard(' ')
  expect(mocks.subscribe).toHaveBeenCalledTimes(1)
  expect(screen.getByRole('menuitemcheckbox', { name: 'An issue is added to the team' })).toHaveAttribute('aria-checked', 'true')
  expect(screen.getByRole('menuitemcheckbox', { name: 'An issue is added to the triage queue' })).toHaveAttribute('data-disabled')
  await act(async () => reject(new Error('Offline')))
  expect(screen.getByRole('menuitemcheckbox', { name: 'An issue is added to the team' })).toHaveAttribute('aria-checked', 'false')
  expect(mocks.subscribe).toHaveBeenCalledTimes(1)
  expect(mocks.error).toHaveBeenCalledWith('Could not update team notifications', { description: 'Offline' })
})

import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { VirtuosoMockContext } from 'react-virtuoso'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, User } from '@/types/flow'
import { MemberProfilePage } from './member-profile-page'
import { memberSwitcherUsers, relativeTimeAgo } from './member-profile-model'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const agent = { id: 'app-1', name: 'codex', displayName: 'Codex', email: '', active: true, app: true, appScopes: ['app:assignable'] } as User
const invited = { id: 'user-3', name: 'invited', displayName: 'Invited Person', email: 'invited@example.test', active: false } as User

function profileData(overrides: Partial<BootstrapData> = {}) {
  const base = makeBootstrap()
  return makeBootstrap({
    favorites: [],
    users: [viewer, teammate, agent, invited],
    members: [
      { user: viewer, role: 'member', status: 'active', joinedAt: new Date(Date.now() - 5 * 3_600_000).toISOString() },
      { user: teammate, role: 'member', status: 'active', joinedAt: '2026-01-02T10:00:00.000Z', lastSeenAt: '2026-01-03T10:00:00.000Z' },
    ],
    teamMembers: [{ teamId: base.teams[0].id, userId: viewer.id, role: 'member', joinedAt: '2026-01-01T00:00:00.000Z' }],
    ...overrides,
  })
}

function renderProfile(data: BootstrapData, user: User, props: Partial<Parameters<typeof MemberProfilePage>[0]> = {}) {
  const onOpenMember = vi.fn()
  const view = render(<I18nProvider><TooltipProvider><VirtuosoMockContext.Provider value={{ viewportHeight: 440, itemHeight: 44 }}>
    <MemberProfilePage data={data} user={user} view="assigned" onNavigate={vi.fn()} onOpenMember={onOpenMember} onOpenIssue={vi.fn()} onUpdateIssue={vi.fn()} {...props}/>
  </VirtuosoMockContext.Provider></TooltipProvider></I18nProvider>)
  return { ...view, onOpenMember }
}

describe('MemberProfilePage', () => {
  beforeEach(() => { localStorage.clear(); localStorage.setItem('flow:locale', 'en-US') })
  afterEach(() => vi.useRealTimers())

  it('lists members before apps and leaves invited people out of the switcher', () => {
    expect(memberSwitcherUsers([agent, teammate, invited, viewer]).map(user => user.id)).toEqual([teammate.id, viewer.id, agent.id])
  })

  it('formats joined dates relative to now', () => {
    const now = Date.parse('2026-09-28T12:00:00.000Z')
    expect(relativeTimeAgo('2026-09-28T07:00:00.000Z', now)).toBe('5 hours ago')
    expect(relativeTimeAgo('2026-09-26T12:00:00.000Z', now)).toBe('2 days ago')
    expect(relativeTimeAgo('2026-09-28T11:59:40.000Z', now)).toBe('just now')
  })

  it('switches to another member from the "Open user" header button', async () => {
    const user = userEvent.setup()
    const { onOpenMember } = renderProfile(profileData(), viewer)
    const trigger = screen.getByRole('button', { name: 'Open user' })
    expect(trigger).toHaveTextContent('Viewer')
    await user.click(trigger)
    const switcher = await screen.findByRole('dialog', { name: /profile/i })
    const options = within(switcher).getAllByRole('option')
    expect(options.map(option => option.querySelector('.property-command-label')?.textContent)).toEqual(['Teammate', 'Viewer', 'Codex'])
    expect(options[2].querySelector('.property-command-option-end')).toHaveTextContent('Agent')
    // The search is hidden until you type.
    await user.keyboard('code')
    await waitFor(() => expect(within(switcher).getAllByRole('option')).toHaveLength(1))
    await user.keyboard('{Enter}')
    expect(onOpenMember).toHaveBeenCalledWith(agent)
  })

  it('opens the user switcher with "O then U"', async () => {
    renderProfile(profileData(), viewer)
    fireEvent.keyDown(document.body, { key: 'o' })
    fireEvent.keyDown(document.body, { key: 'u' })
    expect(await screen.findByRole('dialog', { name: /profile/i })).toBeInTheDocument()
  })

  it('shows the profile aside instead of the view summary and remembers it', async () => {
    const user = userEvent.setup()
    const data = profileData()
    const first = renderProfile(data, viewer)
    expect(screen.getByRole('button', { name: 'Open insights' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Open details' }))
    const aside = screen.getByRole('complementary', { name: 'Member details' })
    expect(within(aside).getByRole('heading', { name: 'Viewer' })).toBeInTheDocument()
    expect(aside).toHaveTextContent('viewer ⋅ Online')
    const details = within(aside).getByRole('region', { name: 'Profile details' })
    expect(details).toHaveTextContent('Emailviewer@example.test')
    expect(details).toHaveTextContent(/Local time\d{1,2}:\d{2}\s?[AP]M/)
    expect(details).toHaveTextContent('Joined5 hours ago')
    expect(within(details).getByRole('link', { name: 'Test team' })).toHaveAttribute('href', '/workspace/team/TST/overview')
    expect(screen.queryByRole('tab', { name: 'Labels' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Close details' })).toHaveAttribute('aria-pressed', 'true')
    first.unmount()

    renderProfile(data, teammate)
    const reopened = screen.getByRole('complementary', { name: 'Member details' })
    expect(within(reopened).getByRole('heading', { name: 'Teammate' })).toBeInTheDocument()
    expect(reopened).not.toHaveTextContent('Online')
    expect(within(reopened).getByRole('region', { name: 'Profile details' })).toHaveTextContent('TeamsNo teams')
    // Only your own profile has the options menu.
    expect(within(reopened).queryByRole('button', { name: 'Open menu' })).not.toBeInTheDocument()
  })

  it('toggles the aside with ⌘I', async () => {
    renderProfile(profileData(), viewer)
    act(() => { fireEvent.keyDown(document.body, { key: 'i', metaKey: true }) })
    expect(await screen.findByRole('complementary', { name: 'Member details' })).toBeInTheDocument()
    act(() => { fireEvent.keyDown(document.body, { key: 'i', metaKey: true }) })
    await waitFor(() => expect(screen.queryByRole('complementary', { name: 'Member details' })).not.toBeInTheDocument())
  })

  it('links "Edit profile" to your profile settings', async () => {
    const user = userEvent.setup()
    localStorage.setItem('workspace:member-profile:details', 'true')
    renderProfile(profileData(), viewer)
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    const item = await screen.findByRole('menuitem', { name: 'Edit profile' })
    expect(item).toHaveAttribute('href', '/workspace/settings/account/profile')
  })

  it('shows "No matching issues" when the member has none', async () => {
    renderProfile(profileData({ issues: [makeIssue({ assignee: teammate })] }), viewer)
    expect(await screen.findByText('No matching issues')).toBeInTheDocument()
  })
})

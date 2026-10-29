import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { batchNotifications, fetchPulseUnread } from '@/lib/api'
import { pulseAudio } from '@/lib/pulse-audio'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { AccountBootstrap, BootstrapData, WorkspaceSettings } from '@/types/flow'
import { pulseUnread, writePulseLastTab } from '@/components/pulse/pulse-unread'
import { inboxUnread } from '@/lib/inbox-unread'
import { Sidebar } from './sidebar'

vi.mock('@/lib/api', async original => ({
  ...(await original<typeof import('@/lib/api')>()),
  fetchPulseUnread: vi.fn(),
  batchNotifications: vi.fn(async () => ({ updated: 1 })),
}))

function sidebarData(overrides: Partial<BootstrapData> = {}): BootstrapData {
  return makeBootstrap({
    viewerRole: 'member', favorites: [], favoriteFolders: [], notifications: [], reviews: [], drafts: [], cycles: [], subscriptions: [],
    teamMembers: [], teamSettings: {}, cycleSettings: {}, userSettings: {},
    workspaceSettings: { featureFlags: { pulse: true }, featureSettings: {} } as unknown as WorkspaceSettings,
    ...overrides,
  })
}

function renderSidebar(data = sidebarData(), page: 'inbox' | 'pulse' = 'inbox') {
  const account = { viewer: data.viewer, workspaces: [{ workspace: data.workspace, role: 'Admin', joinedAt: '2026-01-01T00:00:00Z', issueCount: 0 }], workspaceRegionSelectorEnabled: false, workspaceDefaultRegion: 'us' } as AccountBootstrap
  return render(<MemoryRouter><TooltipProvider><I18nProvider><Sidebar account={account} data={data} page={page} onSearch={vi.fn()} onCreate={vi.fn()} onOpenSettings={vi.fn()} onSwitchWorkspace={vi.fn()} onCreateWorkspace={vi.fn()} onLogout={vi.fn(async () => undefined)}/></I18nProvider></TooltipProvider></MemoryRouter>)
}

const link = (name: RegExp) => screen.getByRole('link', { name })
const stored = (key: string) => JSON.parse(localStorage.getItem(`${key}:${viewer.id}`) ?? 'null')

async function openMenu(name: RegExp) {
  fireEvent.contextMenu(link(name))
  return screen.findByRole('menu')
}
async function openSub(menu: HTMLElement, name: string) {
  const trigger = within(menu).getByRole('menuitem', { name })
  trigger.focus()
  fireEvent.keyDown(trigger, { key: 'ArrowRight' })
  const menus = await screen.findAllByRole('menu')
  return menus[menus.length - 1]
}

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
  vi.mocked(fetchPulseUnread).mockResolvedValue({ count: 3, latestAt: '2026-10-04T08:00:00Z' })
  vi.mocked(batchNotifications).mockClear()
  pulseUnread.set({ count: 0 }, '')
  inboxUnread.set(0, '')
  const media = Object.assign(new EventTarget(), { matches: false, media: '(max-width: 1024px)' })
  vi.stubGlobal('matchMedia', () => media)
})
afterEach(() => { vi.unstubAllGlobals(); pulseAudio.stop() })

describe('sidebar row layout', () => {
  it.each(['dark', 'light'])('uses the shared navigation row inside Try menu wrappers in %s theme', theme => {
    document.documentElement.dataset.theme = theme
    try {
      renderSidebar(sidebarData({ viewerRole: 'admin' }))
      const section = document.querySelector('.sidebar-section-try') as HTMLElement
      const items = within(section)
      const invite = items.getByRole('link', { name: 'Invite people' })
      const cycles = items.getByRole('link', { name: 'Cycles' })
      const github = items.getByRole('button', { name: 'Connect GitHub' })

      for (const item of [invite, cycles, github]) {
        expect(item).toHaveClass('nav-item')
        expect(item.querySelector(':scope > .nav-icon > svg')).not.toBeNull()
        expect(item.querySelector(':scope > .nav-label')).toHaveTextContent(item.textContent!)
        const row = item.closest('.try-item')!
        expect(row.querySelector(':scope > .try-dismiss')).toHaveAccessibleName(`Dismiss ${item.textContent}`)
        expect(item.querySelector('.try-dismiss')).toBeNull()
      }
      for (const item of [invite, cycles]) {
        expect(item.parentElement).toHaveClass('sidebar-item-menu-trigger')
        expect(item.parentElement?.parentElement).toHaveClass('try-item')
      }
      expect(github.parentElement).toHaveClass('try-item')
    } finally {
      delete document.documentElement.dataset.theme
    }
  })

  it('keeps workspace and team links styled on the link rather than their menu wrapper', () => {
    const data = sidebarData({
      teamMembers: [{ teamId: 'team-1', userId: viewer.id, role: 'member', joinedAt: '' }],
      cycleSettings: { 'team-1': { enabled: true } as BootstrapData['cycleSettings'][string] },
      cycles: (['current', 'upcoming'] as const).map((status, index) => ({
        id: `cycle-${index}`, number: index + 1, name: status, description: '', teamId: 'team-1',
        startsAt: '2026-10-01T00:00:00Z', endsAt: '2026-11-01T00:00:00Z', status,
        capacity: 0, favorite: false, resources: [], createdAt: '', updatedAt: '',
      })),
    })
    const { container } = renderSidebar(data)
    for (const section of ['.sidebar-section-workspace', '.sidebar-team-list']) {
      const links = container.querySelectorAll(`${section} .sidebar-item-menu-trigger > a:not(.team-sub-link)`)
      expect(links.length).toBeGreaterThan(0)
      for (const item of links) {
        expect(item).toHaveClass('nav-item')
        expect(item.querySelector(':scope > .nav-icon > svg')).not.toBeNull()
        expect(item.querySelector(':scope > .nav-label')).not.toBeNull()
      }
      for (const label of ['Projects', 'Views']) {
        expect(within(container.querySelector(section) as HTMLElement).getByRole('link', { name: label })).toHaveClass('nav-item')
      }
    }
    const cycleLinks = container.querySelectorAll('.team-cycle-links > .sidebar-item-menu-trigger > a')
    expect(cycleLinks).toHaveLength(2)
    for (const item of cycleLinks) {
      expect(item).toHaveClass('nav-item', 'team-sub-link')
      expect(item.querySelector(':scope > .nav-label')).not.toBeNull()
    }
  })

  it('preserves Try context menus and dismisses without navigating', async () => {
    const user = userEvent.setup()
    renderSidebar(sidebarData({ viewerRole: 'admin' }))
    const menu = await openMenu(/^Invite people$/)
    expect(within(menu).getByRole('menuitem', { name: 'Copy link' })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await user.click(screen.getByRole('button', { name: 'Dismiss Invite people' }))
    expect(screen.queryByRole('link', { name: 'Invite people' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Connect GitHub' })).toHaveClass('nav-item')
  })
})

describe('sidebar item context menu', () => {
  it('offers Visibility, Badge, Customize sidebar and Copy link on Pulse', async () => {
    renderSidebar()
    const menu = await openMenu(/^Pulse/)
    expect(within(menu).getAllByRole('menuitem').map(item => item.textContent)).toEqual(['Visibility', 'Badge', 'Customize sidebar', 'Copy link'])
  })

  it('adds item specific actions and drops Badge where an item has no count', async () => {
    const view = renderSidebar(sidebarData({ workspaceSettings: { featureFlags: { pulse: true, ai: true }, featureSettings: {} } as unknown as WorkspaceSettings }))
    expect(within(await openMenu(/^Inbox/)).getAllByRole('menuitem').map(item => item.textContent)).toEqual(['Mark all as read', 'Visibility', 'Badge', 'Customize sidebar', 'Copy link'])
    fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument())
    expect(within(await openMenu(/^My issues/)).getAllByRole('menuitem').map(item => item.textContent)).toEqual(['Visibility', 'Customize sidebar', 'Copy link'])
    fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument())
    expect(within(await openMenu(/^Agent/)).getAllByRole('menuitem').map(item => item.textContent)).toEqual(['New chat', 'Visibility', 'Customize sidebar', 'Copy link'])
    view.unmount()
  })

  it('Visibility offers the choices each item supports and persists them per user', async () => {
    renderSidebar()
    const menu = await openMenu(/^Inbox/)
    const sub = await openSub(menu, 'Visibility')
    expect(within(sub).getAllByRole('menuitemradio').map(item => item.textContent)).toEqual(['Always show', 'Show when badged'])
    expect(within(sub).getByRole('menuitemradio', { name: 'Always show' })).toBeChecked()
    fireEvent.click(within(sub).getByRole('menuitemradio', { name: 'Show when badged' }))
    await waitFor(() => expect(stored('flow.sidebar.preference-overrides')).toEqual({ inbox: 'badged' }))
  })

  it('"Don\'t show" hides the item', async () => {
    renderSidebar()
    const menu = await openMenu(/^Pulse/)
    const sub = await openSub(menu, 'Visibility')
    expect(within(sub).getAllByRole('menuitemradio').map(item => item.textContent)).toEqual(['Always show', 'Show when badged', "Don't show"])
    fireEvent.click(within(sub).getByRole('menuitemradio', { name: "Don't show" }))
    await waitFor(() => expect(screen.queryByRole('link', { name: /^Pulse/ })).not.toBeInTheDocument())
  })

  it('Badge lists Default (Count), Count, Dot, None with a filter and follows the default badge style', async () => {
    renderSidebar()
    expect(await screen.findByLabelText('3 new pulse items')).toHaveAttribute('data-badge-style', 'count')
    const menu = await openMenu(/^Pulse/)
    const sub = await openSub(menu, 'Badge')
    expect(within(sub).getByPlaceholderText('Filter...')).toBeInTheDocument()
    expect(within(sub).getAllByRole('menuitemradio').map(item => item.textContent)).toEqual(['Default (Count)', 'Count1', 'Dot', 'None'])
    expect(within(sub).getByRole('menuitemradio', { name: 'Default (Count)' })).toBeChecked()
    await userEvent.type(within(sub).getByPlaceholderText('Filter...'), 'do')
    expect(within(sub).getAllByRole('menuitemradio').map(item => item.textContent)).toEqual(['Dot'])
  })

  it('a per item Dot or None changes only that badge and persists', async () => {
    renderSidebar(sidebarData({ notifications: [{ id: 'n1', recipientId: viewer.id, readAt: undefined } as never] }))
    expect(await screen.findByLabelText('3 new pulse items')).toBeInTheDocument()
    let menu = await openMenu(/^Pulse/)
    let sub = await openSub(menu, 'Badge')
    fireEvent.click(within(sub).getByRole('menuitemradio', { name: 'Dot' }))
    await waitFor(() => expect(screen.getByLabelText('3 new pulse items')).toHaveAttribute('data-badge-style', 'dot'))
    expect(stored('flow.sidebar.badge-modes')).toEqual({ pulse: 'dot' })
    menu = await openMenu(/^Pulse/)
    sub = await openSub(menu, 'Badge')
    expect(within(sub).getByRole('menuitemradio', { name: 'Dot' })).toBeChecked()
    fireEvent.click(within(sub).getByRole('menuitemradio', { name: 'None' }))
    await waitFor(() => expect(screen.queryByLabelText('3 new pulse items')).not.toBeInTheDocument())
    menu = await openMenu(/^Pulse/)
    sub = await openSub(menu, 'Badge')
    fireEvent.click(within(sub).getByRole('menuitemradio', { name: 'Default (Count)' }))
    expect(await screen.findByLabelText('3 new pulse items')).toHaveAttribute('data-badge-style', 'count')
    await waitFor(() => expect(localStorage.getItem(`flow.sidebar.badge-modes:${viewer.id}`)).toBeNull())
  })

  it('Default follows the global badge style', async () => {
    localStorage.setItem(`flow.sidebar.badge-style:${viewer.id}`, '"dot"')
    renderSidebar()
    expect(await screen.findByLabelText('3 new pulse items')).toHaveAttribute('data-badge-style', 'dot')
    const sub = await openSub(await openMenu(/^Pulse/), 'Badge')
    expect(within(sub).getByRole('menuitemradio', { name: 'Default (Dot)' })).toBeChecked()
  })

  it('Copy link copies the absolute link and confirms', async () => {
    const writeText = vi.fn(async () => undefined)
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })
    renderSidebar()
    const menu = await openMenu(/^Projects/)
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Copy link' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(new URL('/workspace/projects/all', window.location.origin).href))
  })

  it('Mark all as read on Inbox marks every notification read', async () => {
    renderSidebar()
    fireEvent.click(within(await openMenu(/^Inbox/)).getByRole('menuitem', { name: 'Mark all as read' }))
    await waitFor(() => expect(batchNotifications).toHaveBeenCalledWith('markAllRead'))
  })

  it('Customize sidebar opens the dialog', async () => {
    renderSidebar()
    fireEvent.click(within(await openMenu(/^Pulse/)).getByRole('menuitem', { name: 'Customize sidebar' }))
    expect(await screen.findByRole('dialog', { name: 'Customize sidebar' })).toBeInTheDocument()
  })
})

describe('sidebar Pulse link target (the reference app rule)', () => {
  it('any unread goes to For me, even with a remembered tab', async () => {
    writePulseLastTab('workspace', 'popular')
    renderSidebar()
    await screen.findByLabelText('3 new pulse items')
    expect(link(/^Pulse/)).toHaveAttribute('href', '/workspace/pulse/following')
  })

  it('with nothing unread it goes to the remembered tab, default Recent', async () => {
    vi.mocked(fetchPulseUnread).mockResolvedValue({ count: 0 })
    const view = renderSidebar()
    await waitFor(() => expect(fetchPulseUnread).toHaveBeenCalled())
    expect(link(/^Pulse/)).toHaveAttribute('href', '/workspace/pulse/all')
    view.unmount()
    writePulseLastTab('workspace', 'popular')
    renderSidebar()
    expect(link(/^Pulse/)).toHaveAttribute('href', '/workspace/pulse/popular')
  })

  it('on the Pulse page with nothing unread it is the bare /pulse path', async () => {
    vi.mocked(fetchPulseUnread).mockResolvedValue({ count: 0 })
    writePulseLastTab('workspace', 'popular')
    renderSidebar(sidebarData(), 'pulse')
    await waitFor(() => expect(fetchPulseUnread).toHaveBeenCalled())
    expect(link(/^Pulse/)).toHaveAttribute('href', '/workspace/pulse')
  })
})

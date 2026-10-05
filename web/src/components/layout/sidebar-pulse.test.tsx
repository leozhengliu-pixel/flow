import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { fetchInboxNotifications, fetchPulseUnread } from '@/lib/api'
import { pulseAudio } from '@/lib/pulse-audio'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { AccountBootstrap, BootstrapData, WorkspaceSettings } from '@/types/flow'
import { pulseUnread } from '@/components/pulse/pulse-unread'
import { inboxUnread } from '@/lib/inbox-unread'
import { Sidebar } from './sidebar'

vi.mock('@/lib/api', async original => ({
  ...(await original<typeof import('@/lib/api')>()),
  fetchPulseUnread: vi.fn(),
  fetchInboxNotifications: vi.fn(async () => ({ notifications: [], unreadCount: 7 })),
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

function setVisibility(pulse: 'always' | 'badged' | 'never') {
  const preferences = { inbox: 'always', reviews: 'always', myIssues: 'always', pulse, drafts: 'always', agent: 'always', initiatives: 'always', projects: 'always', documents: 'always', views: 'always', members: 'always', customers: 'never', teams: 'always', releases: 'always', loops: 'always' }
  localStorage.setItem('flow.sidebar.preferences', JSON.stringify(preferences))
}

const pulseLink = () => screen.queryByRole('link', { name: /^Pulse/ })

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
  pulseUnread.set({ count: 0 }, '')
  inboxUnread.set(0, '')
  const media = Object.assign(new EventTarget(), { matches: false, media: '(max-width: 1024px)' })
  vi.stubGlobal('matchMedia', () => media)
})
afterEach(() => { vi.unstubAllGlobals(); pulseAudio.stop() })

describe('sidebar Pulse item', () => {
  it('shows the unread badge ("N new pulse items") and links to For me while unread', async () => {
    vi.mocked(fetchPulseUnread).mockResolvedValue({ count: 3, latestAt: '2026-10-04T08:00:00Z' })
    renderSidebar()
    expect(await screen.findByLabelText('3 new pulse items')).toHaveTextContent('3')
    expect(pulseLink()).toHaveAttribute('href', '/workspace/pulse/following')
  })

  it('links to the last used tab (default Recent) when nothing is unread', async () => {
    vi.mocked(fetchPulseUnread).mockResolvedValue({ count: 0 })
    renderSidebar()
    await waitFor(() => expect(fetchPulseUnread).toHaveBeenCalled())
    expect(pulseLink()).toHaveAttribute('href', '/workspace/pulse/all')
    expect(screen.queryByLabelText(/new pulse item/)).not.toBeInTheDocument()
  })

  it('"Show when badged" hides Pulse until there is something unread', async () => {
    setVisibility('badged')
    vi.mocked(fetchPulseUnread).mockResolvedValue({ count: 0 })
    const view = renderSidebar()
    await waitFor(() => expect(fetchPulseUnread).toHaveBeenCalled())
    expect(pulseLink()).not.toBeInTheDocument()
    view.unmount()
    vi.mocked(fetchPulseUnread).mockResolvedValue({ count: 1 })
    renderSidebar()
    expect(await screen.findByLabelText('1 new pulse item')).toBeInTheDocument()
    expect(pulseLink()).toBeInTheDocument()
  })

  it('keeps Pulse visible while you are on the Pulse page, even "Show when badged" with nothing unread', async () => {
    setVisibility('badged')
    vi.mocked(fetchPulseUnread).mockResolvedValue({ count: 0 })
    renderSidebar(sidebarData(), 'pulse')
    await waitFor(() => expect(fetchPulseUnread).toHaveBeenCalled())
    expect(pulseLink()).toBeInTheDocument()
  })

  it('sidebar visibility is per user: one user hiding Pulse does not hide it for another in the same browser', async () => {
    vi.mocked(fetchPulseUnread).mockResolvedValue({ count: 0 })
    // The pre-upgrade unscoped choice is adopted by the first user, then removed.
    setVisibility('never')
    const alice = renderSidebar()
    await waitFor(() => expect(fetchPulseUnread).toHaveBeenCalled())
    expect(pulseLink()).not.toBeInTheDocument()
    expect(localStorage.getItem('flow.sidebar.preferences')).toBeNull()
    expect(JSON.parse(localStorage.getItem(`flow.sidebar.preferences:${viewer.id}`) ?? '{}').pulse).toBe('never')
    alice.unmount()
    const eve = { ...viewer, id: 'user-eve', name: 'Eve', displayName: 'Eve' }
    renderSidebar(sidebarData({ viewer: eve, users: [eve] }))
    expect(pulseLink()).toBeInTheDocument()
  })

  it('paged workspaces badge the inbox with the server unread count (the bootstrap has no notifications)', async () => {
    vi.mocked(fetchPulseUnread).mockResolvedValue({ count: 0 })
    renderSidebar(sidebarData({ issueCollectionPaged: true, notifications: [] }))
    await waitFor(() => expect(fetchInboxNotifications).toHaveBeenCalledWith('?limit=1'))
    const inbox = screen.getByRole('link', { name: /^Inbox/ })
    await waitFor(() => expect(inbox).toHaveTextContent('7'))
  })

  it('never shows Pulse to guests or when the feature is off', async () => {
    vi.mocked(fetchPulseUnread).mockResolvedValue({ count: 2 })
    const view = renderSidebar(sidebarData({ viewerRole: 'guest' }))
    expect(pulseLink()).not.toBeInTheDocument()
    view.unmount()
    renderSidebar(sidebarData({ workspaceSettings: { featureFlags: { pulse: false }, featureSettings: {} } as unknown as WorkspaceSettings }))
    expect(pulseLink()).not.toBeInTheDocument()
    expect(viewer.id).toBe('user-1')
  })
})

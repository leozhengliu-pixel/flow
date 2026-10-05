import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { StrictMode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { fetchInitiativeProjectUpdatesSubscription, fetchPulseFeed, markPulseSeen, setInitiativeProjectUpdatesSubscription } from '@/lib/api'
import { resetInitiativeProjectUpdatesChoices } from '@/lib/pulse-subscriptions'
import userEvent from '@testing-library/user-event'
import { makeBootstrap, project, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, PulseFeedPage, PulseItem, SavedView } from '@/types/flow'
import { FeedStableLastSeen } from './feed-stable-last-seen'
import { PulsePage } from './pulse-page'
import { recordPulseSeen, resetPulseSeenForTests } from './use-pulse-feed'

vi.mock('@/lib/api', async original => ({
  ...(await original<typeof import('@/lib/api')>()),
  fetchPulseFeed: vi.fn(),
  fetchPulseUnread: vi.fn(async () => ({ count: 0 })),
  markPulseSeen: vi.fn(async () => ({})),
  updateUserSettings: vi.fn(async () => ({})),
  listProjectHistory: vi.fn(async () => ({ nodes: [] })),
  setInitiativeProjectUpdatesSubscription: vi.fn(async (_id: string, subscribed: boolean) => ({ subscribed })),
  fetchInitiativeProjectUpdatesSubscription: vi.fn(async () => ({ subscribed: false })),
}))

function pulseItem(id: string, createdAt: string, overrides: Partial<PulseItem> = {}): PulseItem {
  return {
    id,
    kind: 'project',
    source: { id: project.id, name: project.name, url: '/workspace/project/project-one/updates', icon: 'Project', color: '#5e6ad2' },
    reasons: [{ type: 'projectMember', sourceIds: [project.id] }],
    subscribed: true,
    update: { id: `update-${id}`, projectId: project.id, body: `Body of ${id}`, health: 'onTrack', createdAt, user: teammate, comments: [], reactions: {}, attachments: [] },
    ...overrides,
  } as PulseItem
}

function page(items: PulseItem[], extra: Partial<PulseFeedPage> = {}): PulseFeedPage {
  return { items, unreadCount: 0, ...extra }
}

const savedView = { id: 'view-1', name: 'Design feed', description: '', resource: 'pulse', scope: 'personal', ownerId: viewer.id, view: 'all', filters: [], display: {}, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z' } as SavedView

function data(overrides: Partial<BootstrapData> = {}) {
  return makeBootstrap({
    savedViews: [savedView],
    userSettings: { [viewer.id]: { userId: viewer.id, pulseWelcomeDismissed: true, pulseSchedule: 'weekly' } as BootstrapData['userSettings'][string] },
    teamMembers: [],
    ...overrides,
  })
}

function renderPage(props: Partial<Parameters<typeof PulsePage>[0]> = {}) {
  const handlers = {
    onNavigateView: vi.fn(), onNavigateSavedView: vi.fn(), onNavigate: vi.fn(),
    onCreateSavedView: vi.fn(), onUpdateSavedView: vi.fn(), onDeleteSavedView: vi.fn(),
    onCreateProject: vi.fn(), onUpdateProject: vi.fn(), onDeleteProject: vi.fn(), onCommentProject: vi.fn(), onReactProject: vi.fn(), onUploadProjectAttachment: vi.fn(), onDeleteProjectAttachment: vi.fn(),
    onCreateInitiative: vi.fn(), onUpdateInitiative: vi.fn(), onDeleteInitiative: vi.fn(), onCommentInitiative: vi.fn(), onReactInitiative: vi.fn(), onUploadInitiativeAttachment: vi.fn(), onDeleteInitiativeAttachment: vi.fn(),
  }
  const result = render(<TooltipProvider><I18nProvider><PulsePage data={data()} view="following" {...handlers} {...props}/></I18nProvider></TooltipProvider>)
  return { ...result, handlers: { ...handlers, ...props } }
}

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
  FeedStableLastSeen.clear()
  resetPulseSeenForTests()
  vi.mocked(fetchPulseFeed).mockReset()
  vi.mocked(markPulseSeen).mockClear()
  vi.stubGlobal('IntersectionObserver', class { observe() {} disconnect() {} unobserve() {} })
})
afterEach(() => { vi.unstubAllGlobals() })

describe('Pulse page', () => {
  it('loads the For me tab from the feed API and switches tabs with 1–3 and custom feeds with 4', async () => {
    vi.mocked(fetchPulseFeed).mockResolvedValue(page([pulseItem('a', new Date().toISOString())]))
    const { handlers } = renderPage()
    expect(await screen.findByText('Body of a')).toBeInTheDocument()
    expect(vi.mocked(fetchPulseFeed).mock.calls[0][0]).toMatchObject({ view: 'following', limit: 30 })
    const tabs = screen.getByRole('navigation', { name: 'Pulse views' })
    expect(within(tabs).getByRole('link', { name: 'For me' })).toHaveAttribute('aria-current', 'page')
    expect(within(tabs).getByRole('link', { name: 'Popular' })).toHaveAttribute('href', '/workspace/pulse/popular')
    expect(within(tabs).getByRole('link', { name: 'Recent' })).toHaveAttribute('href', '/workspace/pulse/all')
    expect(within(tabs).getByRole('link', { name: 'Design feed' })).toHaveAttribute('href', '/workspace/pulse/view/view-1')

    fireEvent.keyDown(window, { key: '2' })
    expect(handlers.onNavigateView).toHaveBeenLastCalledWith('popular')
    fireEvent.keyDown(window, { key: '3' })
    expect(handlers.onNavigateView).toHaveBeenLastCalledWith('all')
    fireEvent.keyDown(window, { key: '4' })
    expect(handlers.onNavigateSavedView).toHaveBeenLastCalledWith('view-1')
    // Typing in the search box never switches tabs.
    fireEvent.keyDown(screen.getByRole('textbox', { name: 'Find in feed…' }), { key: '1' })
    expect(handlers.onNavigateView).toHaveBeenCalledTimes(2)
  })

  it('R opens the reply composer on the active update', async () => {
    vi.mocked(fetchPulseFeed).mockResolvedValue(page([pulseItem('a', new Date().toISOString()), pulseItem('b', new Date(Date.now() - 60_000).toISOString())]))
    renderPage()
    await screen.findByText('Body of b')
    const cards = document.querySelectorAll('[data-pulse-item]')
    fireEvent.mouseEnter(cards[1])
    expect(within(cards[1] as HTMLElement).getByRole('button', { name: 'Leave a comment' })).toHaveAttribute('aria-expanded', 'false')
    fireEvent.keyDown(window, { key: 'r' })
    await waitFor(() => expect(within(cards[1] as HTMLElement).getByRole('button', { name: 'Close comments' })).toHaveAttribute('aria-expanded', 'true'))
    expect(within(cards[0] as HTMLElement).getByRole('button', { name: 'Leave a comment' })).toBeInTheDocument()
  })

  it('groups Recent by date, shows "Last seen" above already-seen items, and none of that on Popular', async () => {
    const now = Date.now()
    vi.mocked(fetchPulseFeed).mockResolvedValue(page([
      pulseItem('new', new Date(now - 60_000).toISOString()),
      pulseItem('old', new Date(now - 3 * 3600_000).toISOString()),
    ], { lastSeenAt: new Date(now - 3600_000).toISOString() }))
    const view = renderPage({ view: 'all' })
    await screen.findByText('Body of old')
    expect(screen.getAllByText('Today')).toHaveLength(1)
    expect(screen.getByText('Last seen')).toBeInTheDocument()
    view.unmount()
    FeedStableLastSeen.clear()
    renderPage({ view: 'popular' })
    await screen.findByText('Body of old')
    expect(screen.queryByText('Today')).not.toBeInTheDocument()
    expect(screen.queryByText('Last seen')).not.toBeInTheDocument()
  })

  it('shows Linear empty copy per tab and a no-results state for searches', async () => {
    vi.mocked(fetchPulseFeed).mockResolvedValue(page([]))
    renderPage()
    expect(await screen.findByText('Updates from initiatives, projects that you’re a part of or subscribed to will show here.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New update' })).toBeInTheDocument()
    fireEvent.change(screen.getByRole('textbox', { name: 'Find in feed…' }), { target: { value: 'roadmap' } })
    expect(await screen.findByText('No results found for "roadmap"')).toBeInTheDocument()
    await waitFor(() => expect(vi.mocked(fetchPulseFeed).mock.calls.at(-1)?.[0]).toMatchObject({ q: 'roadmap' }))
    fireEvent.click(within(document.querySelector('.pulse-no-results') as HTMLElement).getByRole('button', { name: 'Clear search' }))
    expect(await screen.findByText('Updates from initiatives, projects that you’re a part of or subscribed to will show here.')).toBeInTheDocument()
  })

  it('counts realtime arrivals in a "New update available" pill instead of inserting them', async () => {
    const first = pulseItem('a', new Date(Date.now() - 60_000).toISOString())
    vi.mocked(fetchPulseFeed).mockResolvedValue(page([first]))
    renderPage({ view: 'all' })
    await screen.findByText('Body of a')
    vi.mocked(fetchPulseFeed).mockResolvedValue(page([pulseItem('b', new Date().toISOString()), first]))
    vi.useFakeTimers({ shouldAdvanceTime: true })
    act(() => { window.dispatchEvent(new CustomEvent('flow:pulse-activity')) })
    await act(async () => { await vi.advanceTimersByTimeAsync(600) })
    vi.useRealTimers()
    const pill = await screen.findByRole('button', { name: /New update available/ })
    expect(pill).toHaveTextContent('1')
    expect(screen.queryByText('Body of b')).not.toBeInTheDocument()
    fireEvent.click(pill)
    expect(await screen.findByText('Body of b')).toBeInTheDocument()
  })

  it('R replies to the card under a resting cursor, then the focused card, never blindly the first', async () => {
    const now = Date.now()
    vi.mocked(fetchPulseFeed).mockResolvedValue(page([pulseItem('a', new Date(now).toISOString()), pulseItem('b', new Date(now - 60_000).toISOString()), pulseItem('c', new Date(now - 120_000).toISOString())]))
    renderPage()
    await screen.findByText('Body of c')
    const cards = document.querySelectorAll<HTMLElement>('[data-pulse-item]')
    // The card rendered under the pointer: no mouseenter, only a move.
    fireEvent.pointerMove(cards[1])
    fireEvent.keyDown(window, { key: 'r' })
    await waitFor(() => expect(within(cards[1]).getByRole('button', { name: 'Close comments' })).toBeInTheDocument())
    expect(within(cards[0]).getByRole('button', { name: 'Leave a comment' })).toBeInTheDocument()
    // Pointer leaves; keyboard focus sits in the third card.
    fireEvent.mouseLeave(cards[1])
    act(() => within(cards[2]).getByRole('button', { name: 'Post actions' }).focus())
    fireEvent.keyDown(window, { key: 'r' })
    await waitFor(() => expect(within(cards[2]).getByRole('button', { name: 'Close comments' })).toBeInTheDocument())
    expect(within(cards[0]).getByRole('button', { name: 'Leave a comment' })).toBeInTheDocument()
  })

  it('marks seen at the newest loaded For me update (not now), and only on the unfiltered For me tab', async () => {
    const newest = '2026-10-04T08:00:00.000Z'
    vi.mocked(fetchPulseFeed).mockResolvedValue(page([pulseItem('a', newest), pulseItem('b', '2026-10-03T08:00:00.000Z')]))
    const view = renderPage()
    await screen.findByText('Body of b')
    await waitFor(() => expect(markPulseSeen).toHaveBeenCalledWith(newest, { keepalive: undefined }))
    view.unmount()
    // Leaving never writes wall-clock now — at most the newest update the reader reached.
    expect(vi.mocked(markPulseSeen).mock.calls.every(([at]) => at === newest)).toBe(true)

    for (const tab of ['popular', 'all'] as const) {
      vi.mocked(markPulseSeen).mockClear()
      resetPulseSeenForTests()
      FeedStableLastSeen.clear()
      const other = renderPage({ view: tab })
      await screen.findByText('Body of b')
      other.unmount()
      expect(markPulseSeen).not.toHaveBeenCalled()
    }

    // A search on For me narrows the feed: reaching its top is not "seen".
    vi.mocked(markPulseSeen).mockClear()
    resetPulseSeenForTests()
    FeedStableLastSeen.clear()
    vi.mocked(fetchPulseFeed).mockImplementation(async query => page(query.q ? [pulseItem('a', newest)] : []))
    const searching = renderPage()
    await screen.findByText('Updates from initiatives, projects that you’re a part of or subscribed to will show here.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Find in feed…' }), { target: { value: 'roadmap' } })
    await screen.findByText('Body of a')
    searching.unmount()
    expect(markPulseSeen).not.toHaveBeenCalled()
  })

  it('StrictMode mount/unmount before the feed loads records nothing', async () => {
    vi.mocked(fetchPulseFeed).mockImplementation(() => new Promise(() => undefined))
    const handlers = { onNavigateView: vi.fn(), onNavigateSavedView: vi.fn(), onCreateSavedView: vi.fn(), onUpdateSavedView: vi.fn(), onDeleteSavedView: vi.fn(), onCreateProject: vi.fn(), onUpdateProject: vi.fn(), onDeleteProject: vi.fn(), onCommentProject: vi.fn(), onReactProject: vi.fn(), onUploadProjectAttachment: vi.fn(), onDeleteProjectAttachment: vi.fn(), onCreateInitiative: vi.fn(), onUpdateInitiative: vi.fn(), onDeleteInitiative: vi.fn(), onCommentInitiative: vi.fn(), onReactInitiative: vi.fn(), onUploadInitiativeAttachment: vi.fn(), onDeleteInitiativeAttachment: vi.fn() }
    const view = render(<StrictMode><TooltipProvider><I18nProvider><PulsePage data={data()} view="following" {...handlers}/></I18nProvider></TooltipProvider></StrictMode>)
    await act(async () => { await Promise.resolve() })
    view.unmount()
    expect(markPulseSeen).not.toHaveBeenCalled()
  })

  it('initiative cards offer "Subscribe to initiative\'s project updates" wired to the Pulse API', async () => {
    resetInitiativeProjectUpdatesChoices()
    const initiativeItem = pulseItem('i', new Date().toISOString(), {
      kind: 'initiative',
      source: { id: 'initiative-1', name: 'Growth', url: '/workspace/initiative/growth/updates', icon: 'Initiative', color: '#5e6ad2' },
      update: { id: 'update-i', initiativeId: 'initiative-1', body: 'Body of i', health: 'onTrack', createdAt: new Date().toISOString(), user: teammate, comments: [], reactions: {}, attachments: [] },
    } as Partial<PulseItem>)
    vi.mocked(fetchPulseFeed).mockResolvedValue(page([initiativeItem, pulseItem('p', new Date(Date.now() - 60_000).toISOString())]))
    renderPage()
    await screen.findByText('Body of i')
    const user = userEvent.setup()
    const [initiativeCard, projectCard] = document.querySelectorAll<HTMLElement>('[data-pulse-item]')
    await user.click(within(initiativeCard).getByRole('button', { name: 'Post actions' }))
    const subscribe = await screen.findByRole('menuitem', { name: "Subscribe to initiative's project updates" })
    expect(fetchInitiativeProjectUpdatesSubscription).toHaveBeenCalledWith('initiative-1', expect.any(AbortSignal))
    await user.click(subscribe)
    await waitFor(() => expect(setInitiativeProjectUpdatesSubscription).toHaveBeenCalledWith('initiative-1', true))
    await user.click(within(initiativeCard).getByRole('button', { name: 'Post actions' }))
    expect(await screen.findByRole('menuitem', { name: "Unsubscribe from initiative's project updates" })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    // Project cards do not have it.
    await user.click(within(projectCard).getByRole('button', { name: 'Post actions' }))
    await screen.findByRole('menuitem', { name: 'Unsubscribe from this project' })
    expect(screen.queryByRole('menuitem', { name: /project updates/ })).not.toBeInTheDocument()
  })

  it('records last seen monotonically: never backwards, throttled while reading, forced on leave', () => {
    const base = Date.parse('2026-10-01T10:00:00Z')
    expect(recordPulseSeen(base)).toBe(true)
    expect(markPulseSeen).toHaveBeenLastCalledWith('2026-10-01T10:00:00.000Z', { keepalive: undefined })
    expect(recordPulseSeen(base - 60_000)).toBe(false)
    expect(recordPulseSeen(base + 5_000)).toBe(false)
    expect(recordPulseSeen(base + 5_000, { force: true, keepalive: true })).toBe(true)
    expect(markPulseSeen).toHaveBeenLastCalledWith('2026-10-01T10:00:05.000Z', { keepalive: true })
    expect(recordPulseSeen(base + 5_000, { force: true })).toBe(false)
    expect(markPulseSeen).toHaveBeenCalledTimes(2)
  })
})

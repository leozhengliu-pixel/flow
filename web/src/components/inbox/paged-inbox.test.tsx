import { act, fireEvent, render, renderHook, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { fetchInboxNotifications, fetchPulseCapabilities, fetchPulseSummary, listIssueRecords, updateInboxNotification } from '@/lib/api'
import { INBOX_ACTIVITY_EVENT, inboxUnread } from '@/lib/inbox-unread'
import { makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, Notification, NotificationList } from '@/types/flow'

import { InboxAppPage } from './inbox-app-page'
import { usePagedInbox } from './use-paged-inbox'

vi.mock('@/lib/api', async original => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchInboxNotifications: vi.fn(),
  listIssueRecords: vi.fn(),
  updateInboxNotification: vi.fn(),
  batchNotifications: vi.fn(async () => ({ updated: 0 })),
  fetchPulseSummary: vi.fn(),
  fetchPulseCapabilities: vi.fn(),
  updateUserSettings: vi.fn(),
}))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/components/detail/detail-pane', () => ({ DetailPane: ({ issue }: { issue: { title: string; isSummary?: boolean } }) => <div data-testid="issue-detail">{issue.isSummary ? 'Loading issue' : issue.title}</div> }))

const flowActor = { id: 'flow', name: 'Flow', displayName: 'Flow', email: '', active: true } as unknown as Notification['actor']

function notification(overrides: Partial<Notification>): Notification {
  return {
    id: 'n-1', recipientId: viewer.id, type: 'assignment', sourceType: 'issue', sourceId: 'issue-9', actor: teammate,
    category: 'assignments', groupKey: 'n-1', occurrenceCount: 1, latestActorIds: [], favorite: false,
    createdAt: '2026-10-01T06:00:00.000Z', updatedAt: '2026-10-01T06:00:00.000Z',
    ...overrides,
  } as Notification
}

const assignment = notification({ id: 'n-issue', issueId: 'issue-9' })
const dailyPulse = notification({
  id: 'pulse-1', type: 'pulseSummary', category: 'pulse', sourceType: 'pulse', sourceId: '2026-10-01T06:00:00Z', actor: flowActor,
  title: 'Daily Pulse', text: 'Update from Apollo', payload: { schedule: 'daily', updateIds: ['update-1'] },
  createdAt: '2026-10-01T07:00:00.000Z', updatedAt: '2026-10-01T07:00:00.000Z',
})
const pagedIssue = makeIssue({ id: 'issue-9', identifier: 'TST-9', title: 'Paged issue', isSummary: true })

function list(notifications: Notification[], extra: Partial<NotificationList> = {}): NotificationList {
  return { notifications, unreadCount: notifications.filter(item => !item.readAt).length, ...extra }
}

function pagedData(overrides: Partial<BootstrapData> = {}) {
  // The paged bootstrap ships no notifications and only a window of issues.
  return makeBootstrap({ issueCollectionPaged: true, issues: [], notifications: [], reviews: [], comments: {}, userSettings: {}, ...overrides } as Partial<BootstrapData>)
}

function renderInbox(data: BootstrapData, onLoadIssueContext = vi.fn(async () => undefined)) {
  const noop = vi.fn(async () => undefined)
  const view = render(
    <I18nProvider>
      <InboxAppPage data={data} onReload={noop} onOpenIssue={vi.fn()} onDeleteRelation={noop} onCreateSubIssue={noop} onReactIssue={noop} onCreateComment={noop} onEditComment={noop} onDeleteComment={noop} onReactComment={noop} onUploadAttachment={noop} onDeleteAttachment={noop} onLoadIssueContext={onLoadIssueContext}/>
    </I18nProvider>,
  )
  return { ...view, onLoadIssueContext }
}

const row = (text: string) => screen.getAllByRole('link').find(item => item.textContent?.includes(text))!

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.clearAllMocks()
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
  window.history.replaceState(null, '', '/workspace/inbox')
  inboxUnread.set(0, '')
  vi.mocked(fetchInboxNotifications).mockResolvedValue(list([dailyPulse, assignment], { nextCursor: 'cursor-1', hasMore: true }))
  vi.mocked(listIssueRecords).mockResolvedValue({ items: [pagedIssue], hasMore: false, total: 1 } as Awaited<ReturnType<typeof listIssueRecords>>)
  vi.mocked(fetchPulseSummary).mockResolvedValue({ title: 'Daily Pulse', generatedAt: '2026-10-01T06:00:00.000Z', text: '', ai: false, sections: [] })
  vi.mocked(fetchPulseCapabilities).mockResolvedValue({ aiSummaries: false, audio: false })
})
afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

describe('paged inbox', () => {
  it('reads notifications from the API (paged bootstrap has none) and resolves their issues', async () => {
    renderInbox(pagedData())
    expect(await screen.findByText('Daily Pulse')).toBeInTheDocument()
    expect(within(row('Daily Pulse')).getByText('Update from Apollo')).toBeInTheDocument()
    expect(await screen.findByText('Paged issue')).toBeInTheDocument()
    expect(fetchInboxNotifications).toHaveBeenCalledWith('?limit=50')
    expect(listIssueRecords).toHaveBeenCalledWith(expect.objectContaining({ filter: { field: 'id', operator: 'in', values: ['issue-9'] }, archived: 'all' }), undefined, 'workspace')
    // The sidebar badge follows the server's unread count.
    expect(inboxUnread.state).toEqual({ count: 2, workspaceKey: 'workspace' })
  })

  it('applies row actions to the API rows (mark as read)', async () => {
    vi.mocked(updateInboxNotification).mockImplementation(async (id, input) => ({ ...assignment, id, readAt: input.read ? '2026-10-02T00:00:00.000Z' : undefined }))
    renderInbox(pagedData())
    await screen.findByText('Paged issue')
    expect(row('Paged issue')).toHaveAttribute('data-read', 'false')
    fireEvent.contextMenu(row('Paged issue'))
    await userEvent.setup().click(await screen.findByRole('menuitem', { name: /Mark as read/ }))
    await waitFor(() => expect(updateInboxNotification).toHaveBeenCalledWith('n-issue', { read: true }))
    await waitFor(() => expect(row('Paged issue')).toHaveAttribute('data-read', 'true'))
  })

  it('opening an issue notification loads that issue’s full record into the app', async () => {
    const { onLoadIssueContext } = renderInbox(pagedData())
    await screen.findByText('Paged issue')
    await userEvent.setup().click(row('Paged issue'))
    await waitFor(() => expect(onLoadIssueContext).toHaveBeenCalledWith('issue-9'))
  })

  it('realtime activity re-reads the first page and adds new notifications on top', async () => {
    renderInbox(pagedData())
    await screen.findByText('Paged issue')
    const fresh = notification({ id: 'n-new', type: 'mention', category: 'mentions', issueId: 'issue-9', createdAt: '2026-10-03T00:00:00.000Z', updatedAt: '2026-10-03T00:00:00.000Z' })
    vi.mocked(fetchInboxNotifications).mockResolvedValue(list([fresh, dailyPulse, assignment]))
    act(() => { window.dispatchEvent(new CustomEvent(INBOX_ACTIVITY_EVENT, { detail: { id: 'e1', type: 'comment.created', actorId: teammate.id } })) })
    await waitFor(() => expect(screen.getAllByRole('link').some(item => item.textContent?.includes('mentioned you'))).toBe(true))
    expect(screen.getByText('Daily Pulse')).toBeInTheDocument()
  })

  it('keeps a ?notification= selection while the first page loads', async () => {
    window.history.replaceState(null, '', '/workspace/inbox?notification=pulse-1')
    renderInbox(pagedData())
    expect(await screen.findByRole('heading', { name: 'Daily Pulse' })).toBeInTheDocument()
  })
})

describe('usePagedInbox', () => {
  it('pages with the cursor and merges realtime patches from the app', async () => {
    const second = notification({ id: 'n-old', createdAt: '2026-09-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z' })
    vi.mocked(fetchInboxNotifications).mockResolvedValueOnce(list([dailyPulse], { nextCursor: 'cursor-1', hasMore: true })).mockResolvedValueOnce(list([second], { hasMore: false }))
    const { result, rerender } = renderHook(({ live }) => usePagedInbox({ enabled: true, workspaceKey: 'workspace', live, loadedIssues: [] }), { initialProps: { live: [] as Notification[] } })
    await waitFor(() => expect(result.current.notifications.map(item => item.id)).toEqual(['pulse-1']))
    expect(result.current.hasMore).toBe(true)
    await act(async () => { await result.current.loadMore() })
    expect(fetchInboxNotifications).toHaveBeenLastCalledWith('?limit=50&cursor=cursor-1')
    expect(result.current.notifications.map(item => item.id)).toEqual(['pulse-1', 'n-old'])
    expect(result.current.hasMore).toBe(false)
    // A realtime notification patch (app data.notifications) replaces the row.
    rerender({ live: [{ ...dailyPulse, readAt: '2026-10-02T00:00:00.000Z' }] })
    await waitFor(() => expect(result.current.notifications.find(item => item.id === 'pulse-1')?.readAt).toBe('2026-10-02T00:00:00.000Z'))
  })

  it('does nothing outside paged mode', () => {
    renderHook(() => usePagedInbox({ enabled: false, workspaceKey: 'workspace', live: [], loadedIssues: [] }))
    expect(fetchInboxNotifications).not.toHaveBeenCalled()
  })
})

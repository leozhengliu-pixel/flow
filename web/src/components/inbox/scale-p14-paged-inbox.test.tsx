import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchInboxNotifications, listIssueRecords } from '@/lib/api'
import { INBOX_ACTIVITY_EVENT, INBOX_STATE_CAPABILITY, inboxUnread } from '@/lib/inbox-unread'
import type { Notification } from '@/types/flow'
import { usePagedInbox } from './use-paged-inbox'

vi.mock('@/lib/api', () => ({ fetchInboxNotifications: vi.fn(), listIssueRecords: vi.fn() }))
const notifications = (n: number) => Array.from({ length: n }, (_, i) => ({ id: `row-${i}`, recipientId: 'me' } as Notification))
const live: Notification[] = []
const loadedIssues: [] = []

beforeEach(() => {
  inboxUnread.configure({ workspaceKey: 'acme', viewerId: 'me', capabilities: [INBOX_STATE_CAPABILITY] })
  vi.mocked(fetchInboxNotifications).mockResolvedValue({ notifications: notifications(100), unreadCount: 100, hasMore: false })
  vi.mocked(listIssueRecords).mockResolvedValue({ items: [], hasMore: false, total: 0 })
})
afterEach(() => { cleanup(); inboxUnread.configure(); vi.useRealTimers(); vi.clearAllMocks() })

describe('P14 open inbox recipient refresh', () => {
  it('ignores unrelated business activity and refreshes a changed row beyond page one', async () => {
    const { result } = renderHook(() => usePagedInbox({ enabled: true, workspaceKey: 'acme', live, loadedIssues }))
    await waitFor(() => expect(result.current.notifications).toHaveLength(100))
    vi.useFakeTimers()
    act(() => window.dispatchEvent(new CustomEvent(INBOX_ACTIVITY_EVENT, { detail: { type: 'comment.created', actorId: 'other' } })))
    await act(async () => { await vi.advanceTimersByTimeAsync(501) })
    expect(fetchInboxNotifications).toHaveBeenCalledTimes(1)
    const updated = notifications(100)
    updated[80] = { ...updated[80], readAt: '2026-11-01T00:00:00Z' }
    vi.mocked(fetchInboxNotifications).mockResolvedValueOnce({ notifications: updated, unreadCount: 99, hasMore: false })
    act(() => { inboxUnread.apply('acme', 'me', { revision: '1', unreadCount: 99, changedIds: ['row-80'], invalidated: false }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(501) })
    expect(fetchInboxNotifications).toHaveBeenLastCalledWith('?limit=100')
    expect(result.current.notifications[80].readAt).toBeTruthy()
    expect(inboxUnread.state.count).toBe(99)
  })

  it('merges new notifications at the front while preserving older loaded pages', async () => {
    const { result } = renderHook(() => usePagedInbox({ enabled: true, workspaceKey: 'acme', live, loadedIssues }))
    await waitFor(() => expect(result.current.notifications).toHaveLength(100))
    vi.useFakeTimers()
    const newest = { id: 'new-row', recipientId: 'me' } as Notification
    vi.mocked(fetchInboxNotifications).mockResolvedValueOnce({ notifications: [newest, ...notifications(49)], unreadCount: 1, hasMore: true })
    act(() => { inboxUnread.apply('acme', 'me', { revision: '1', unreadCount: 101, changedIds: ['new-row'], invalidated: false }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(501) })
    expect(fetchInboxNotifications).toHaveBeenLastCalledWith('?limit=50')
    expect(result.current.notifications).toHaveLength(101)
    expect(result.current.notifications[0].id).toBe('new-row')
    expect(inboxUnread.state.count).toBe(101)
  })

  it('preserves a loaded window larger than the server page cap on batch invalidation', async () => {
    const all = notifications(600)
    vi.mocked(fetchInboxNotifications).mockImplementation(async query => {
      const params = new URLSearchParams(query)
      const start = Number(params.get('cursor') ?? 0)
      const limit = Number(params.get('limit'))
      const end = Math.min(all.length, start + limit)
      return { notifications: all.slice(start, end), unreadCount: 600, nextCursor: end < all.length ? String(end) : undefined, hasMore: end < all.length }
    })
    const { result } = renderHook(() => usePagedInbox({ enabled: true, workspaceKey: 'acme', live, loadedIssues }))
    await waitFor(() => expect(result.current.notifications).toHaveLength(50))
    for (let page = 1; page < 12; page++) await act(async () => { await result.current.loadMore() })
    expect(result.current.notifications).toHaveLength(600)
    all[580] = { ...all[580], readAt: '2026-11-01T00:00:00Z' }
    vi.useFakeTimers()
    act(() => { inboxUnread.apply('acme', 'me', { revision: '1', unreadCount: 599, changedIds: ['row-580'], invalidated: false }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(501) })
    expect(fetchInboxNotifications).toHaveBeenCalledWith('?limit=500')
    expect(fetchInboxNotifications).toHaveBeenLastCalledWith('?limit=100&cursor=500')
    expect(result.current.notifications).toHaveLength(600)
    expect(result.current.notifications[580].readAt).toBeTruthy()
  })
})

import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchInboxNotifications } from '@/lib/api'
import { inboxRealtimeRelevant, inboxUnread } from './inbox-unread'

vi.mock('@/lib/api', () => ({ fetchInboxNotifications: vi.fn(async () => ({ notifications: [], unreadCount: 4 })) }))

afterEach(() => { vi.useRealTimers() })

describe('inbox unread (paged workspaces)', () => {
  it('refreshes on notification events and on activity by others, not on presence or own changes', () => {
    expect(inboxRealtimeRelevant({ type: 'notification.read', actorId: 'me' }, 'me')).toBe(true)
    expect(inboxRealtimeRelevant({ type: 'notifications.batch_updated', actorId: 'me' }, 'me')).toBe(true)
    expect(inboxRealtimeRelevant({ type: 'comment.created', actorId: 'other' }, 'me')).toBe(true)
    expect(inboxRealtimeRelevant({ type: 'pulse.summary_scheduled', actorId: '' }, 'me')).toBe(true)
    expect(inboxRealtimeRelevant({ type: 'comment.created', actorId: 'me' }, 'me')).toBe(false)
    expect(inboxRealtimeRelevant({ type: 'presence.updated', actorId: 'other' }, 'me')).toBe(false)
  })

  it('a steady event stream costs one count request per window', async () => {
    vi.useFakeTimers()
    for (let index = 0; index < 10; index++) { inboxUnread.schedule('acme', 2000); await vi.advanceTimersByTimeAsync(300) }
    // 3s of events every 300ms: the 2s window fired once instead of being postponed.
    expect(fetchInboxNotifications).toHaveBeenCalledTimes(1)
    expect(fetchInboxNotifications).toHaveBeenCalledWith('?limit=1')
    await vi.advanceTimersByTimeAsync(2000)
    expect(inboxUnread.state).toEqual({ count: 4, workspaceKey: 'acme' })
  })
})

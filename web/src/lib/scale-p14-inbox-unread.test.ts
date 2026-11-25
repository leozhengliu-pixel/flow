import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchInboxNotifications } from '@/lib/api'
import { request } from '@/lib/api-client'
import { INBOX_STATE_CAPABILITY, INBOX_STATE_EVENT, inboxRealtimeRelevant, inboxUnread, type InboxNotificationState } from './inbox-unread'

vi.mock('@/lib/api', () => ({ fetchInboxNotifications: vi.fn(async () => ({ notifications: [], unreadCount: 4 })) }))
vi.mock('@/lib/api-client', () => ({ request: vi.fn(async () => ({ revision: '1', unreadCount: 4, invalidated: false })) }))

beforeEach(() => { inboxUnread.configure(); vi.clearAllMocks(); vi.useFakeTimers() })
afterEach(() => { inboxUnread.configure(); vi.useRealTimers() })

const configure = (viewerId = 'me', workspaceKey = 'acme') => inboxUnread.configure({ workspaceKey, viewerId, capabilities: [INBOX_STATE_CAPABILITY] })
const overlay = (revision: string, unreadCount = 5, invalidated = false): InboxNotificationState => ({ revision, unreadCount, invalidated })

describe('P14 unread fan-out baseline', () => {
  it('measures the legacy read demand for 2,000 viewers at one event per ten seconds', () => {
    const event = { type: 'comment.created', actorId: 'writer' }
    const viewers = Array.from({ length: 2000 }, (_, index) => `viewer-${index}`)
    const reads = viewers.filter(viewer => inboxRealtimeRelevant(event, viewer)).length
    console.info(`P14 baseline: ${reads} unread recovery reads/event; ${reads / 10} reads/s`)
    expect(reads).toBe(2000)
  })

  it('measures zero GETs for normal pushes and at most five targeted recoveries per event', async () => {
    let legacyReads = 0
    for (let index = 0; index < 2000; index++) {
      const viewer = `viewer-${index}`
      configure(viewer)
      if (inboxRealtimeRelevant({ type: 'comment.created', actorId: 'writer' }, viewer)) legacyReads++
      if (index < 5) inboxUnread.receive('acme', viewer, { type: 'notification.state', aggregateId: viewer, payload: overlay('1') })
      await vi.advanceTimersByTimeAsync(10000)
    }
    expect(legacyReads).toBe(0)
    expect(request).not.toHaveBeenCalled()
    expect(fetchInboxNotifications).not.toHaveBeenCalled()
    for (let index = 0; index < 5; index++) {
      const viewer = `recipient-${index}`
      configure(viewer)
      for (let duplicate = 0; duplicate < 4; duplicate++) inboxUnread.receive('acme', viewer, { type: 'notification.state', aggregateId: viewer, payload: overlay('1', 5, true) })
      await vi.advanceTimersByTimeAsync(1)
    }
    expect(request).toHaveBeenCalledTimes(5)
    expect(request).toHaveBeenLastCalledWith('/api/notifications/state', expect.objectContaining({ headers: { 'X-Workspace-Key': 'acme' }, signal: expect.any(AbortSignal) }))
    console.info('P14 overlay: 0 GETs for normal push; 5 targeted recoveries/event = 0.5/s')
  })

  it('keeps legacy refresh behavior when the capability is absent or disabled', async () => {
    configure()
    inboxUnread.configure({ workspaceKey: 'acme', viewerId: 'me', capabilities: [] })
    expect(inboxRealtimeRelevant({ type: 'comment.created', actorId: 'other' }, 'me')).toBe(true)
    inboxUnread.schedule('acme', 300)
    await vi.advanceTimersByTimeAsync(301)
    expect(fetchInboxNotifications).toHaveBeenCalledWith('?limit=1')
    expect(inboxUnread.state).toEqual({ count: 4, workspaceKey: 'acme' })
    expect(request).not.toHaveBeenCalled()
  })

  it('falls back when an older replica has no recovery endpoint', async () => {
    configure()
    vi.mocked(request).mockRejectedValueOnce({ status: 404 })
    await inboxUnread.refresh('acme')
    await vi.advanceTimersByTimeAsync(1)
    expect(inboxUnread.overlayEnabled('acme')).toBe(false)
    expect(fetchInboxNotifications).toHaveBeenCalledTimes(1)
    expect(inboxRealtimeRelevant({ type: 'comment.created', actorId: 'other' }, 'me')).toBe(true)
  })

  it('refreshes immediately on capability removal while retaining the same viewer count', async () => {
    configure()
    inboxUnread.apply('acme', 'me', overlay('1', 9))
    inboxUnread.configure({ workspaceKey: 'acme', viewerId: 'me', capabilities: [] })
    expect(inboxUnread.state.count).toBe(9)
    await vi.advanceTimersByTimeAsync(1)
    expect(fetchInboxNotifications).toHaveBeenCalledWith('?limit=1')
    expect(inboxUnread.state.count).toBe(4)
  })

  it('resets an authorization epoch even when viewer and workspace stay the same', () => {
    configure()
    inboxUnread.apply('acme', 'me', overlay('100', 20))
    inboxUnread.configure({ workspaceKey: 'acme', viewerId: 'me', capabilities: [INBOX_STATE_CAPABILITY], authzEpoch: 'new-policy' })
    expect(inboxUnread.state.count).toBe(0)
    expect(inboxUnread.apply('acme', 'me', overlay('1', 2))).toBe(true)
    expect(inboxUnread.state.count).toBe(2)
  })

  it('invalidates an open window on the first recovery after scope reset', async () => {
    configure()
    const changed = vi.fn()
    window.addEventListener(INBOX_STATE_EVENT, changed)
    try {
      await inboxUnread.refresh('acme')
      expect(changed.mock.calls[0][0].detail.state.invalidated).toBe(true)
    } finally { window.removeEventListener(INBOX_STATE_EVENT, changed) }
  })

  it('orders decimal revisions without numeric precision loss and suppresses replay', () => {
    configure()
    expect(inboxUnread.apply('acme', 'me', overlay('9007199254740993', 7))).toBe(true)
    expect(inboxUnread.apply('acme', 'me', overlay('9007199254740992', 2))).toBe(false)
    expect(inboxUnread.apply('acme', 'me', overlay('9007199254740993', 2))).toBe(false)
    expect(inboxUnread.state.count).toBe(7)
  })

  it('recovers a missed recipient revision and ignores invalidation replay after recovery', async () => {
    configure()
    inboxUnread.apply('acme', 'me', overlay('1', 1))
    vi.mocked(request).mockResolvedValueOnce(overlay('3', 3))
    inboxUnread.apply('acme', 'me', overlay('3', 3))
    await vi.advanceTimersByTimeAsync(1)
    expect(request).toHaveBeenCalledTimes(1)
    inboxUnread.apply('acme', 'me', overlay('3', 3, true))
    await vi.advanceTimersByTimeAsync(1000)
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('rejects wrong recipients, workspaces and malformed payloads without querying others', async () => {
    configure()
    expect(inboxUnread.apply('other', 'me', overlay('1'))).toBe(false)
    expect(inboxUnread.apply('acme', 'other', overlay('1'))).toBe(false)
    inboxUnread.receive('acme', 'me', { type: 'notification.state', aggregateId: 'other', payload: overlay('1', 1, true) })
    await vi.advanceTimersByTimeAsync(1)
    expect(request).not.toHaveBeenCalled()
    for (const bad of [{ revision: 1 }, { revision: '-1' }, { unreadCount: -2 }, { changedIds: new Array(51).fill('id') }]) {
      expect(inboxUnread.apply('acme', 'me', { ...overlay('1'), ...bad } as InboxNotificationState)).toBe(false)
    }
    expect(inboxUnread.state.count).toBe(0)
  })

  it('does not let stale recovery or unversioned page counts overwrite a push', async () => {
    configure()
    let resolve!: (state: InboxNotificationState) => void
    vi.mocked(request).mockImplementationOnce(() => new Promise(resolver => { resolve = resolver as typeof resolve }))
    const refreshing = inboxUnread.refresh('acme')
    inboxUnread.apply('acme', 'me', overlay('2', 9))
    inboxUnread.set(0, 'acme')
    resolve(overlay('1', 2))
    await refreshing
    expect(inboxUnread.state.count).toBe(9)
  })

  it('isolates inflight requests and timers across account and workspace switches', async () => {
    configure()
    let resolve!: (state: InboxNotificationState) => void
    vi.mocked(request).mockImplementationOnce(() => new Promise(resolver => { resolve = resolver as typeof resolve }))
    const refreshing = inboxUnread.refresh('acme')
    const signal = vi.mocked(request).mock.calls.at(-1)?.[1]?.signal
    inboxUnread.schedule('acme', 300)
    configure('someone-else', 'other')
    expect(signal?.aborted).toBe(true)
    inboxUnread.apply('other', 'someone-else', overlay('1', 8))
    resolve(overlay('99', 100))
    await refreshing
    await vi.advanceTimersByTimeAsync(1000)
    expect(inboxUnread.state).toEqual({ count: 8, workspaceKey: 'other' })
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('notifies an open inbox for changed IDs and permits recovery at the same revision after snooze expiry', async () => {
    configure()
    const changed = vi.fn()
    window.addEventListener(INBOX_STATE_EVENT, changed)
    try {
      inboxUnread.apply('acme', 'me', { ...overlay('1', 0), changedIds: ['snoozed-row'] })
      vi.mocked(request).mockResolvedValueOnce(overlay('1', 1))
      await inboxUnread.refresh('acme')
      expect(inboxUnread.state.count).toBe(1)
      expect(changed).toHaveBeenCalledTimes(2)
    } finally { window.removeEventListener(INBOX_STATE_EVENT, changed) }
  })
})

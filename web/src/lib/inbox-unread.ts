import { useEffect, useSyncExternalStore } from 'react'
import { fetchInboxNotifications } from '@/lib/api'

/**
 * Inbox unread count for paged workspaces. The paged bootstrap ships no
 * notifications, so the sidebar badge reads the server's count
 * (GET /api/notifications → unreadCount) and refreshes it on realtime activity.
 */
type State = { count: number; workspaceKey?: string }

let state: State = { count: 0 }
const listeners = new Set<() => void>()
let inflight: Promise<void> | undefined
let queued = false
let timer: ReturnType<typeof setTimeout> | undefined
let due = 0

function set(next: State) {
  if (next.count === state.count && next.workspaceKey === state.workspaceKey) return
  state = next
  for (const listener of listeners) listener()
}

/** Dispatched by the app for realtime events that can change the viewer's inbox. */
export const INBOX_ACTIVITY_EVENT = 'flow:inbox-activity'

/**
 * New notifications are written inside other entity events (issue, comment,
 * update, Pulse summary delivery …), so any event caused by someone else may
 * add one; the viewer's own read/archive/snooze arrive as notification events.
 */
export function inboxRealtimeRelevant(event: { type: string; actorId?: string }, viewerId: string) {
  if (/^notifications?\./.test(event.type)) return true
  if (event.type === 'presence.updated' || event.type.startsWith('favorite') || event.type.startsWith('subscription.')) return false
  // Loop run progress signals never create notifications.
  if (event.type.startsWith('loop_run.')) return false
  return event.actorId !== viewerId
}

export const inboxUnread = {
  get state() { return state },
  subscribe(listener: () => void) {
    listeners.add(listener)
    return () => { listeners.delete(listener) }
  },
  async refresh(workspaceKey: string) {
    if (inflight) { queued = true; return inflight }
    inflight = fetchInboxNotifications('?limit=1')
      .then(result => set({ count: Math.max(0, result.unreadCount ?? 0), workspaceKey }))
      .catch(() => undefined)
      .finally(() => {
        inflight = undefined
        if (queued) { queued = false; void inboxUnread.refresh(workspaceKey) }
      })
    return inflight
  },
  /**
   * Refreshes within `delay`. A pending refresh is kept (only moved earlier),
   * so a steady stream of events costs one request per window instead of
   * postponing it forever.
   */
  schedule(workspaceKey: string, delay = 600) {
    const at = Date.now() + delay
    if (timer && due <= at) return
    if (timer) clearTimeout(timer)
    due = at
    timer = setTimeout(() => { timer = undefined; void inboxUnread.refresh(workspaceKey) }, delay)
  },
  set(count: number, workspaceKey: string) { set({ count: Math.max(0, count), workspaceKey }) },
}

/** The server's unread count while `enabled` (paged workspaces); undefined otherwise. */
export function useInboxUnread(workspaceKey: string | undefined, enabled: boolean) {
  const snapshot = useSyncExternalStore(inboxUnread.subscribe, () => state, () => state)
  useEffect(() => {
    if (!enabled || !workspaceKey) return
    void inboxUnread.refresh(workspaceKey)
    const onFocus = () => inboxUnread.schedule(workspaceKey, 0)
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [enabled, workspaceKey])
  if (!enabled) return undefined
  return snapshot.workspaceKey === workspaceKey ? snapshot.count : 0
}

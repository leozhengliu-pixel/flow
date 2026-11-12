import { useEffect, useSyncExternalStore } from 'react'
import { fetchInboxNotifications } from '@/lib/api'
import { request } from '@/lib/api-client'

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
let generation = 0
let countVersion = 0
let activeWorkspace: string | undefined
let recoveryAbort: AbortController | undefined

export const INBOX_STATE_CAPABILITY = 'recipient-inbox-v1'
export const INBOX_STATE_EVENT = 'flow:inbox-state'
export const INBOX_STATE_REALTIME_EVENT = 'notification.state'
export type InboxNotificationState = { revision: string; unreadCount: number; changedIds?: string[]; invalidated: boolean }
export type InboxStateActivity = { workspaceKey: string; viewerId: string; state: InboxNotificationState }
type OverlayScope = { workspaceKey: string; viewerId: string; revision?: string; invalidated?: boolean; recoveredRevision?: string }
let overlay: OverlayScope | undefined
let configured: { workspaceKey: string; viewerId: string; authzEpoch?: string } | undefined

function validState(value: unknown): value is InboxNotificationState {
  if (!value || typeof value !== 'object') return false
  const item = value as Partial<InboxNotificationState>
  return typeof item.revision === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(item.revision)
    && Number.isSafeInteger(item.unreadCount) && item.unreadCount! >= 0 && typeof item.invalidated === 'boolean'
    && (item.changedIds === undefined || Array.isArray(item.changedIds) && item.changedIds.length <= 50
      && item.changedIds.every(id => typeof id === 'string' && id.length > 0 && id.length <= 191))
}

function resetRequests(workspaceKey?: string) {
  generation++
  countVersion++
  activeWorkspace = workspaceKey
  recoveryAbort?.abort()
  recoveryAbort = undefined
  inflight = undefined
  queued = false
  if (timer) clearTimeout(timer)
  timer = undefined
}

function applyState(workspaceKey: string, viewerId: string, next: InboxNotificationState, recovery = false) {
  const current = overlay
  if (!current || current.workspaceKey !== workspaceKey || current.viewerId !== viewerId || !validState(next)) return false
  const sameRevision = current.revision === next.revision
  if (!recovery && sameRevision && current.recoveredRevision === next.revision) return false
  const recoveredChange = recovery && (!sameRevision || state.count !== next.unreadCount)
  const gap = current.revision !== undefined && BigInt(next.revision) > BigInt(current.revision) + 1n
  if (current.revision !== undefined) {
    const order = BigInt(next.revision) - BigInt(current.revision)
    if (order < 0n || order === 0n && !recovery && (!next.invalidated || current.invalidated)) return false
  }
  current.revision = next.revision
  current.invalidated = next.invalidated || gap
  if (recovery) current.recoveredRevision = next.revision
  countVersion++
  if (!sameRevision || recovery) set({ count: next.unreadCount, workspaceKey })
  window.dispatchEvent(new CustomEvent<InboxStateActivity>(INBOX_STATE_EVENT, {
    detail: { workspaceKey, viewerId, state: { ...next, invalidated: next.invalidated || recoveredChange || gap, changedIds: next.changedIds?.slice() } },
  }))
  if ((next.invalidated || gap) && !recovery) inboxUnread.schedule(workspaceKey, 0)
  return true
}

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
  if (overlay?.viewerId === viewerId) return false
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
  /** P13 configures this from each manifest and clears it on logout/epoch change. */
  configure(options?: { workspaceKey: string; viewerId: string; capabilities: readonly string[]; authzEpoch?: string }) {
    const enabled = Boolean(options?.capabilities.includes(INBOX_STATE_CAPABILITY))
    const next = enabled && options ? { workspaceKey: options.workspaceKey, viewerId: options.viewerId } : undefined
    if (options && configured?.workspaceKey === options.workspaceKey && configured.viewerId === options.viewerId && configured.authzEpoch === options.authzEpoch && Boolean(overlay) === enabled) return
    const sameIdentity = options && configured?.workspaceKey === options.workspaceKey && configured.viewerId === options.viewerId && configured.authzEpoch === options.authzEpoch
    const count = sameIdentity && state.workspaceKey === options.workspaceKey ? state.count : 0
    const restoreLegacy = overlay && !enabled && options
    resetRequests(options?.workspaceKey)
    overlay = next
    configured = options ? { workspaceKey: options.workspaceKey, viewerId: options.viewerId, authzEpoch: options.authzEpoch } : undefined
    set({ count, workspaceKey: options?.workspaceKey })
    if (restoreLegacy) inboxUnread.schedule(restoreLegacy.workspaceKey, 0)
  },
  overlayEnabled(workspaceKey: string, viewerId?: string) { return overlay?.workspaceKey === workspaceKey && (viewerId === undefined || overlay.viewerId === viewerId) },
  /** P13 forwards recipient frames here before its ordinary business-event path. */
  receive(workspaceKey: string, viewerId: string, event: { type: string; aggregateId?: string; payload?: unknown }) {
    if (overlay?.workspaceKey !== workspaceKey || overlay.viewerId !== viewerId || event.type !== INBOX_STATE_REALTIME_EVENT) return false
    if (event.aggregateId !== viewerId) return true
    if (!validState(event.payload)) { inboxUnread.schedule(workspaceKey, 0); return true }
    applyState(workspaceKey, viewerId, event.payload)
    return true
  },
  apply(workspaceKey: string, viewerId: string, next: InboxNotificationState) { return applyState(workspaceKey, viewerId, next) },
  async refresh(workspaceKey: string) {
    if (configured && configured.workspaceKey !== workspaceKey) return
    if (activeWorkspace !== workspaceKey) resetRequests(workspaceKey)
    if (inflight) { queued = true; return inflight }
    const run = generation
    const version = countVersion
    const scope = overlay?.workspaceKey === workspaceKey ? overlay : undefined
    const controller = scope ? new AbortController() : undefined
    recoveryAbort = controller
    const response = scope
      ? request<InboxNotificationState>('/api/notifications/state', { headers: { 'X-Workspace-Key': workspaceKey }, signal: controller?.signal }).then(result => {
        if (generation !== run || countVersion !== version || !validState(result)) return
        applyState(workspaceKey, scope.viewerId, result, true)
      })
      : fetchInboxNotifications('?limit=1').then(result => {
        if (generation === run && countVersion === version) set({ count: Math.max(0, result.unreadCount ?? 0), workspaceKey })
      })
    inflight = response
      .catch((error: unknown) => {
        if (generation !== run || !scope || !error || typeof error !== 'object' || !('status' in error)) return
        if (error.status === 404 || error.status === 405 || error.status === 501) {
          // A server without the negotiated endpoint restores the old path.
          inboxUnread.configure({ workspaceKey, viewerId: scope.viewerId, capabilities: [], authzEpoch: configured?.authzEpoch })
          inboxUnread.schedule(workspaceKey, 0)
        }
      })
      .finally(() => {
        if (generation !== run) return
        recoveryAbort = undefined
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
    if (configured && configured.workspaceKey !== workspaceKey) return
    if (activeWorkspace !== workspaceKey) resetRequests(workspaceKey)
    const at = Date.now() + delay
    if (timer && due <= at) return
    if (timer) clearTimeout(timer)
    due = at
    timer = setTimeout(() => { timer = undefined; void inboxUnread.refresh(workspaceKey) }, delay)
  },
  set(count: number, workspaceKey: string) {
    // Legacy notification pages carry no revision and can finish after a push.
    if (overlay || configured && configured.workspaceKey !== workspaceKey) return
    countVersion++
    set({ count: Math.max(0, count), workspaceKey })
  },
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

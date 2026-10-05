import { useEffect, useSyncExternalStore } from 'react'
import { fetchPulseUnread } from '@/lib/api'
import { pulsePath, pulseViewPath } from '@/lib/app-routes'
import type { PulseFeedView, PulseUnread } from '@/types/flow'

/**
 * Sidebar Pulse badge: unread For-me items newer than last seen, excluding
 * the viewer's own (server computed by GET /api/pulse/unread). Refreshed on
 * mount, on realtime update events and after the feed records "seen".
 */
type State = PulseUnread & { workspaceKey?: string }

let state: State = { count: 0 }
const listeners = new Set<() => void>()
let inflight: Promise<void> | undefined
let queued = false
let timer: ReturnType<typeof setTimeout> | undefined

function set(next: State) {
  if (next.count === state.count && next.latestAt === state.latestAt && next.workspaceKey === state.workspaceKey) return
  state = next
  for (const listener of listeners) listener()
}

export const pulseUnread = {
  get state() { return state },
  subscribe(listener: () => void) {
    listeners.add(listener)
    return () => { listeners.delete(listener) }
  },
  /** Fetches the count now (coalesces concurrent refreshes). */
  async refresh(workspaceKey: string) {
    if (inflight) { queued = true; return inflight }
    inflight = fetchPulseUnread()
      .then(result => set({ count: Math.max(0, result.count ?? 0), latestAt: result.latestAt, workspaceKey }))
      .catch(() => undefined)
      .finally(() => {
        inflight = undefined
        if (queued) { queued = false; void pulseUnread.refresh(workspaceKey) }
      })
    return inflight
  },
  /** Debounced refresh for bursts of realtime events. */
  schedule(workspaceKey: string, delay = 400) {
    if (timer) clearTimeout(timer)
    timer = setTimeout(() => { timer = undefined; void pulseUnread.refresh(workspaceKey) }, delay)
  },
  /** The feed recorded "seen" at `at`; drop the badge immediately. */
  markSeen(at: string) {
    if (!state.latestAt || new Date(state.latestAt).getTime() <= new Date(at).getTime()) set({ ...state, count: 0 })
  },
  set(next: PulseUnread, workspaceKey?: string) { set({ ...next, workspaceKey: workspaceKey ?? state.workspaceKey }) },
}

export function usePulseUnread(workspaceKey: string | undefined, enabled: boolean) {
  const snapshot = useSyncExternalStore(pulseUnread.subscribe, () => state, () => state)
  useEffect(() => {
    if (!enabled || !workspaceKey) return
    void pulseUnread.refresh(workspaceKey)
    const onFocus = () => pulseUnread.schedule(workspaceKey, 0)
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [enabled, workspaceKey])
  return enabled && snapshot.workspaceKey === workspaceKey ? snapshot.count : 0
}

/** Realtime event types that can change the Pulse feed. */
export function isPulseRealtimeEvent(type: string) {
  return /^(project|initiative)\.update_|^(project|initiative)_update\.|^pulse\./.test(type)
}

/* Last used Pulse tab, for the sidebar click target. */
const LAST_TAB_KEY = 'flow.pulse.lastTab'
type PulseTab = Exclude<PulseFeedView, 'created'> | `view:${string}`

export function readPulseLastTab(workspaceKey: string): PulseTab {
  try {
    const stored = JSON.parse(localStorage.getItem(LAST_TAB_KEY) ?? '{}') as Record<string, string>
    const value = stored[workspaceKey]
    if (value === 'following' || value === 'popular' || value === 'all' || value?.startsWith('view:')) return value as PulseTab
  } catch { /* default below */ }
  return 'all'
}

export function writePulseLastTab(workspaceKey: string, tab: PulseTab) {
  try {
    const stored = JSON.parse(localStorage.getItem(LAST_TAB_KEY) ?? '{}') as Record<string, string>
    stored[workspaceKey] = tab
    localStorage.setItem(LAST_TAB_KEY, JSON.stringify(stored))
  } catch { /* in-memory only */ }
}

/** Linear: unread → For me; otherwise the last used tab (default Recent). */
export function pulseSidebarTarget(workspaceKey: string, unread: number): PulseTab {
  return unread > 0 ? 'following' : readPulseLastTab(workspaceKey)
}

export function pulseSidebarPath(workspaceKey: string, unread: number) {
  const target = pulseSidebarTarget(workspaceKey, unread)
  return target.startsWith('view:') ? pulseViewPath(workspaceKey, target.slice(5)) : pulsePath(workspaceKey, target as Exclude<PulseFeedView, 'created'>)
}

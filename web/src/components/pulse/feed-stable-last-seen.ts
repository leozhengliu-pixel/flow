/**
 * LS-0270 — FeedStableLastSeen.
 * Stable last-seen cursor for Pulse/Feed across tab switches within a session.
 * Persist via userSettings.feedLastSeenTime (neighbor of useFeedView).
 */

type Listener = () => void

let published: number | null = null
const listeners = new Set<Listener>()

function notify() {
  for (const listener of listeners) listener()
}

export const FeedStableLastSeen = {
  /** Published stable cursor, or null when cleared / never published. */
  get published(): number | null {
    return published
  },

  /**
   * Effective last-seen time used for unread / separators.
   * Falls back to persisted/settings time when nothing has been published yet.
   */
  effectiveLastSeenTime(fallback?: number | string | Date | null): number {
    if (published != null) return published
    if (fallback == null || fallback === '') return 0
    if (typeof fallback === 'number') return fallback
    const ms = new Date(fallback).getTime()
    return Number.isFinite(ms) ? ms : 0
  },

  publish(time: number | string | Date = Date.now()) {
    const ms = typeof time === 'number' ? time : new Date(time).getTime()
    if (!Number.isFinite(ms)) return
    published = ms
    notify()
  },

  clear() {
    published = null
    notify()
  },

  subscribe(listener: Listener) {
    listeners.add(listener)
    return () => {
      listeners.delete(listener)
    }
  },
}

export type FeedStableLastSeenApi = typeof FeedStableLastSeen

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { fetchPulseFeed, markPulseSeen } from '@/lib/api'
import type { InitiativeUpdate, ProjectUpdate, PulseFeedView, PulseItem } from '@/types/flow'
import { FeedStableLastSeen } from './feed-stable-last-seen'
import { nextPulseSeen, pulseFilterParam, pulseLastSeenItemId, type PulseViewConfig } from './pulse-model'
import { pulseUnread } from './pulse-unread'

/** Linear shows the first 100 feed items. */
export const PULSE_FEED_MAX_ITEMS = 100
export const PULSE_FEED_PAGE_SIZE = 30

export type PulseFeedQueryState = {
  view: Exclude<PulseFeedView, 'created'>
  viewId?: string
  q: string
  config: PulseViewConfig
}

let lastPostedSeen = 0

/** POST /api/pulse/seen, never moving the cursor backwards (the server enforces it too). */
export function recordPulseSeen(at: number = Date.now(), options?: { keepalive?: boolean; force?: boolean }) {
  const next = nextPulseSeen(lastPostedSeen, at)
  if (!next) return false
  // While reading, one write per 15s is plenty; leaving the page always writes.
  if (!options?.force && lastPostedSeen && next - lastPostedSeen < 15_000) return false
  lastPostedSeen = next
  const iso = new Date(next).toISOString()
  pulseUnread.markSeen(iso)
  void markPulseSeen(iso, { keepalive: options?.keepalive }).catch(() => undefined)
  return true
}

/** Test hook. */
export function resetPulseSeenForTests() { lastPostedSeen = 0 }

function sameIds(left: PulseItem[], right: PulseItem[]) {
  return left.length === right.length && left.every((item, index) => item.id === right[index].id)
}

export function usePulseFeed(query: PulseFeedQueryState, options: { enabled?: boolean; workspaceKey: string }) {
  const [items, setItems] = useState<PulseItem[]>([])
  const [nextCursor, setNextCursor] = useState<string>()
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string>()
  const [unreadCount, setUnreadCount] = useState(0)
  const [newIds, setNewIds] = useState<string[]>([])
  const [, bump] = useState(0)
  const itemsRef = useRef(items)
  itemsRef.current = items
  const cursorRef = useRef(nextCursor)
  cursorRef.current = nextCursor
  const generation = useRef(0)
  const abortRef = useRef<AbortController | undefined>(undefined)
  const filter = useMemo(() => pulseFilterParam(query.config), [query.config])
  const filterKey = JSON.stringify(filter ?? null)
  const enabled = options.enabled !== false
  const request = useCallback((cursor?: string, signal?: AbortSignal) => fetchPulseFeed({
    view: query.view,
    viewId: query.viewId,
    q: query.q,
    filter,
    cursor,
    limit: PULSE_FEED_PAGE_SIZE,
  }, signal), [filter, query.q, query.view, query.viewId])

  useEffect(() => FeedStableLastSeen.subscribe(() => bump(value => value + 1)), [])

  const load = useCallback(async () => {
    const run = ++generation.current
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller
    setLoading(true)
    setError(undefined)
    try {
      const page = await request(undefined, controller.signal)
      if (run !== generation.current) return
      // The stable cursor survives tab switches; the first response of a visit publishes it.
      if (FeedStableLastSeen.published == null) FeedStableLastSeen.publish(page.lastSeenAt ? new Date(page.lastSeenAt).getTime() : 0)
      setItems(page.items.slice(0, PULSE_FEED_MAX_ITEMS))
      setNextCursor(page.nextCursor)
      setUnreadCount(page.unreadCount ?? 0)
      setNewIds([])
    } catch (cause) {
      if (run === generation.current) setError(cause instanceof Error ? cause.message : 'Could not load Pulse')
    } finally {
      if (run === generation.current) setLoading(false)
    }
  }, [request])

  const cancel = useCallback(() => { generation.current++; abortRef.current?.abort() }, [])
  useEffect(() => {
    if (!enabled) return
    void load()
    return cancel
  }, [cancel, enabled, filterKey, load])

  const loadMore = useCallback(async () => {
    const cursor = cursorRef.current
    if (!cursor || loadingMore || itemsRef.current.length >= PULSE_FEED_MAX_ITEMS) return
    const run = generation.current
    setLoadingMore(true)
    try {
      const page = await request(cursor)
      if (run !== generation.current) return
      setItems(current => {
        const seen = new Set(current.map(item => item.id))
        return [...current, ...page.items.filter(item => !seen.has(item.id))].slice(0, PULSE_FEED_MAX_ITEMS)
      })
      setNextCursor(page.nextCursor)
    } catch { /* keep what is shown; the sentinel retries on the next scroll */ }
    finally { if (run === generation.current) setLoadingMore(false) }
  }, [loadingMore, request])

  /** Realtime: re-read the first page, refresh shown cards, count (not insert) new ones. */
  const probe = useCallback(async () => {
    const run = generation.current
    try {
      const page = await request()
      if (run !== generation.current) return
      const fresh = new Map(page.items.map(item => [item.id, item]))
      const current = itemsRef.current
      const known = new Set(current.map(item => item.id))
      const newest = current[0] ? new Date(current[0].update.createdAt).getTime() : 0
      const oldestFresh = page.items.length ? new Date(page.items[page.items.length - 1].update.createdAt).getTime() : Infinity
      const chronological = query.view !== 'popular'
      const next = current
        // Keep cards the reader just unsubscribed from (they turn into the "Unsubscribed" note).
        .filter(item => fresh.has(item.id) || !item.subscribed || !chronological || (page.nextCursor !== undefined && new Date(item.update.createdAt).getTime() < oldestFresh))
        .map(item => fresh.get(item.id) ?? item)
      if (!sameIds(next, current) || next.some((item, index) => item !== current[index])) setItems(next)
      setUnreadCount(page.unreadCount ?? 0)
      setNewIds(page.items.filter(item => !known.has(item.id) && (!chronological || new Date(item.update.createdAt).getTime() > newest)).map(item => item.id))
    } catch { /* the next event retries */ }
  }, [query.view, request])

  useEffect(() => {
    if (!enabled) return
    let timer: ReturnType<typeof setTimeout> | undefined
    const onActivity = () => {
      if (timer) clearTimeout(timer)
      timer = setTimeout(() => void probe(), 500)
    }
    window.addEventListener('flow:pulse-activity', onActivity)
    window.addEventListener('flow:pulse-subscriptions-changed', onActivity)
    return () => {
      if (timer) clearTimeout(timer)
      window.removeEventListener('flow:pulse-activity', onActivity)
      window.removeEventListener('flow:pulse-subscriptions-changed', onActivity)
    }
  }, [enabled, probe])

  const lastSeen = FeedStableLastSeen.effectiveLastSeenTime(0)
  const lastSeenFeedItemId = useMemo(
    () => (query.view === 'popular' ? undefined : pulseLastSeenItemId(items, lastSeen)),
    [items, lastSeen, query.view],
  )

  const patchUpdate = useCallback((itemId: string, update: ProjectUpdate | InitiativeUpdate) => {
    setItems(current => current.map(item => item.id === itemId ? { ...item, update } as PulseItem : item))
  }, [])
  const removeItem = useCallback((itemId: string) => setItems(current => current.filter(item => item.id !== itemId)), [])
  const setSubscribed = useCallback((sourceId: string, subscribed: boolean) => {
    setItems(current => current.map(item => item.source.id === sourceId ? { ...item, subscribed } : item))
  }, [])

  /** "New updates available": show them and drop the stable last-seen cursor (Linear's reset). */
  const showNewItems = useCallback(async () => {
    FeedStableLastSeen.publish(Date.now())
    await load()
  }, [load])

  return {
    items,
    loading,
    loadingMore,
    error,
    hasMore: Boolean(nextCursor) && items.length < PULSE_FEED_MAX_ITEMS,
    unreadCount,
    newItemsCount: newIds.length,
    lastSeen,
    lastSeenFeedItemId,
    reload: load,
    loadMore,
    showNewItems,
    patchUpdate,
    removeItem,
    setSubscribed,
  }
}

export type PulseFeedState = ReturnType<typeof usePulseFeed>

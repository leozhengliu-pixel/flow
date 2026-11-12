import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useLatestRef } from '@/hooks/use-latest-ref'
import { fetchInboxNotifications, listIssueRecords } from '@/lib/api'
import { INBOX_ACTIVITY_EVENT, INBOX_STATE_EVENT, inboxUnread, type InboxStateActivity } from '@/lib/inbox-unread'
import type { Issue, Notification, RealtimeEvent } from '@/types/flow'

/** Notifications per page (the server caps a page at 500). */
export const INBOX_PAGE_SIZE = 50
const MAX_RELOAD = 500
const ISSUE_BATCH = 100

type PageState = { items: Notification[]; cursor?: string; hasMore: boolean; loading: boolean; loadingMore: boolean; error: boolean }

function upsertAll(current: Notification[], incoming: Notification[]) {
  if (!incoming.length) return current
  const index = new Map(current.map((item, position) => [item.id, position]))
  const next = [...current]
  const added: Notification[] = []
  for (const item of incoming) {
    const position = index.get(item.id)
    if (position === undefined) added.push(item)
    else next[position] = item
  }
  return added.length ? [...added, ...next] : next
}

function query(limit: number, cursor?: string) {
  const params = new URLSearchParams({ limit: String(limit) })
  if (cursor) params.set('cursor', cursor)
  return `?${params}`
}

/**
 * Paged workspaces: the bootstrap ships no notifications, so the inbox reads
 * GET /api/notifications page by page, merges realtime notification patches
 * (`live`, the app's data.notifications) and action responses, re-reads the
 * first page on realtime activity, and loads the summaries of issues the
 * loaded notifications point at that the app does not hold.
 */
export function usePagedInbox({ enabled, workspaceKey, live, loadedIssues }: { enabled: boolean; workspaceKey: string; live: Notification[]; loadedIssues: Issue[] }) {
  const [page, setPage] = useState<PageState>({ items: [], hasMore: false, loading: enabled, loadingMore: false, error: false })
  const pageRef = useLatestRef(page)
  const generation = useRef(0)

  const applyCount = useCallback((unreadCount: number | undefined) => {
    if (typeof unreadCount === 'number') inboxUnread.set(unreadCount, workspaceKey)
  }, [workspaceKey])

  const reload = useCallback(async () => {
    const run = ++generation.current
    setPage(current => ({ ...current, loading: true, error: false }))
    try {
      const requested = Math.max(INBOX_PAGE_SIZE, pageRef.current.items.length)
      const recipientOverlay = inboxUnread.overlayEnabled(workspaceKey)
      const target = recipientOverlay ? requested : Math.min(MAX_RELOAD, requested)
      const items: Notification[] = []
      let cursor: string | undefined
      let hasMore = false
      do {
        const result = await fetchInboxNotifications(query(Math.min(MAX_RELOAD, target - items.length), cursor))
        if (run !== generation.current) return
        applyCount(result.unreadCount)
        items.push(...(result.notifications ?? []))
        hasMore = Boolean(result.hasMore ?? result.nextCursor)
        if (recipientOverlay && (cursor === result.nextCursor || !result.notifications?.length)) hasMore = false
        cursor = result.nextCursor
      } while (recipientOverlay && items.length < target && hasMore && cursor)
      setPage({ items, cursor, hasMore, loading: false, loadingMore: false, error: false })
    } catch {
      if (run === generation.current) setPage(current => ({ ...current, loading: false, error: true }))
    }
  }, [applyCount, pageRef, workspaceKey])

  const cancel = useCallback(() => { generation.current++ }, [])
  useEffect(() => {
    if (!enabled) return
    // reload() flips the loading state up front; start it from a microtask so the effect itself never sets state.
    queueMicrotask(() => { void reload() })
    return cancel
  }, [cancel, enabled, reload, workspaceKey])

  const loadMore = useCallback(async () => {
    const { cursor, loadingMore } = pageRef.current
    if (!cursor || loadingMore) return
    const run = generation.current
    setPage(current => ({ ...current, loadingMore: true }))
    try {
      const result = await fetchInboxNotifications(query(INBOX_PAGE_SIZE, cursor))
      if (run !== generation.current) return
      setPage(current => ({ ...current, items: [...current.items, ...(result.notifications ?? []).filter(item => !current.items.some(existing => existing.id === item.id))], cursor: result.nextCursor, hasMore: Boolean(result.hasMore ?? result.nextCursor), loadingMore: false }))
    } catch {
      if (run === generation.current) setPage(current => ({ ...current, loadingMore: false }))
    }
  }, [pageRef])

  /** Re-reads the first page and merges it in (new rows on top, older pages kept). */
  const refreshFirstPage = useCallback(async () => {
    const run = generation.current
    try {
      const result = await fetchInboxNotifications(query(INBOX_PAGE_SIZE))
      if (run !== generation.current) return
      applyCount(result.unreadCount)
      setPage(current => ({ ...current, items: upsertAll(current.items, result.notifications ?? []) }))
    } catch { /* the next event retries */ }
  }, [applyCount])

  const upsert = useCallback((notification: Notification | undefined) => {
    if (!notification || typeof notification !== 'object' || !('id' in notification)) return
    setPage(current => ({ ...current, items: upsertAll(current.items, [notification]) }))
  }, [])

  // Realtime notification patches land in the app's data.notifications.
  const seenLive = useRef(new Map<string, Notification>())
  useEffect(() => {
    if (!enabled) return
    const changed = live.filter(item => seenLive.current.get(item.id) !== item)
    for (const item of changed) seenLive.current.set(item.id, item)
    if (changed.length) setPage(current => ({ ...current, items: upsertAll(current.items, changed) }))
  }, [enabled, live])

  // Other realtime activity: batch updates re-read what is loaded; anything else re-reads the first page.
  useEffect(() => {
    if (!enabled) return
    let timer: ReturnType<typeof setTimeout> | undefined
    let full = false
    const onActivity = (event: Event) => {
      if (inboxUnread.overlayEnabled(workspaceKey)) return
      const detail = (event as CustomEvent<RealtimeEvent>).detail
      const entity = detail?.payload?.entity as Notification | undefined
      if (detail?.type?.startsWith('notification.') && entity && 'recipientId' in entity) { upsert(entity); return }
      if (detail?.type?.startsWith('notifications.')) full = true
      // One re-read per window while events keep coming (never postponed indefinitely).
      if (timer) return
      timer = setTimeout(() => {
        timer = undefined
        const reloadAll = full
        full = false
        void (reloadAll ? reload() : refreshFirstPage())
      }, 500)
    }
    const onState = (event: Event) => {
      const detail = (event as CustomEvent<InboxStateActivity>).detail
      if (detail?.workspaceKey !== workspaceKey || !inboxUnread.overlayEnabled(workspaceKey, detail.viewerId)) return
      const { state } = detail
      if (!state.invalidated && !state.changedIds?.length) return
      // A changed row can be on an older loaded page. Re-read that window;
      // fetching just page one would leave mark/read/archive state stale.
      full = full || state.invalidated || Boolean(state.changedIds?.some(id => pageRef.current.items.some(item => item.id === id)))
      if (timer) return
      timer = setTimeout(() => {
        timer = undefined
        const reloadAll = full
        full = false
        void (reloadAll ? reload() : refreshFirstPage())
      }, 500)
    }
    window.addEventListener(INBOX_ACTIVITY_EVENT, onActivity)
    window.addEventListener(INBOX_STATE_EVENT, onState)
    return () => {
      if (timer) clearTimeout(timer)
      window.removeEventListener(INBOX_ACTIVITY_EVENT, onActivity)
      window.removeEventListener(INBOX_STATE_EVENT, onState)
    }
  }, [enabled, pageRef, refreshFirstPage, reload, upsert, workspaceKey])

  // Issue summaries for notifications whose issue the app has not loaded.
  const [fetchedIssues, setFetchedIssues] = useState<Map<string, Issue>>(() => new Map())
  const requested = useRef(new Set<string>())
  const loadedIds = useMemo(() => new Set(loadedIssues.map(issue => issue.id)), [loadedIssues])
  useEffect(() => {
    if (!enabled) return
    const missing = [...new Set(page.items.map(item => item.issueId).filter((id): id is string => Boolean(id) && !loadedIds.has(id!) && !requested.current.has(id!)))]
    if (!missing.length) return
    missing.forEach(id => requested.current.add(id))
    for (let start = 0; start < missing.length; start += ISSUE_BATCH) {
      const ids = missing.slice(start, start + ISSUE_BATCH)
      void listIssueRecords({ filter: { field: 'id', operator: 'in', values: ids }, archived: 'all', limit: ids.length }, undefined, workspaceKey)
        .then(result => setFetchedIssues(current => {
          const next = new Map(current)
          for (const issue of result.items ?? []) next.set(issue.id, issue)
          return next
        }))
        .catch(() => { ids.forEach(id => requested.current.delete(id)) })
    }
  }, [enabled, loadedIds, page.items, workspaceKey])
  const issues = useMemo(() => {
    if (!enabled || !fetchedIssues.size) return loadedIssues
    return [...loadedIssues, ...[...fetchedIssues.values()].filter(issue => !loadedIds.has(issue.id))]
  }, [enabled, fetchedIssues, loadedIds, loadedIssues])

  return {
    notifications: page.items,
    issues,
    loading: page.loading,
    loadingMore: page.loadingMore,
    hasMore: page.hasMore,
    error: page.error,
    reload,
    loadMore,
    upsert,
  }
}

import { useEffect, useRef } from 'react'
import { Virtuoso, type Components, type VirtuosoHandle } from 'react-virtuoso'

import { InboxNotificationRow, type InboxNotificationRowData, type InboxNotificationRowProps } from './notification-row'

export interface InboxNotificationListProps extends Pick<InboxNotificationRowProps, 'onOpen' | 'onReadChange' | 'onDelete' | 'onSnooze' | 'onFavoriteChange' | 'onCopyLink' | 'onCopyIdentifier'> {
  notifications: InboxNotificationRowData[]
  filterHiddenCount?: number
  onClearFilters?: () => void
  selectedId?: string | null
  pending?: Record<string, unknown>
  hasMore?: boolean
  loadingMore?: boolean
  onLoadMore?: () => void
}

type InboxListContext = { filterHiddenCount: number; loadingMore: boolean; onClearFilters?: () => void }
const INBOX_VIRTUAL_COMPONENTS: Components<InboxNotificationRowData, InboxListContext> = { Footer: InboxVirtualFooter }

function InboxVirtualFooter({ context }: { context: InboxListContext }) {
  return <>{context.loadingMore ? <div className="flow-inbox-notification-list__loading" role="status">Loading…</div> : null}{context.filterHiddenCount > 0 ? <InboxFilterNotice hiddenCount={context.filterHiddenCount} onClear={context.onClearFilters} /> : null}</>
}

export function InboxNotificationList({ notifications, selectedId = null, pending, hasMore = false, loadingMore = false, onLoadMore, filterHiddenCount = 0, onClearFilters, ...rowActions }: InboxNotificationListProps) {
  const listRef = useRef<HTMLElement | null>(null)
  const virtuosoRef = useRef<VirtuosoHandle | null>(null)
  const loadingRef = useRef(loadingMore)
  const focusedRowRef = useRef<{ id: string; index: number } | null>(null)
  const virtualized = notifications.length > 80
  loadingRef.current = loadingMore

  useEffect(() => {
    const list = listRef.current
    if (!list || virtualized || !hasMore || !onLoadMore) return
    const onScroll = () => {
      if (!loadingRef.current && list.scrollHeight - list.scrollTop - list.clientHeight <= 165) onLoadMore()
    }
    list.addEventListener('scroll', onScroll, { passive: true })
    return () => list.removeEventListener('scroll', onScroll)
  }, [hasMore, onLoadMore, virtualized])

  useEffect(() => {
    const focused = focusedRowRef.current
    if (!focused || notifications.some(notification => notification.id === focused.id)) return
    const index = Math.min(focused.index, notifications.length - 1)
    const next = notifications[index]
    if (!next) {
      focusedRowRef.current = null
      return
    }
    focusedRowRef.current = { id: next.id, index }
    if (virtualized) virtuosoRef.current?.scrollToIndex({ index, align: 'center' })
    window.requestAnimationFrame(() => focusRow(listRef.current, next.id))
  }, [notifications, virtualized])

  const moveFocus = (direction: -1 | 1, notification: InboxNotificationRowData) => {
    const current = notifications.findIndex(item => item.id === notification.id)
    if (current < 0) return
    const next = Math.max(0, Math.min(notifications.length - 1, current + direction))
    const target = notifications[next]
    if (!target) return
    focusedRowRef.current = { id: target.id, index: next }
    // Directional navigation carries the active detail to the adjacent row.
    if (next !== current) rowActions.onOpen(target)
    if (virtualized) {
      virtuosoRef.current?.scrollToIndex({ index: next, align: 'center' })
      window.requestAnimationFrame(() => window.requestAnimationFrame(() => focusRow(listRef.current, target.id)))
    } else focusRow(listRef.current, target.id)
  }

  const renderNotification = (notification: InboxNotificationRowData) => <div role="listitem" key={notification.id} data-notification-id={notification.id}>
    <InboxNotificationRow
      {...rowActions}
      notification={notification}
      active={selectedId === notification.id}
      disabled={Boolean(pending?.[notification.id])}
      pending={Boolean(pending?.[notification.id])}
      onMoveFocus={moveFocus}
      onFocus={(focused) => {
        const index = notifications.findIndex(notification => notification.id === focused.id)
        if (index >= 0) focusedRowRef.current = { id: focused.id, index }
      }}
      onBlur={() => { focusedRowRef.current = null }}
    />
  </div>

  if (virtualized) return <Virtuoso
    className="flow-inbox-notification-list"
    role="list"
    ref={virtuosoRef}
    scrollerRef={element => { listRef.current = element instanceof HTMLElement ? element : null }}
    data={notifications}
    computeItemKey={(_index, notification) => notification.id}
    components={INBOX_VIRTUAL_COMPONENTS}
    context={{ filterHiddenCount, loadingMore, onClearFilters }}
    increaseViewportBy={{ top: 220, bottom: 550 }}
    itemContent={(_index, notification) => renderNotification(notification)}
    endReached={() => { if (hasMore && !loadingRef.current) onLoadMore?.() }}
  />

  return (
    <div className="flow-inbox-notification-list" role="list" ref={element => { listRef.current = element }}>
      {notifications.map(renderNotification)}
      {loadingMore ? <div className="flow-inbox-notification-list__loading" role="status">Loading…</div> : null}
      {filterHiddenCount > 0 && notifications.length > 0 ? <InboxFilterNotice hiddenCount={filterHiddenCount} onClear={onClearFilters} /> : null}
    </div>
  )
}

function InboxFilterNotice({ hiddenCount, onClear }: { hiddenCount: number; onClear?: () => void }) {
  return <div className="flow-inbox-filter-notice" role="status">
    <div><span className="flow-inbox-filter-notice__count">{hiddenCount} {hiddenCount === 1 ? 'notification' : 'notifications'}</span><span> hidden by filters</span></div>
    {onClear ? <button type="button" onClick={onClear}>Clear Filters <span aria-hidden="true">×</span></button> : null}
  </div>
}

function focusRow(list: HTMLElement | null, id: string) {
  list?.querySelector<HTMLElement>(`[data-notification-id="${cssEscape(id)}"] [role="link"]`)?.focus()
}

function cssEscape(value: string) {
  return globalThis.CSS?.escape ? globalThis.CSS.escape(value) : value.replace(/["\\]/g, '\\$&')
}

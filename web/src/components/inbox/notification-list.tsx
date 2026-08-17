import { useEffect, useRef } from 'react'

import { InboxNotificationRow, type InboxNotificationRowData, type InboxNotificationRowProps } from './notification-row'

export interface InboxNotificationListProps extends Pick<InboxNotificationRowProps, 'onOpen' | 'onReadChange' | 'onDelete' | 'onSnooze' | 'onFavoriteChange' | 'onCopyLink' | 'onCopyIdentifier'> {
  notifications: InboxNotificationRowData[]
  selectedId?: string | null
  pending?: Record<string, unknown>
  hasMore?: boolean
  loadingMore?: boolean
  onLoadMore?: () => void
}

export function InboxNotificationList({ notifications, selectedId = null, pending, hasMore = false, loadingMore = false, onLoadMore, ...rowActions }: InboxNotificationListProps) {
  const listRef = useRef<HTMLDivElement>(null)
  const loadingRef = useRef(loadingMore)
  const focusedRowRef = useRef<{ id: string; index: number } | null>(null)
  loadingRef.current = loadingMore

  useEffect(() => {
    const list = listRef.current
    if (!list || !hasMore || !onLoadMore) return
    const onScroll = () => {
      if (!loadingRef.current && list.scrollHeight - list.scrollTop - list.clientHeight <= 165) onLoadMore()
    }
    list.addEventListener('scroll', onScroll, { passive: true })
    return () => list.removeEventListener('scroll', onScroll)
  }, [hasMore, onLoadMore])

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
    window.requestAnimationFrame(() => focusRow(listRef.current, next.id))
  }, [notifications])

  const moveFocus = (direction: -1 | 1, notification: InboxNotificationRowData) => {
    const current = notifications.findIndex(item => item.id === notification.id)
    if (current < 0) return
    const next = Math.max(0, Math.min(notifications.length - 1, current + direction))
    const target = notifications[next]
    if (!target) return
    focusedRowRef.current = { id: target.id, index: next }
    // Directional navigation carries the active detail to the adjacent row.
    if (next !== current) rowActions.onOpen(target)
    focusRow(listRef.current, target.id)
  }

  return (
    <div className="flow-inbox-notification-list" role="list" ref={listRef}>
      {notifications.map(notification => (
        <div role="listitem" key={notification.id} data-notification-id={notification.id}>
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
      ))}
      {loadingMore ? <div className="flow-inbox-notification-list__loading" role="status">Loading…</div> : null}
    </div>
  )
}

function focusRow(list: HTMLDivElement | null, id: string) {
  list?.querySelector<HTMLElement>(`[data-notification-id="${cssEscape(id)}"] [role="link"]`)?.focus()
}

function cssEscape(value: string) {
  return globalThis.CSS?.escape ? globalThis.CSS.escape(value) : value.replace(/["\\]/g, '\\$&')
}

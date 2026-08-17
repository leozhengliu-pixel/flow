import { useCallback, useMemo, useRef, useState } from 'react'

import type { InboxNotificationRowData, InboxSnoozePreset } from './notification-row'

export type InboxMutationKind = 'read' | 'delete' | 'snooze' | 'favorite'

export interface InboxPersistenceAdapter {
  setRead: (id: string, read: boolean) => Promise<void>
  delete: (id: string) => Promise<void>
  snooze: (id: string, preset: InboxSnoozePreset) => Promise<void>
  setFavorite: (id: string, favorite: boolean) => Promise<void>
}

export interface InboxMutationError {
  id: string
  kind: InboxMutationKind
  message: string
}

export interface InboxControllerOptions {
  notifications: InboxNotificationRowData[]
  selectedId?: string | null
  onNotificationsChange: (notifications: InboxNotificationRowData[]) => void
  onSelectedIdChange: (id: string | null) => void
  persistence: InboxPersistenceAdapter
}

export function useInboxController(options: InboxControllerOptions) {
  const { notifications, selectedId = null, onNotificationsChange, onSelectedIdChange, persistence } = options
  const [pending, setPending] = useState<Record<string, InboxMutationKind | undefined>>({})
  const [error, setError] = useState<InboxMutationError | null>(null)
  const retryRef = useRef<null | (() => Promise<void>)>(null)
  const latestRef = useRef(notifications)
  latestRef.current = notifications

  const commit = useCallback((items: InboxNotificationRowData[]) => {
    latestRef.current = items
    onNotificationsChange(items)
  }, [onNotificationsChange])

  const setPendingFor = useCallback((id: string, kind?: InboxMutationKind) => {
    setPending((current) => {
      if (kind) return { ...current, [id]: kind }
      const { [id]: _completed, ...remaining } = current
      return remaining
    })
  }, [])

  const run = useCallback(async (
    id: string,
    kind: InboxMutationKind,
    optimistic: (items: InboxNotificationRowData[]) => InboxNotificationRowData[],
    rollback: (items: InboxNotificationRowData[]) => InboxNotificationRowData[],
    persist: () => Promise<void>,
  ) => {
    const previousSelected = selectedId
    setPendingFor(id, kind)
    setError(null)
    commit(optimistic(latestRef.current))

    const attempt = async () => {
      try {
        await persist()
        retryRef.current = null
        setError(null)
      } catch (cause) {
        commit(rollback(latestRef.current))
        onSelectedIdChange(previousSelected)
        const nextError = {
          id,
          kind,
          message: cause instanceof Error ? cause.message : 'The notification update failed.',
        }
        setError(nextError)
        retryRef.current = async () => {
          setPendingFor(id, kind)
          commit(optimistic(latestRef.current))
          await attempt()
        }
        throw cause
      } finally {
        setPendingFor(id)
      }
    }
    await attempt()
  }, [commit, onSelectedIdChange, selectedId, setPendingFor])

  const actions = useMemo(() => ({
    open: (notification: InboxNotificationRowData) => {
      onSelectedIdChange(notification.id)
      if (!notification.read) {
        void run(notification.id, 'read', items => patchItem(items, notification.id, { read: true }), items => patchItem(items, notification.id, { read: notification.read }), () => persistence.setRead(notification.id, true)).catch(() => undefined)
      }
    },
    close: () => onSelectedIdChange(null),
    setRead: (notification: InboxNotificationRowData, read: boolean) => run(notification.id, 'read', items => patchItem(items, notification.id, { read }), items => patchItem(items, notification.id, { read: notification.read }), () => persistence.setRead(notification.id, read)),
    delete: (notification: InboxNotificationRowData) => run(notification.id, 'delete', items => items.filter(item => item.id !== notification.id), items => restoreItem(items, notification, notifications.findIndex(item => item.id === notification.id)), async () => {
      if (selectedId === notification.id) onSelectedIdChange(null)
      await persistence.delete(notification.id)
    }),
    snooze: (notification: InboxNotificationRowData, preset: InboxSnoozePreset) => run(notification.id, 'snooze', items => items.filter(item => item.id !== notification.id), items => restoreItem(items, notification, notifications.findIndex(item => item.id === notification.id)), async () => {
      if (selectedId === notification.id) onSelectedIdChange(null)
      await persistence.snooze(notification.id, preset)
    }),
    setFavorite: (notification: InboxNotificationRowData, favorite: boolean) => run(notification.id, 'favorite', items => patchItem(items, notification.id, { favorite }), items => patchItem(items, notification.id, { favorite: notification.favorite }), () => persistence.setFavorite(notification.id, favorite)),
  }), [notifications, onSelectedIdChange, persistence, run, selectedId])

  return {
    actions,
    error,
    pending,
    retry: async () => {
      const retry = retryRef.current
      if (!retry) return
      setError(null)
      await retry().catch(() => undefined)
    },
    dismissError: () => setError(null),
  }
}

function patchItem(items: InboxNotificationRowData[], id: string, patch: Partial<InboxNotificationRowData>) {
  return items.map(item => item.id === id ? { ...item, ...patch } : item)
}

function restoreItem(items: InboxNotificationRowData[], item: InboxNotificationRowData, originalIndex: number) {
  if (items.some(candidate => candidate.id === item.id)) return items
  const next = [...items]
  next.splice(Math.max(0, Math.min(originalIndex, next.length)), 0, item)
  return next
}

import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { InboxNotificationRowData } from './notification-row'
import { useInboxController } from './inbox-controller'

const notification: InboxNotificationRowData = {
  id: 'notification-1', actor: 'Teammate', kind: 'assignment', identifier: 'TST-1', title: 'Assigned issue',
  body: 'You were assigned', timeLabel: 'now', timestamp: '2026-08-31T00:00:00.000Z', read: false, favorite: false,
}

function setup(overrides: Partial<Parameters<typeof useInboxController>[0]['persistence']> = {}) {
  let items = [notification]
  let selected: string | null = notification.id
  const onNotificationsChange = vi.fn((next: InboxNotificationRowData[]) => { items = next })
  const onSelectedIdChange = vi.fn((next: string | null) => { selected = next })
  const persistence = {
    setRead: vi.fn().mockResolvedValue(undefined), delete: vi.fn().mockResolvedValue(undefined),
    snooze: vi.fn().mockResolvedValue(undefined), setFavorite: vi.fn().mockResolvedValue(undefined), ...overrides,
  }
  const hook = renderHook(() => useInboxController({ notifications: items, selectedId: selected, onNotificationsChange, onSelectedIdChange, persistence }))
  return { ...hook, onNotificationsChange, onSelectedIdChange, persistence, items: () => items }
}

describe('inbox controller', () => {
  it('applies read, favorite, delete, and snooze mutations optimistically', async () => {
    const controller = setup()
    await act(() => controller.result.current.actions.setRead(notification, true))
    expect(controller.items()[0].read).toBe(true)
    expect(controller.persistence.setRead).toHaveBeenCalledWith(notification.id, true)
    await act(() => controller.result.current.actions.setFavorite({ ...notification, read: true }, true))
    expect(controller.items()[0].favorite).toBe(true)
    await act(() => controller.result.current.actions.snooze({ ...notification, read: true, favorite: true }, 'tomorrow'))
    expect(controller.items()).toEqual([])
    expect(controller.onSelectedIdChange).toHaveBeenCalledWith(null)
  })

  it('marks unread notifications when opened and closes selection', async () => {
    const controller = setup()
    act(() => controller.result.current.actions.open(notification))
    await waitFor(() => expect(controller.persistence.setRead).toHaveBeenCalledWith(notification.id, true))
    expect(controller.onSelectedIdChange).toHaveBeenCalledWith(notification.id)
    act(() => controller.result.current.actions.close())
    expect(controller.onSelectedIdChange).toHaveBeenLastCalledWith(null)
  })

  it('rolls back failures and retries the stored mutation', async () => {
    const remove = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(undefined)
    const controller = setup({ delete: remove })
    await act(async () => {
      await controller.result.current.actions.delete(notification).catch(error => expect(error).toEqual(new Error('offline')))
    })
    expect(controller.items()).toEqual([notification])
    await waitFor(() => expect(controller.result.current.error).toMatchObject({ id: notification.id, kind: 'delete', message: 'offline' }))
    await act(() => controller.result.current.retry())
    expect(remove).toHaveBeenCalledTimes(2)
    expect(controller.items()).toEqual([])
    expect(controller.result.current.error).toBeNull()
  })
})

import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { InboxNotificationRow, type InboxNotificationRowData } from './notification-row'

const notification: InboxNotificationRowData = {
  id: 'inbox-1',
  actor: 'Flow user',
  actorInitials: 'FU',
  kind: 'assignment',
  identifier: 'INT-1',
  title: 'Favorite perf issue',
  body: 'Flow user assigned the issue to you',
  timeLabel: '2h',
  timestamp: 'Sep 12, 10:00',
  read: false,
}

describe('inbox row hover actions', () => {
  it('exposes only the compact snooze action without opening the notification', async () => {
    const user = userEvent.setup()
    const onOpen = vi.fn()
    const onReadChange = vi.fn()
    const onDelete = vi.fn()
    const onSnooze = vi.fn()
    render(
      <InboxNotificationRow
        notification={notification}
        onOpen={onOpen}
        onReadChange={onReadChange}
        onDelete={onDelete}
        onSnooze={onSnooze}
      />,
    )
    await user.click(screen.getByRole('button', { name: 'Snooze notification' }))
    await user.click(screen.getByRole('menuitem', { name: 'An hour from now' }))
    expect(onSnooze).toHaveBeenCalledWith(notification, 'hour')
    expect(onOpen).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Snooze notification' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Mark as read' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Notification actions' })).not.toBeInTheDocument()
  })
})

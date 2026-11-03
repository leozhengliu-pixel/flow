import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
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
  it('opens the complete dated snooze menu with H and accepts a typed time', async () => {
    const user = userEvent.setup()
    const onSnooze = vi.fn()
    render(<InboxNotificationRow notification={notification} onOpen={vi.fn()} onReadChange={vi.fn()} onDelete={vi.fn()} onSnooze={onSnooze}/>)
    screen.getByRole('link').focus()
    await user.keyboard('h')
    const menu = screen.getByRole('menu', { name: 'Snooze notification' })
    await waitFor(() => expect(within(menu).getByRole('menuitem', { name: 'An hour from now' })).toHaveFocus())
    expect(within(menu).getAllByRole('menuitem')).toHaveLength(6)
    for (const option of within(menu).getAllByRole('menuitem').slice(0, 5)) {
      expect(option.querySelector('time')).not.toBeNull()
      expect(option.querySelector('svg')).not.toBeNull()
    }
    expect(within(menu).getByRole('menuitem', { name: 'Custom…' }).querySelector('svg')).not.toBeNull()
    const input = within(menu).getByRole('textbox', { name: 'Snooze time' })
    await user.type(input, 'not a date{Enter}')
    expect(onSnooze).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toHaveTextContent('Choose a valid date and time.')
    await user.clear(input)
    const start = Date.now()
    await user.type(input, 'in 3 hours{Enter}')
    expect(onSnooze).toHaveBeenCalledWith(notification, expect.objectContaining({ kind: 'custom' }))
    const chosen = new Date(onSnooze.mock.calls[0][1].snoozedUntil).getTime()
    expect(Math.abs(chosen - start - 3 * 60 * 60 * 1000)).toBeLessThan(5000)
  })

  it.each(['下午 4 点','2 天后','5 周后','两周后','下周','明年','下个月'])('accepts the Chinese snooze input %s',async query=>{
    const user=userEvent.setup()
    const onSnooze=vi.fn()
    render(<InboxNotificationRow notification={notification} onOpen={vi.fn()} onReadChange={vi.fn()} onDelete={vi.fn()} onSnooze={onSnooze}/>)
    screen.getByRole('link').focus()
    await user.keyboard('h')
    const menu=screen.getByRole('menu',{name:'Snooze notification'})
    await waitFor(()=>expect(within(menu).getByRole('menuitem',{name:'An hour from now'})).toHaveFocus())
    await user.type(within(menu).getByRole('textbox',{name:'Snooze time'}),`${query}{Enter}`)
    expect(onSnooze).toHaveBeenCalledWith(notification,expect.objectContaining({kind:'custom'}))
    expect(Date.parse(onSnooze.mock.calls[0][1].snoozedUntil)).toBeGreaterThan(Date.now())
  })
  it('keeps IME confirmation separate from submitting the snooze query',async()=>{
    const user=userEvent.setup(),onSnooze=vi.fn()
    render(<InboxNotificationRow notification={notification} onOpen={vi.fn()} onReadChange={vi.fn()} onDelete={vi.fn()} onSnooze={onSnooze}/>)
    screen.getByRole('link').focus()
    await user.keyboard('h')
    const menu=screen.getByRole('menu',{name:'Snooze notification'})
    await waitFor(()=>expect(within(menu).getByRole('menuitem',{name:'An hour from now'})).toHaveFocus())
    const input=within(menu).getByRole('textbox',{name:'Snooze time'})
    await user.type(input,'2 天后')
    fireEvent.keyDown(input,{key:'Enter',isComposing:true})
    expect(onSnooze).not.toHaveBeenCalled()
    await user.keyboard('{Enter}')
    expect(onSnooze).toHaveBeenCalledWith(notification,expect.objectContaining({kind:'custom'}))
  })

  it('retains the favorite keyboard action after simplifying the row menu', async () => {
    const user = userEvent.setup()
    const onFavoriteChange = vi.fn()
    render(<InboxNotificationRow notification={notification} onOpen={vi.fn()} onReadChange={vi.fn()} onDelete={vi.fn()} onSnooze={vi.fn()} onFavoriteChange={onFavoriteChange}/>)
    screen.getByRole('link').focus()
    await user.keyboard('{Alt>}f{/Alt}')
    expect(onFavoriteChange).toHaveBeenCalledWith(notification, true)
  })

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

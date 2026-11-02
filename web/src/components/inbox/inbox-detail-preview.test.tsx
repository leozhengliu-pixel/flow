import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { makeIssue } from '@/test/fixtures'
import { InboxDetailPreview } from './inbox-detail-preview'
import type { InboxNotificationRowData } from './notification-row'

it('copies the selected issue through the canonical link callback', async () => {
  const issue = makeIssue()
  const notification: InboxNotificationRowData = { id: 'notification', actor: 'Teammate', kind: 'comment', identifier: issue.identifier, title: issue.title, body: '', timeLabel: '', timestamp: '', read: true, href: '/workspace/issue/ENG-1/test' }
  const onCopyLink = vi.fn()
  render(<InboxDetailPreview notification={notification} issue={issue} onBack={vi.fn()} onOpenIssue={vi.fn()} onFavoriteChange={vi.fn()} onReadChange={vi.fn()} onSnooze={vi.fn()} onDelete={vi.fn()} onCopyLink={onCopyLink}><div>Issue</div></InboxDetailPreview>)
  await userEvent.setup().click(screen.getByRole('button', { name: 'Copy issue URL' }))
  expect(onCopyLink).toHaveBeenCalledWith(notification)
})

it('uses the notification issue path when no copy callback is supplied', async () => {
  const user = userEvent.setup()
  const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
  const issue = makeIssue()
  const notification: InboxNotificationRowData = { id: 'notification', actor: 'Teammate', kind: 'comment', identifier: issue.identifier, title: issue.title, body: '', timeLabel: '', timestamp: '', read: true, href: '/workspace/issue/TST-1/test' }
  render(<InboxDetailPreview notification={notification} issue={issue} onBack={vi.fn()} onOpenIssue={vi.fn()} onFavoriteChange={vi.fn()} onReadChange={vi.fn()} onSnooze={vi.fn()} onDelete={vi.fn()}><div>Issue</div></InboxDetailPreview>)
  await user.click(screen.getByRole('button', { name: 'Copy issue URL' }))
  expect(copy).toHaveBeenCalledWith(`${window.location.origin}${notification.href}`)
  copy.mockRestore()
})

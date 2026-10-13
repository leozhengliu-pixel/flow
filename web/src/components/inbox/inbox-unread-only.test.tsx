import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { fetchInboxNotifications, fetchPulseCapabilities, fetchPulseSummary, listIssueRecords, updateInboxNotification } from '@/lib/api'
import { inboxUnread } from '@/lib/inbox-unread'
import { makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'
import type { Notification, NotificationList } from '@/types/flow'

import { InboxAppPage } from './inbox-app-page'

vi.mock('@/lib/api', async original => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchInboxNotifications: vi.fn(),
  listIssueRecords: vi.fn(),
  updateInboxNotification: vi.fn(),
  batchNotifications: vi.fn(async () => ({ updated: 0 })),
  fetchPulseSummary: vi.fn(),
  fetchPulseCapabilities: vi.fn(),
  updateUserSettings: vi.fn(),
}))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/components/detail/detail-pane', () => ({ DetailPane: ({ issue }: { issue: { title: string } }) => <div data-testid="issue-detail">{issue.title}</div> }))

const READ_AT = '2026-10-02T00:00:00.000Z'

function notification(id: string, issueId: string, createdAt: string, overrides: Partial<Notification> = {}): Notification {
  return {
    id, recipientId: viewer.id, type: 'assignment', sourceType: 'issue', sourceId: issueId, issueId, actor: teammate,
    category: 'assignments', groupKey: id, occurrenceCount: 1, latestActorIds: [], favorite: false, createdAt, updatedAt: createdAt,
    ...overrides,
  } as Notification
}

const unread = [
  notification('n-1', 'issue-1', '2026-10-01T09:00:00.000Z'),
  notification('n-2', 'issue-2', '2026-10-01T08:00:00.000Z'),
  notification('n-3', 'issue-3', '2026-10-01T07:00:00.000Z'),
]
const issues = ['Alpha', 'Bravo', 'Charlie'].map((title, index) => makeIssue({ id: `issue-${index + 1}`, identifier: `TST-${index + 1}`, title }))

function list(notifications: Notification[]): NotificationList {
  return { notifications, unreadCount: notifications.filter(item => !item.readAt).length }
}

function renderInbox() {
  const noop = vi.fn(async () => undefined)
  return render(
    <I18nProvider><TooltipProvider>
      <InboxAppPage data={makeBootstrap({ issueCollectionPaged: true, issues, notifications: [], reviews: [], comments: {}, userSettings: {} })} onReload={noop} onOpenIssue={vi.fn()} onDeleteRelation={noop} onCreateSubIssue={noop} onReactIssue={noop} onCreateComment={noop} onEditComment={noop} onDeleteComment={noop} onReactComment={noop} onUploadAttachment={noop} onDeleteAttachment={noop} onLoadIssueContext={noop}/>
    </TooltipProvider></I18nProvider>,
  )
}

const row = (text: string) => screen.getAllByRole('link').find(item => item.textContent?.includes(text))!
const queryRow = (text: string) => screen.queryAllByRole('link').find(item => item.textContent?.includes(text))
const titles = () => screen.getAllByRole('link').map(item => ['Alpha', 'Bravo', 'Charlie'].find(title => item.textContent?.includes(title))).filter(Boolean)

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.clearAllMocks()
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
  // "Show unreads only": the read filter is off.
  localStorage.setItem('flow.inbox.display-options', JSON.stringify({ ordering: 'newest', showSnoozed: false, showRead: false, showUnreadFirst: false, priorityInbox: false, unreadGrouping: 'none' }))
  window.history.replaceState(null, '', '/workspace/inbox')
  inboxUnread.set(0, '')
  vi.mocked(fetchInboxNotifications).mockResolvedValue(list(unread))
  vi.mocked(listIssueRecords).mockResolvedValue({ items: issues, hasMore: false, total: 3 } as Awaited<ReturnType<typeof listIssueRecords>>)
  vi.mocked(fetchPulseSummary).mockResolvedValue({ title: 'Daily Pulse', generatedAt: READ_AT, text: '', ai: false, sections: [] })
  vi.mocked(fetchPulseCapabilities).mockResolvedValue({ aiSummaries: false, audio: false })
  vi.mocked(updateInboxNotification).mockImplementation(async (id, input) => ({ ...unread.find(item => item.id === id)!, readAt: input.read ? READ_AT : undefined }))
})
afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

describe('inbox with unreads only', () => {
  it('keeps a clicked notification listed and selected once opening marks it read', async () => {
    renderInbox()
    await screen.findByText('Alpha')
    await userEvent.setup().click(row('Alpha'))
    await waitFor(() => expect(updateInboxNotification).toHaveBeenCalledWith('n-1', { read: true }))
    await waitFor(() => expect(row('Alpha')).toHaveAttribute('data-read', 'true'))
    expect(titles()).toEqual(['Alpha', 'Bravo', 'Charlie'])
    expect(await screen.findByTestId('issue-detail')).toHaveTextContent('Alpha')
  })

  it('continues keyboard navigation from the opened notification', async () => {
    renderInbox()
    await screen.findByText('Alpha')
    const user = userEvent.setup()
    await user.click(row('Alpha'))
    await waitFor(() => expect(row('Alpha')).toHaveAttribute('data-read', 'true'))
    row('Alpha').focus()
    fireEvent.keyDown(row('Alpha'), { key: 'ArrowDown' })
    await waitFor(() => expect(screen.getByTestId('issue-detail')).toHaveTextContent('Bravo'))
    await waitFor(() => expect(row('Bravo')).toHaveAttribute('data-read', 'true'))
    expect(titles()).toEqual(['Alpha', 'Bravo', 'Charlie'])
    fireEvent.keyDown(row('Bravo'), { key: 'ArrowDown' })
    await waitFor(() => expect(screen.getByTestId('issue-detail')).toHaveTextContent('Charlie'))
    fireEvent.keyDown(row('Charlie'), { key: 'ArrowUp' })
    await waitFor(() => expect(screen.getByTestId('issue-detail')).toHaveTextContent('Bravo'))
    expect(titles()).toEqual(['Alpha', 'Bravo', 'Charlie'])
  })

  it('drops read notifications once the Inbox is left and re-entered', async () => {
    const first = renderInbox()
    await screen.findByText('Alpha')
    await userEvent.setup().click(row('Alpha'))
    await waitFor(() => expect(row('Alpha')).toHaveAttribute('data-read', 'true'))
    first.unmount()
    vi.mocked(fetchInboxNotifications).mockResolvedValue(list([{ ...unread[0], readAt: READ_AT }, unread[1], unread[2]]))
    renderInbox()
    await screen.findByText('Bravo')
    expect(queryRow('Alpha')).toBeUndefined()
    expect(titles()).toEqual(['Bravo', 'Charlie'])
  })
})

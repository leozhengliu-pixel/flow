import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { makeSession } from '@/components/coding-sessions/coding-session-test-fixtures'
import { TooltipProvider } from '@/components/ui/tooltip'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, CodeReview, Notification } from '@/types/flow'

vi.mock('@/lib/coding-sessions-api', () => ({
  fetchCodingStatus: vi.fn(async () => ({ enabled: true })),
  fetchCodingSession: vi.fn(async () => makeSession({ id: 'session-1', state: 'complete', phase: 'done' })),
  fetchCodingSessions: vi.fn(async () => []), fetchCodingSessionDiff: vi.fn(async () => ({ source: 'none', additions: 0, deletions: 0, files: [] })),
  fetchCodingSessionArtifacts: vi.fn(async () => []), fetchCodingModels: vi.fn(async () => []), fetchCodingEnvironments: vi.fn(async () => []),
  fetchGitHubApp: vi.fn(async () => ({ configured: true, hostName: 'github.com', installations: [] })),
  setCodingSessionSubscription: vi.fn(), promptCodingSession: vi.fn(), stopCodingSession: vi.fn(), dismissCodingSession: vi.fn(),
}))
vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), getApplicationTask: vi.fn(async () => ({ session: { id: 'task-1' }, activities: [] })) }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), syncReview: vi.fn(), updateInboxNotification: vi.fn(async () => undefined) }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/components/agent/agent-rich-text', () => ({ AgentRichText: ({ content }: { content: string }) => <div>{content}</div> }))

import { InboxAppPage } from './inbox-app-page'

const agent = { ...viewer, id: 'app-flow', name: 'flow', displayName: 'Flow', app: true }
const issue = makeIssue()
const review = {
  id: 'review-1', slugId: 'add-shout-1a2b3c4d5e6f', provider: 'github', externalId: 'github-1', number: 33, title: 'Add shout function', description: '',
  status: 'open', repositoryOwner: 'flow', repositoryName: 'flow', url: 'https://github.com/flow/flow/pull/33', author: teammate,
  reviewerIds: [], teamReviewers: [], issueIds: [issue.id], baseBranch: 'main', headBranch: 'feature/shout', branchState: 'upToDate',
  additions: 4, deletions: 0, commitCount: 1, checks: [], files: [], events: [], favorite: false, draft: false, quickToReview: false,
  createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T01:00:00Z',
} as unknown as CodeReview
const finished = {
  id: 'n-finished', recipientId: viewer.id, type: 'agentReply', issueId: issue.id, sourceType: 'issue', sourceId: issue.id, commentId: '', actor: agent, category: 'comments',
  groupKey: 'g', occurrenceCount: 1, latestActorIds: [agent.id], favorite: false, text: 'Flow finished: Added shout(name)',
  payload: { excerpt: 'Added shout(name)', agentActivity: 'finished', codingSessionId: 'session-1', reviewId: review.id },
  createdAt: '2026-10-02T00:00:00Z', updatedAt: '2026-10-02T01:00:00Z',
} as unknown as Notification

const bootstrap = () => makeBootstrap({
  notifications: [finished], issues: [issue], reviews: [review], comments: {}, userSettings: {}, integrationConnections: [{ id: 'github', provider: 'github' }],
} as unknown as Partial<BootstrapData>)

function renderInbox(onNavigate = vi.fn()) {
  const noop = vi.fn(async () => undefined)
  render(<I18nProvider><TooltipProvider>
    <InboxAppPage data={bootstrap()} onReload={noop} onOpenIssue={vi.fn()} onNavigate={onNavigate} onDeleteRelation={noop} onCreateSubIssue={noop} onReactIssue={noop} onCreateComment={noop} onEditComment={noop} onDeleteComment={noop} onReactComment={noop} onUploadAttachment={noop} onDeleteAttachment={noop}/>
  </TooltipProvider></I18nProvider>)
  return onNavigate
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
  window.history.replaceState(null, '', `/workspace/inbox?notification=${finished.id}`)
})
afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

describe('a finished coding session in the inbox', () => {
  it('embeds the review page on its Diff tab with the agent panel, in place of the inbox detail header', async () => {
    renderInbox()
    const tab = await screen.findByRole('link', { name: 'Diff' })
    expect(tab).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Overview' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Guide' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Hide agent' })).toHaveAttribute('aria-pressed', 'true')
    await waitFor(() => expect(document.querySelector('.review-agent-panel')).not.toBeNull())
    expect(screen.queryByRole('button', { name: 'Back to Inbox' })).toBeNull()
  })

  it('opens the full review page from the expand control and keeps tab changes inside the pane', async () => {
    const onNavigate = renderInbox()
    fireEvent.click(await screen.findByRole('link', { name: 'Overview' }))
    expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute('aria-current', 'page')
    expect(onNavigate).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Review diff in full window' }))
    expect(onNavigate).toHaveBeenCalledWith('/workspace/review/add-shout-1a2b3c4d5e6f?showAgent=true&agentSessionId=session-1')
  })

  it('leaves inbox shortcuts to the inbox and review shortcuts to the review', async () => {
    renderInbox()
    await screen.findByRole('link', { name: 'Diff' })
    // The review claims 1 / 2 / 3 on the page body.
    fireEvent.keyDown(document.body, { key: '2', code: 'Digit2' })
    expect(screen.getByRole('link', { name: 'Guide' })).toHaveAttribute('aria-current', 'page')
    // A key typed inside the review is ignored by the inbox's own shortcuts.
    const inReview = document.querySelector('[data-embedded-review] a')!
    const event = new KeyboardEvent('keydown', { key: 'Backspace', shiftKey: true, bubbles: true, cancelable: true })
    inReview.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
  })
})

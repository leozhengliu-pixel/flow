import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import * as api from '@/lib/coding-sessions-api'
import { getApplicationTask } from '@/lib/application-agents'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import { CodingSessionPage } from './coding-session-page'
import { CodingSessionStatusRow } from './coding-session-card'
import { codingDiffTree, codingSessionSubscribed } from './coding-session-page-model'
import { IssueCodingDiffs } from './issue-coding-diffs'
import { makeSession } from './coding-session-test-fixtures'
import { resetCodingAvailabilityCache } from './use-coding-sessions'

vi.mock('@/lib/coding-sessions-api', () => ({
  fetchCodingStatus: vi.fn(async () => ({ enabled: true })), fetchCodingSession: vi.fn(), fetchCodingSessions: vi.fn(async () => []), stopCodingSession: vi.fn(), restartCodingSession: vi.fn(),
  approveCodingSession: vi.fn(), dismissCodingSession: vi.fn(), createCodingSessionPullRequest: vi.fn(), promptCodingSession: vi.fn(), fetchCodingSandboxLogs: vi.fn(),
  fixCodingEnvironmentWithAgent: vi.fn(), startReviewCodingSession: vi.fn(), fetchCodingSessionArtifacts: vi.fn(async () => []),
  fetchCodingSessionDiff: vi.fn(async () => ({ source: 'none', additions: 0, deletions: 0, files: [] })), setCodingSessionSubscription: vi.fn(),
}))
vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), getApplicationTask: vi.fn() }))

const issue = makeIssue()
const pullRequest = { sessionId: 'cs-1', codeHost: 'github', hostName: 'github.com', repository: 'PocklyApp/qa-coding-session', number: 6, url: 'https://github.com/PocklyApp/qa-coding-session/pull/6', title: 'Add shout(name)', state: 'open', draft: true, createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z' } as never
const data = () => makeBootstrap({ reviews: [], issues: [issue] } as never)

function Location() {
  const location = useLocation()
  return <output aria-label="location">{location.pathname + location.search}</output>
}

function renderPage(path = '/workspace/diff/cs-1') {
  const navigate = vi.fn()
  const result = render(<MemoryRouter initialEntries={[path]}><I18nProvider><Routes>
    <Route path="/:ws/diff/:id" element={<><CodingSessionPage data={data()} sessionId="cs-1" onNavigate={navigate} onOpenSidebar={vi.fn()}/><Location/></>}/>
  </Routes></I18nProvider></MemoryRouter>)
  return { ...result, navigate }
}

beforeEach(() => {
  vi.clearAllMocks()
  resetCodingAvailabilityCache()
  vi.mocked(getApplicationTask).mockResolvedValue({ session: { id: 'task-1', status: 'active', version: 1 }, activities: [] } as never)
})

describe('coding session page', () => {
  it('shows the crumb bar, the code changes and the agent panel, and hides the agent through the URL', async () => {
    const user = userEvent.setup()
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ issueId: issue.id, displayTitle: 'Fix login' }))
    const { navigate } = renderPage()
    const page = await screen.findByRole('main', { name: 'Coding session' })
    expect(within(page).getAllByRole('link', { name: /TST-1/ })[0]).toHaveAttribute('href', expect.stringContaining('TST-1'))
    expect(within(page).getByRole('heading', { level: 2, name: 'Fix login' })).toBeVisible()
    expect(await within(page).findByText('No changes yet')).toBeVisible()
    expect(within(page).getByRole('complementary', { name: 'Agent' })).toBeVisible()
    // Without a pull request the bar offers only the agent toggle (no "Copy URL").
    expect(within(page).queryByRole('button', { name: 'Copy URL' })).toBeNull()
    await user.click(within(page).getByRole('button', { name: 'Hide agent' }))
    expect(navigate).toHaveBeenCalledWith('/workspace/diff/cs-1?showAgent=false', { replace: true })
  })

  it('offers Copy URL once the session has a pull request', async () => {
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ issueId: issue.id, latestPullRequest: pullRequest }))
    renderPage()
    expect(await screen.findByRole('button', { name: 'Copy URL' })).toBeVisible()
  })

  it('moves to the review page (replacing the entry) once the session’s PR has its review', async () => {
    const review = { id: 'review-6', slugId: 'add-shout-6', provider: 'github', number: 6, repositoryOwner: 'PocklyApp', repositoryName: 'qa-coding-session', url: pullRequest.url, title: 'Add shout(name)' }
    const withReview = () => makeBootstrap({ reviews: [review], issues: [issue] } as never)
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ issueId: issue.id }))
    const navigate = vi.fn()
    const page = (bootstrap: ReturnType<typeof data>) => <MemoryRouter initialEntries={['/workspace/diff/cs-1']}><I18nProvider><Routes>
      <Route path="/:ws/diff/:id" element={<CodingSessionPage data={bootstrap} sessionId="cs-1" onNavigate={navigate} onOpenSidebar={vi.fn()}/>}/>
    </Routes></I18nProvider></MemoryRouter>
    const view = render(page(withReview()))
    await screen.findByRole('heading', { level: 2 })
    // No PR yet: the session page stays.
    expect(navigate).not.toHaveBeenCalled()
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ issueId: issue.id, latestPullRequest: pullRequest }))
    view.unmount()
    render(page(withReview()))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/workspace/review/add-shout-6/changes?showAgent=true&agentSessionId=cs-1', { replace: true }))
  })

  it('keeps the agent hidden from ?showAgent=false', async () => {
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession())
    renderPage('/workspace/diff/cs-1?showAgent=false')
    const toggle = await screen.findByRole('button', { name: 'Show agent' })
    expect(toggle).toHaveAttribute('aria-pressed', 'false')
    await waitFor(() => expect(screen.queryByRole('complementary', { name: 'Agent' })).toBeNull())
  })

  it('shows the load error', async () => {
    vi.mocked(api.fetchCodingSession).mockRejectedValue(new Error('Not found'))
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('Not found')
  })
})

describe('issue sidebar Diffs › Code changes', () => {
  it('links the latest session page once the issue has a coding session', async () => {
    vi.mocked(api.fetchCodingSessions).mockResolvedValue([
      makeSession({ id: 'old', createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-03T00:00:00Z' }),
      makeSession({ id: 'new', createdAt: '2026-10-02T00:00:00Z', diffs: [{ path: 'a.ts', additions: 3, deletions: 1, status: 'modified' }] }),
    ])
    render(<MemoryRouter><I18nProvider><IssueCodingDiffs issue={issue} data={data()}/></I18nProvider></MemoryRouter>)
    const section = await screen.findByRole('region', { name: 'Diffs' })
    const row = within(section).getByRole('link', { name: /Code changes/ })
    expect(row).toHaveAttribute('href', '/workspace/diff/new')
    expect(row).toHaveTextContent('+3−1')
  })

  it('renders nothing without a session', async () => {
    vi.mocked(api.fetchCodingSessions).mockResolvedValue([])
    const { container } = render(<MemoryRouter><I18nProvider><IssueCodingDiffs issue={issue} data={data()}/></I18nProvider></MemoryRouter>)
    await waitFor(() => expect(api.fetchCodingSessions).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
  })
})

describe('issue session card PR chip', () => {
  it('opens the PR hover card above the chip', async () => {
    const user = userEvent.setup()
    const session = makeSession({ issueId: issue.id, state: 'complete', phase: 'idle', summary: 'Done', latestPullRequest: pullRequest, diffs: [{ path: 'src/greet.js', additions: 4, deletions: 0, status: 'modified' }] })
    render(<MemoryRouter><I18nProvider><CodingSessionStatusRow session={session} data={data()} href="/workspace/diff/cs-1"/></I18nProvider></MemoryRouter>)
    await user.hover(screen.getByLabelText(/Add shout\(name\), qa-coding-session#6, Draft/))
    const card = (await screen.findAllByText('qa-coding-session#6')).map(node => node.closest('.coding-pr-hover-card')).find(Boolean)!
    expect(card).toHaveTextContent('Add shout(name)')
    expect(card).toHaveTextContent('Draft')
    expect(card).toHaveTextContent('1 file')
    expect(card).toHaveTextContent('+4')
  })
})

describe('session page model', () => {
  it('folds single-child directories in the file tree', () => {
    const tree = codingDiffTree([
      { path: 'src/app/a.ts', status: 'modified', additions: 1, deletions: 0, hunks: [] },
      { path: 'src/app/b.ts', status: 'added', additions: 1, deletions: 0, hunks: [] },
      { path: 'README.md', status: 'modified', additions: 1, deletions: 0, hunks: [] },
    ])
    expect([...tree.children.keys()]).toEqual(['src/app', 'README.md'])
    expect([...tree.children.get('src/app')!.children.keys()]).toEqual(['a.ts', 'b.ts'])
  })

  it('subscribes the creator by default and honours opting out', () => {
    expect(codingSessionSubscribed(makeSession({ creatorId: viewer.id }), viewer.id)).toBe(true)
    expect(codingSessionSubscribed(makeSession({ creatorId: viewer.id, unsubscribedIds: [viewer.id] }), viewer.id)).toBe(false)
    expect(codingSessionSubscribed(makeSession({ creatorId: 'other' }), viewer.id)).toBe(false)
    expect(codingSessionSubscribed(makeSession({ creatorId: 'other', subscriberIds: [viewer.id] }), viewer.id)).toBe(true)
  })
})

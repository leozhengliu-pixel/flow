import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentProps } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TooltipProvider } from '@/components/ui/tooltip'
import { I18nProvider } from '@/i18n/i18n'
import { ApiError } from '@/lib/api-client'
import * as api from '@/lib/coding-sessions-api'
import { getApplicationTask, listApplicationTasks, type ApplicationTask } from '@/lib/application-agents'
import { createAgentSession } from '@/lib/api'
import { toast } from '@/lib/toast'
import { makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'
import type { AgentMessage, CodeReview, ReviewEvent, User } from '@/types/flow'
import type { CodingEnvironment } from '@/types/coding-sessions'
import { IssueHeader } from '@/components/issue/issue-header'
import { IssueAssigneeProperty } from '@/components/issue/issue-assignee-property'
import { IssueAgentTasks } from '@/components/agent/issue-agent-tasks'
import { PullRequestCommentActions } from '@/components/reviews/pull-request-comment-actions'
import { AgentTaskCodingSession } from './agent-task-coding-session'
import { ChatCodingSessionCards } from './chat-coding-session-cards'
import { CodingSessionsList } from './coding-session-card'
import { useDelegateCodingSession } from './coding-session-start'
import { ReviewCodingSessionActions } from './review-coding-actions'
import { ReviewAgentPanel, ReviewAgentToggle } from './review-agent-panel'
import { reviewCommentPrompt } from './review-coding-model'
import { makeSession } from './coding-session-test-fixtures'
import { resetCodingAvailabilityCache } from './use-coding-sessions'

vi.mock('@/lib/coding-sessions-api', () => ({
  fetchCodingStatus: vi.fn(), fetchCodingSession: vi.fn(), fetchCodingSessions: vi.fn(), stopCodingSession: vi.fn(), restartCodingSession: vi.fn(),
  approveCodingSession: vi.fn(), dismissCodingSession: vi.fn(), createCodingSessionPullRequest: vi.fn(), promptCodingSession: vi.fn(), fetchCodingSandboxLogs: vi.fn(),
  fixCodingEnvironmentWithAgent: vi.fn(), startReviewCodingSession: vi.fn(), startIssueCodingSession: vi.fn(), fetchCodingEnvironments: vi.fn(),
  fetchCodingSessionArtifacts: vi.fn(async () => []),
  fetchCodingSessionDiff: vi.fn(async () => ({ source: 'none', additions: 0, deletions: 0, files: [] })), setCodingSessionSubscription: vi.fn(),
}))
vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), getApplicationTask: vi.fn(), listApplicationTasks: vi.fn() }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), createAgentSession: vi.fn() }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() } }))
vi.mock('@/components/agent/agent-rich-text', () => ({ AgentRichText: ({ content }: { content: string }) => <div>{content}</div> }))
vi.mock('@/components/ui/action-dialog-service', async original => ({ ...await original<typeof import('@/components/ui/action-dialog-service')>(), confirmAction: vi.fn(async () => true) }))

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const env = (id: string, repository: string): CodingEnvironment => ({ id, name: `Env ${id}`, codeHost: 'github', hostName: 'github.com', repository, mise: {}, version: 1, createdAt: '', updatedAt: '' })
const flowAgent = { id: 'app_flow', name: 'flow', displayName: 'Flow', email: '', active: true, app: true, builtinAgent: true, appScopes: ['app:assignable'], appTeamIds: ['team-1'] } as User
const coder = { ...flowAgent, id: 'app_coder', name: 'coder', displayName: 'Code Agent', builtinAgent: false } as User
const baseData = (overrides = {}) => makeBootstrap({ cycleSettings: {}, documents: [], members: [], drafts: [], reviews: [], favorites: [], customers: [], teamMembers: [], userSettings: {}, teamSettings: {}, projectTemplates: [], agentSkills: [], customerRequests: [], issueSlas: [], slaRules: [], users: [viewer, teammate, flowAgent, coder], ...overrides } as never)
const task = { id: 'task-1', issueId: 'issue-1', teamId: 'team-1', appUserId: flowAgent.id, creatorId: viewer.id, status: 'active', prompt: '', trigger: 'delegation', version: 1, updatedAt: '' } as ApplicationTask

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  resetCodingAvailabilityCache()
  vi.mocked(api.fetchCodingStatus).mockResolvedValue({ enabled: true } as never)
  vi.mocked(api.fetchCodingSessions).mockResolvedValue([])
  vi.mocked(api.startIssueCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued' }))
  vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities: [] })
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn(async () => undefined) } })
})

function renderHeader(data = baseData()) {
  const issue = makeIssue()
  const props: ComponentProps<typeof IssueHeader> = { issue, data, states: data.states, activities: [], saveState: 'idle', position: 1, total: 2, onClose: vi.fn(), onNavigate: vi.fn(), onUpdate: vi.fn(async () => undefined), onDelete: vi.fn(async () => undefined), onRelation: vi.fn() }
  render(<MemoryRouter><I18nProvider><TooltipProvider><IssueHeader {...props}/></TooltipProvider></I18nProvider></MemoryRouter>)
}

describe('Work on issue › Start coding session', () => {
  it('starts in the only environment', async () => {
    const user = userEvent.setup()
    vi.mocked(api.fetchCodingEnvironments).mockResolvedValue([env('a', 'acme/app')])
    renderHeader()
    await waitFor(() => expect(api.fetchCodingStatus).toHaveBeenCalled())
    await user.click(screen.getByRole('button', { name: 'Choose coding tool' }))
    await user.click(await screen.findByRole('option', { name: /Start coding session/ }))
    await waitFor(() => expect(api.startIssueCodingSession).toHaveBeenCalledWith('issue-1', { environmentId: 'a', prompt: undefined }))
    expect(toast.success).toHaveBeenCalledWith('Started coding session', expect.anything())
    expect(localStorage.getItem('flow:coding-sessions:last-environment:workspace:team-1')).toBe('a')
  })

  it('asks which environment when ambiguous, with instructions', async () => {
    const user = userEvent.setup()
    vi.mocked(api.fetchCodingEnvironments).mockResolvedValue([env('a', 'acme/app'), env('b', 'acme/api')])
    renderHeader()
    await waitFor(() => expect(api.fetchCodingStatus).toHaveBeenCalled())
    await user.click(screen.getByRole('button', { name: 'Choose coding tool' }))
    await user.click(await screen.findByRole('option', { name: /Start coding session/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Start coding session' })
    await user.click(within(dialog).getByRole('radio', { name: /Env b/ }))
    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'Keep it small' } })
    await user.click(within(dialog).getByRole('button', { name: 'Start session' }))
    await waitFor(() => expect(api.startIssueCodingSession).toHaveBeenCalledWith('issue-1', { environmentId: 'b', prompt: 'Keep it small' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('suggests the repository of the issue’s linked pull request', async () => {
    const user = userEvent.setup()
    vi.mocked(api.fetchCodingEnvironments).mockResolvedValue([env('a', 'acme/app'), env('b', 'acme/api')])
    const review = { id: 'r', provider: 'github', repositoryOwner: 'acme', repositoryName: 'api', issueIds: ['issue-1'], number: 1, checks: [] } as unknown as CodeReview
    renderHeader(baseData({ reviews: [review] }))
    await waitFor(() => expect(api.fetchCodingStatus).toHaveBeenCalled())
    await user.click(screen.getByRole('button', { name: 'Choose coding tool' }))
    await user.click(await screen.findByRole('option', { name: /Start coding session/ }))
    await waitFor(() => expect(api.startIssueCodingSession).toHaveBeenCalledWith('issue-1', { environmentId: 'b', prompt: undefined }))
  })

  it('shows the error’s next action when the start fails', async () => {
    const user = userEvent.setup()
    vi.mocked(api.fetchCodingEnvironments).mockResolvedValue([])
    vi.mocked(api.startIssueCodingSession).mockRejectedValue(new ApiError('no app', 412, 'noGitHubConnection'))
    renderHeader()
    await waitFor(() => expect(api.fetchCodingStatus).toHaveBeenCalled())
    await user.click(screen.getByRole('button', { name: 'Choose coding tool' }))
    await user.click(await screen.findByRole('option', { name: /Start coding session/ }))
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('GitHub isn’t connected', expect.objectContaining({ action: { label: 'Connect GitHub', href: '/workspace/settings/integrations/github' } })))
    expect(api.startIssueCodingSession).toHaveBeenCalledWith('issue-1', { environmentId: undefined, prompt: undefined })
  })

  it('is hidden when coding sessions are off', async () => {
    const user = userEvent.setup()
    vi.mocked(api.fetchCodingStatus).mockResolvedValue({ enabled: false } as never)
    renderHeader()
    await waitFor(() => expect(api.fetchCodingStatus).toHaveBeenCalled())
    await user.click(screen.getByRole('button', { name: 'Choose coding tool' }))
    expect(await screen.findByRole('option', { name: /Copy as prompt/ })).toBeVisible()
    expect(screen.queryByRole('option', { name: /Start coding session/ })).toBeNull()
  })

  it('respects the workspace flag without asking the server', () => {
    renderHeader(baseData({ workspaceSettings: { featureFlags: { 'coding-sessions': false } } }))
    expect(api.fetchCodingStatus).not.toHaveBeenCalled()
  })
})

function DelegateHarness({ onUpdate }: { onUpdate: (input: object) => Promise<void> }) {
  const data = baseData()
  const issue = makeIssue({ delegate: undefined })
  const coding = useDelegateCodingSession(issue, data, onUpdate)
  return <><IssueAssigneeProperty issue={issue} users={data.users} teamId="team-1" workspaceKey="workspace" allowAgents hoverContext={{ workspaceName: 'W' }} onUpdate={coding.onUpdate}/>{coding.element}</>
}

describe('delegating to the agent', () => {
  const delegateTo = async (name: RegExp) => {
    const user = userEvent.setup(), onUpdate = vi.fn(async () => undefined)
    render(<I18nProvider><DelegateHarness onUpdate={onUpdate}/></I18nProvider>)
    await waitFor(() => expect(api.fetchCodingStatus).toHaveBeenCalled())
    await act(async () => { await Promise.resolve() })
    await user.click(screen.getByRole('combobox', { name: /Change assignee/ }))
    await user.click(await screen.findByRole('option', { name }))
    return onUpdate
  }

  it('starts a coding session when delegating to the Flow agent', async () => {
    vi.mocked(api.fetchCodingEnvironments).mockResolvedValue([env('a', 'acme/app')])
    const onUpdate = await delegateTo(/^Flow/)
    expect(onUpdate).toHaveBeenCalledWith({ delegateId: flowAgent.id })
    await waitFor(() => expect(api.startIssueCodingSession).toHaveBeenCalledWith('issue-1', { environmentId: 'a', prompt: undefined }))
  })

  it('opens the environment picker when the repository is ambiguous', async () => {
    vi.mocked(api.fetchCodingEnvironments).mockResolvedValue([env('a', 'acme/app'), env('b', 'acme/api')])
    await delegateTo(/^Flow/)
    const dialog = await screen.findByRole('dialog', { name: 'Start coding session' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(api.startIssueCodingSession).not.toHaveBeenCalled()
  })

  it('leaves a plain delegation alone without environments or for other agents', async () => {
    vi.mocked(api.fetchCodingEnvironments).mockResolvedValue([])
    await delegateTo(/^Flow/)
    await waitFor(() => expect(api.fetchCodingEnvironments).toHaveBeenCalled())
    expect(api.startIssueCodingSession).not.toHaveBeenCalled()
  })

  it('does not start for a third-party agent', async () => {
    vi.mocked(api.fetchCodingEnvironments).mockResolvedValue([env('a', 'acme/app')])
    const onUpdate = await delegateTo(/Code Agent/)
    expect(onUpdate).toHaveBeenCalledWith({ delegateId: coder.id })
    expect(api.fetchCodingEnvironments).not.toHaveBeenCalled()
  })
})

describe('agent chat session card', () => {
  const message = { parts: [{ id: 'p', type: 'toolCall', toolCall: { id: 't', name: 'start_coding_session', status: 'completed', result: { id: 'cs-1', agentSessionId: 'task-1' } } }] } as Pick<AgentMessage, 'parts'>

  it('shows a live card that opens the session view and can be dismissed', async () => {
    const user = userEvent.setup()
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ state: 'complete', phase: 'idle', displayTitle: 'Fix login', repository: 'acme/app', branchName: 'flow/fix', summary: 'Fixed the login redirect' }))
    render(<MemoryRouter><I18nProvider><ChatCodingSessionCards message={message} data={baseData()}/></I18nProvider></MemoryRouter>)
    const card = await screen.findByRole('article', { name: 'Coding session' })
    expect(within(card).getByText('Fix login')).toBeVisible()
    expect(within(card).getByText('started by')).toBeVisible()
    const row = within(card).getByRole('link', { name: /Show session/ })
    expect(row).toHaveTextContent('Completed')
    expect(row).toHaveTextContent('Fixed the login redirect')
    expect(row).toHaveAttribute('href', '/workspace/diff/cs-1')
    expect(within(card).getByRole('link', { name: 'Open issue' })).toHaveAttribute('href', expect.stringContaining('#agent-session-task-1'))
    await user.click(within(card).getAllByRole('button', { name: 'Dismiss' })[0])
    expect(screen.queryByRole('article')).toBeNull()
    expect(localStorage.getItem('flow:coding-sessions:dismissed:workspace:user-1')).toContain('cs-1')
  })

  it('falls back to the session dialog where no app router is mounted', async () => {
    const user = userEvent.setup()
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ state: 'complete', phase: 'idle', displayTitle: 'Fix login' }))
    render(<I18nProvider><ChatCodingSessionCards message={message} data={baseData()}/></I18nProvider>)
    await user.click(await screen.findByRole('button', { name: /Show session/ }))
    const dialog = await screen.findByRole('dialog', { name: /Coding session/ })
    expect(within(dialog).getByRole('region', { name: 'Coding session' })).toBeVisible()
  })

  it('renders nothing for replies without a coding session', () => {
    const { container } = render(<I18nProvider><ChatCodingSessionCards message={{ parts: [] }} data={baseData()}/></I18nProvider>)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('sessions list', () => {
  it('lists the viewer’s sessions, opens one and dismisses finished ones', async () => {
    const user = userEvent.setup()
    const working = makeSession({ id: 'cs-a', displayTitle: 'Working one', updatedAt: '2026-10-02T00:00:00Z' })
    const done = makeSession({ id: 'cs-b', displayTitle: 'Done one', state: 'complete', phase: 'idle', updatedAt: '2026-10-01T00:00:00Z' })
    vi.mocked(api.fetchCodingSessions).mockResolvedValue([done, working])
    vi.mocked(api.fetchCodingSession).mockResolvedValue(working)
    render(<MemoryRouter><I18nProvider><CodingSessionsList data={baseData()} creatorId="user-1"/></I18nProvider></MemoryRouter>)
    const list = await screen.findByRole('region', { name: 'Coding sessions' })
    expect(api.fetchCodingSessions).toHaveBeenCalledWith({ issueId: undefined, creatorId: 'user-1', state: undefined })
    expect(within(list).getAllByRole('listitem').map(item => item.textContent)).toEqual([expect.stringContaining('Working one'), expect.stringContaining('Done one')])
    expect(within(list).queryByRole('button', { name: 'Dismiss Working one' })).toBeNull()
    await user.click(within(list).getByRole('button', { name: 'Dismiss Done one' }))
    expect(within(list).queryByText('Done one')).toBeNull()
    expect(within(list).getByRole('link', { name: /Working one/ })).toHaveAttribute('href', '/workspace/diff/cs-a')
  })
})

describe('issue activity', () => {
  it('mounts the session view in the agent thread of a task that anchors a coding session', async () => {
    vi.mocked(listApplicationTasks).mockResolvedValue([{ ...task, codingSessionId: 'cs-1' }])
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ repository: 'acme/app' }))
    render(<MemoryRouter><I18nProvider><IssueAgentTasks issue={makeIssue({ delegate: flowAgent })} data={baseData()}/></I18nProvider></MemoryRouter>)
    const row = await screen.findByRole('link', { name: /Show session/ })
    expect(row).toHaveTextContent('Thinking…')
    expect(row).toHaveAttribute('href', '/workspace/diff/cs-1')
    // The delegated session is a session card: "<Agent> started by <member>", no message thread or reply box.
    const card = screen.getByRole('article', { name: 'Coding session' })
    expect(card).toHaveTextContent(/Flow\s*started by\s*Viewer/)
    expect(screen.queryByRole('textbox', { name: 'Reply to agent' })).toBeNull()
    expect(getApplicationTask).not.toHaveBeenCalled()
  })

  it('shows the setup line, then "Finished" with the PR chip, and archives from the card options', async () => {
    const user = userEvent.setup()
    vi.mocked(listApplicationTasks).mockResolvedValue([{ ...task, codingSessionId: 'cs-1' }])
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'provisioning', repository: 'acme/app' }))
    const view = render(<MemoryRouter><I18nProvider><IssueAgentTasks issue={makeIssue({ delegate: flowAgent })} data={baseData()}/></I18nProvider></MemoryRouter>)
    expect(await screen.findByRole('link', { name: /Show session/ })).toHaveTextContent('Setting up acme/app…')
    view.unmount()
    const pr = { sessionId: 'cs-1', codeHost: 'github' as const, hostName: 'github.com', repository: 'acme/app', number: 1, url: 'https://github.com/acme/app/pull/1', title: 'Add farewell', state: 'open' as const, draft: true, headBranch: 'flow/x', baseBranch: 'main', createdAt: '', updatedAt: '' }
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ state: 'complete', phase: 'done', repository: 'acme/app', summary: 'Added farewell()', diffs: [{ path: 'a.js', additions: 4, deletions: 0, status: 'modified' }], latestPullRequest: pr }))
    render(<MemoryRouter><I18nProvider><IssueAgentTasks issue={makeIssue({ delegate: flowAgent })} data={baseData()}/></I18nProvider></MemoryRouter>)
    const row = await screen.findByRole('link', { name: /Show session/ })
    await waitFor(() => expect(row).toHaveTextContent('FinishedAdded farewell()#1'))
    await user.click(screen.getByRole('button', { name: 'Agent chat options' }))
    expect(screen.getAllByRole('menuitem').map(item => item.textContent)).toEqual(['Copy link', 'Unsubscribe from chat', 'Archive'])
    await user.click(screen.getByRole('menuitem', { name: 'Archive' }))
    await waitFor(() => expect(screen.queryByRole('article', { name: 'Coding session' })).toBeNull())
  })

  it('shows the running tool step and leaves the feed once the session is archived', async () => {
    vi.mocked(listApplicationTasks).mockResolvedValue([{ ...task, codingSessionId: 'cs-1' }])
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ currentTurnId: 't1', currentStep: { turnId: 't1', action: 'Bash', parameter: 'git status --short', at: '' } }))
    const view = render(<MemoryRouter><I18nProvider><IssueAgentTasks issue={makeIssue({ delegate: flowAgent })} data={baseData()}/></I18nProvider></MemoryRouter>)
    const row = await screen.findByRole('link', { name: /Show session/ })
    expect(row).toHaveTextContent('Bashgit status --short')
    expect(within(row).getByText('git status --short').tagName).toBe('CODE')
    view.unmount()
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ state: 'complete', phase: 'done', archivedAt: '2026-10-19T18:40:00Z' }))
    render(<MemoryRouter><I18nProvider><IssueAgentTasks issue={makeIssue({ delegate: flowAgent })} data={baseData()}/></I18nProvider></MemoryRouter>)
    await waitFor(() => expect(api.fetchCodingSession).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.queryByRole('article', { name: 'Coding session' })).toBeNull())
  })

  it('opens a session with a synced pull request on its review page with the agent panel', async () => {
    vi.mocked(listApplicationTasks).mockResolvedValue([{ ...task, codingSessionId: 'cs-1' }])
    const pr = { sessionId: 'cs-1', codeHost: 'github' as const, hostName: 'github.com', repository: 'acme/app', number: 1, url: 'https://github.com/acme/app/pull/1', title: 'Add farewell', state: 'open' as const, draft: true, headBranch: 'flow/x', baseBranch: 'main', reviewId: 'review-1', createdAt: '', updatedAt: '' }
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ state: 'complete', phase: 'idle', latestPullRequest: pr }))
    const reviews = [{ id: 'review-1', slugId: 'add-farewell-0123456789ab', provider: 'github', number: 1, repositoryOwner: 'acme', repositoryName: 'app' } as CodeReview]
    render(<MemoryRouter><I18nProvider><IssueAgentTasks issue={makeIssue({ delegate: flowAgent })} data={baseData({ reviews })}/></I18nProvider></MemoryRouter>)
    expect(await screen.findByRole('link', { name: /Show session/ })).toHaveAttribute('href', '/workspace/review/add-farewell-0123456789ab/changes?showAgent=true&agentSessionId=cs-1')
  })

  it('matches the session by anchor when the task has no session id', async () => {
    vi.mocked(api.fetchCodingSessions).mockResolvedValue([makeSession({ id: 'other', agentSessionId: 'task-9' }), makeSession({ agentSessionId: 'task-1', branchName: 'flow/anchored' })])
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ agentSessionId: 'task-1', branchName: 'flow/anchored' }))
    render(<MemoryRouter><I18nProvider><AgentTaskCodingSession task={task} data={baseData()}/></I18nProvider></MemoryRouter>)
    expect(await screen.findByRole('link', { name: /Show session/ })).toHaveAttribute('href', '/workspace/diff/cs-1')
    expect(api.fetchCodingSessions).toHaveBeenCalledWith({ issueId: 'issue-1', creatorId: undefined, state: undefined })
  })

  it('renders nothing when coding sessions are unavailable', async () => {
    vi.mocked(api.fetchCodingStatus).mockResolvedValue({ enabled: false } as never)
    const { container } = render(<I18nProvider><AgentTaskCodingSession task={{ ...task, codingSessionId: 'cs-1' }} data={baseData()}/></I18nProvider>)
    await waitFor(() => expect(api.fetchCodingStatus).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
    expect(api.fetchCodingSession).not.toHaveBeenCalled()
  })
})

describe('reviews', () => {
  const review = { id: 'review-1', status: 'open', headBranch: 'flow/fix', provider: 'github' } as const

  it("shows the session's agent panel on the review page; Close chat closes it", async () => {
    const user = userEvent.setup(), onClose = vi.fn(), onToggle = vi.fn()
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ state: 'complete', phase: 'idle', displayTitle: 'Add farewell function' }))
    render(<MemoryRouter><I18nProvider><ReviewAgentPanel sessionId="cs-1" data={baseData()} onClose={onClose}/><ReviewAgentToggle shown onToggle={onToggle}/></I18nProvider></MemoryRouter>)
    expect(await screen.findByRole('heading', { name: 'Add farewell function' })).toBeVisible()
    const panel = screen.getByRole('complementary', { name: 'Agent' })
    expect(vi.mocked(api.fetchCodingSession).mock.calls[0]?.[0]).toBe('cs-1')
    await user.click(within(panel).getByRole('button', { name: 'Close chat' }))
    expect(onClose).toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Hide agent' }))
    expect(onToggle).toHaveBeenCalled()
  })

  it('fixes a pull request with the agent and opens the session', async () => {
    const user = userEvent.setup()
    vi.mocked(api.startReviewCodingSession).mockResolvedValue(makeSession({ id: 'cs-r' }))
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ id: 'cs-r' }))
    render(<MemoryRouter><I18nProvider><ReviewCodingSessionActions review={review} data={baseData()}/></I18nProvider></MemoryRouter>)
    await user.click(await screen.findByRole('button', { name: 'Fix with agent' }))
    expect(api.startReviewCodingSession).toHaveBeenCalledWith('review-1', { kind: 'fix', prompt: undefined })
    expect(await screen.findByRole('dialog', { name: 'Coding session' })).toBeVisible()
  })

  it.each([['Fix lint', 'lint'], ['Fix tests', 'test'], ['Rebase on base branch', 'rebase']] as const)('runs the %s quick action', async (label, kind) => {
    const user = userEvent.setup()
    vi.mocked(api.startReviewCodingSession).mockResolvedValue(makeSession({ id: 'cs-r' }))
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ id: 'cs-r' }))
    render(<MemoryRouter><I18nProvider><ReviewCodingSessionActions review={review} data={baseData()}/></I18nProvider></MemoryRouter>)
    await user.click(await screen.findByRole('button', { name: 'More coding agent actions' }))
    await user.click(await screen.findByRole('menuitem', { name: label }))
    await waitFor(() => expect(api.startReviewCodingSession).toHaveBeenCalledWith('review-1', { kind, prompt: undefined }))
  })

  it('confirms a rebase of a branch Flow did not create', async () => {
    const user = userEvent.setup()
    vi.mocked(api.startReviewCodingSession).mockResolvedValue(makeSession({ id: 'cs-r' }))
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ id: 'cs-r' }))
    render(<MemoryRouter><I18nProvider><ReviewCodingSessionActions review={{ ...review, headBranch: 'feature/x' }} data={baseData()}/></I18nProvider></MemoryRouter>)
    await user.click(await screen.findByRole('button', { name: 'More coding agent actions' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Rebase on base branch' }))
    const dialog = await screen.findByRole('dialog', { name: 'Rebase this branch?' })
    expect(api.startReviewCodingSession).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Rebase' }))
    expect(api.startReviewCodingSession).toHaveBeenCalledWith('review-1', { kind: 'rebase', prompt: undefined })
  })

  it('sends a free follow-up prompt', async () => {
    const user = userEvent.setup()
    vi.mocked(api.startReviewCodingSession).mockResolvedValue(makeSession({ id: 'cs-r' }))
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ id: 'cs-r' }))
    render(<MemoryRouter><I18nProvider><ReviewCodingSessionActions review={review} data={baseData()}/></I18nProvider></MemoryRouter>)
    await user.click(await screen.findByRole('button', { name: 'More coding agent actions' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Ask the agent…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Ask the coding agent' })
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Prompt' }), { target: { value: 'Rename the helper' } })
    await user.click(within(dialog).getByRole('button', { name: 'Send' }))
    expect(api.startReviewCodingSession).toHaveBeenCalledWith('review-1', { kind: 'followUp', prompt: 'Rename the helper' })
  })

  it('reports a start error with its next action', async () => {
    const user = userEvent.setup()
    vi.mocked(api.startReviewCodingSession).mockRejectedValue(new ApiError('limit', 402, 'quotaExceeded'))
    render(<MemoryRouter><I18nProvider><ReviewCodingSessionActions review={review} data={baseData()}/></I18nProvider></MemoryRouter>)
    await user.click(await screen.findByRole('button', { name: 'Fix with agent' }))
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Spend limit reached', expect.objectContaining({ action: expect.objectContaining({ label: 'Review usage & limits' }) })))
  })

  it('is hidden on merged pull requests and when unavailable', async () => {
    const { container, unmount } = render(<I18nProvider><ReviewCodingSessionActions review={{ ...review, status: 'merged' }} data={baseData()}/></I18nProvider>)
    await waitFor(() => expect(api.fetchCodingStatus).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
    unmount()
    resetCodingAvailabilityCache()
    vi.mocked(api.fetchCodingStatus).mockResolvedValue({ enabled: false } as never)
    const second = render(<I18nProvider><ReviewCodingSessionActions review={review} data={baseData()}/></I18nProvider>)
    await waitFor(() => expect(api.fetchCodingStatus).toHaveBeenCalledTimes(2))
    expect(second.container).toBeEmptyDOMElement()
  })

  const comment = { id: 'e1', type: 'review_commented', body: 'Handle null here', path: 'src/a.ts', line: 12, actor: { ...teammate }, createdAt: '' } as ReviewEvent

  it('sends a review comment to the coding agent', async () => {
    const user = userEvent.setup()
    vi.mocked(api.startReviewCodingSession).mockResolvedValue(makeSession())
    render(<I18nProvider><PullRequestCommentActions event={comment} reviewId="review-1"/></I18nProvider>)
    await waitFor(() => expect(api.fetchCodingStatus).toHaveBeenCalled())
    await user.click(screen.getByRole('button', { name: 'Comment actions' }))
    await user.click(await screen.findByRole('button', { name: 'Send comment to agent' }))
    expect(api.startReviewCodingSession).toHaveBeenCalledWith('review-1', { kind: 'followUp', prompt: reviewCommentPrompt(comment) })
    expect(reviewCommentPrompt(comment)).toBe('Address this review comment from Teammate on `src/a.ts:12`:\n\nHandle null here')
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Sent comment to coding agent'))
    expect(createAgentSession).not.toHaveBeenCalled()
  })

  it('keeps the chat dispatch when coding sessions are off', async () => {
    const user = userEvent.setup()
    vi.mocked(api.fetchCodingStatus).mockResolvedValue({ enabled: false } as never)
    vi.mocked(createAgentSession).mockResolvedValue({ id: 'chat' } as never)
    render(<I18nProvider><PullRequestCommentActions event={comment} reviewId="review-1"/></I18nProvider>)
    await waitFor(() => expect(api.fetchCodingStatus).toHaveBeenCalled())
    await user.click(screen.getByRole('button', { name: 'Comment actions' }))
    await user.click(await screen.findByRole('button', { name: 'Send to agent' }))
    expect(createAgentSession).toHaveBeenCalled()
    expect(api.startReviewCodingSession).not.toHaveBeenCalled()
  })
})

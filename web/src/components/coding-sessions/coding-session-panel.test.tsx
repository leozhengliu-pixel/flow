import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import * as api from '@/lib/coding-sessions-api'
import { AGENT_TASK_ACTIVITY_EVENT, getApplicationTask, replyApplicationTask, type ApplicationActivity, type ApplicationTask } from '@/lib/application-agents'
import type { CodeReview } from '@/types/flow'
import type { CodingSession, CodingSessionState } from '@/types/coding-sessions'
import { CodingSessionPanel, CodingSessionView } from './coding-session-panel'
import { CODING_SESSION_EVENT, dispatchCodingSessionRealtimeEvent } from './coding-session-events'
import { CODING_ERROR_CODES } from './coding-session-model'
import { makeSession } from './coding-session-test-fixtures'

vi.mock('@/lib/coding-sessions-api', () => ({
  fetchCodingStatus: vi.fn(), fetchCodingSession: vi.fn(), fetchCodingSessions: vi.fn(), stopCodingSession: vi.fn(), restartCodingSession: vi.fn(),
  approveCodingSession: vi.fn(), dismissCodingSession: vi.fn(), createCodingSessionPullRequest: vi.fn(), promptCodingSession: vi.fn(), fetchCodingSandboxLogs: vi.fn(),
  fixCodingEnvironmentWithAgent: vi.fn(), startReviewCodingSession: vi.fn(), startIssueCodingSession: vi.fn(), fetchCodingEnvironments: vi.fn(),
  fetchCodingSessionArtifacts: vi.fn(async () => []),
}))
vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), getApplicationTask: vi.fn(), replyApplicationTask: vi.fn() }))
vi.mock('@/components/agent/agent-rich-text', () => ({ AgentRichText: ({ content }: { content: string }) => <div>{content}</div> }))

const task = { id: 'task-1', issueId: 'issue-1', teamId: 'team-1', appUserId: 'app', creatorId: viewer.id, status: 'active', prompt: '', trigger: 'delegation', version: 4, updatedAt: '' } as ApplicationTask
const activity = (id: string, type: string, body: string, extra: Partial<ApplicationActivity> = {}): ApplicationActivity => ({ id, sessionId: 'task-1', actorId: 'app', type, body, createdAt: '', ...extra })
const data = (overrides = {}) => makeBootstrap({ reviews: [], ...overrides })

function renderPanel(session: CodingSession, props: Partial<Parameters<typeof CodingSessionPanel>[0]> = {}) {
  const onChange = vi.fn()
  const result = render(<I18nProvider><CodingSessionPanel session={session} data={data()} onChange={onChange} {...props}/></I18nProvider>)
  return { ...result, onChange }
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities: [] })
})

describe('coding session panel', () => {
  it.each<[CodingSessionState, string]>([
    ['pending', 'Starting'], ['active', 'Working'], ['stopping', 'Stopping…'], ['complete', 'Finished'],
    ['awaitingInput', 'Needs input'], ['error', 'Errored'], ['stale', 'Stopped'],
  ])('shows the %s state as %s', (state, label) => {
    renderPanel(makeSession({ state, phase: state === 'pending' ? 'provisioning' : state === 'awaitingInput' ? 'awaitingUser' : 'done' }))
    expect(screen.getByRole('region', { name: 'Coding session' })).toHaveAttribute('data-state', state)
    expect(screen.getAllByText(label)[0]).toBeVisible()
  })

  it('shows the session screenshots (track I artifacts mounted directly)', () => {
    renderPanel(makeSession({ artifacts: [{ id: 'a1', sessionId: 'session-1', kind: 'screenshot', label: 'Empty state', url: '/signed/a1.png', contentUrl: '/api/coding/sessions/session-1/artifacts/a1/content', contentType: 'image/png', sizeBytes: 100, createdAt: '2026-10-18T10:00:00Z' }] }))
    expect(screen.getByRole('region', { name: 'Screenshots' })).toBeVisible()
    expect(screen.getByRole('img', { name: 'Empty state' })).toBeInTheDocument()
  })

  it('shows phase progress, harness, model and cost while working', () => {
    renderPanel(makeSession({ state: 'pending', phase: 'starting', costMicros: 2_500_000 }))
    const steps = within(screen.getByRole('list', { name: 'Session progress' })).getAllByRole('listitem')
    expect(steps.find(step => step.getAttribute('aria-current') === 'step')).toHaveTextContent('Starting sandbox')
    expect(screen.getByText('Claude Code · claude-sonnet-4-5 (Auto)')).toBeVisible()
    expect(screen.getByLabelText('Cost')).toHaveTextContent('$2.50')
  })

  it('stops a working session', async () => {
    const user = userEvent.setup(), stopped = makeSession({ state: 'stopping', phase: 'stopping', version: 2 })
    vi.mocked(api.stopCodingSession).mockResolvedValue(stopped)
    const { onChange } = renderPanel(makeSession())
    await user.click(screen.getByRole('button', { name: 'Stop' }))
    expect(api.stopCodingSession).toHaveBeenCalledWith('cs-1')
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(stopped))
  })

  it('approves and rejects a session waiting for approval', async () => {
    const user = userEvent.setup()
    vi.mocked(api.approveCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued' }))
    renderPanel(makeSession({ state: 'awaitingInput', phase: 'awaitingApproval' }))
    expect(screen.queryByRole('button', { name: 'Stop' })).toBeNull()
    expect(screen.queryByRole('textbox', { name: 'Follow-up prompt' })).toBeNull()
    const group = screen.getByRole('group', { name: 'Approval required' })
    await user.click(within(group).getByRole('button', { name: 'Approve' }))
    expect(api.approveCodingSession).toHaveBeenLastCalledWith('cs-1')
    await user.click(within(group).getByRole('button', { name: 'Reject' }))
    expect(api.approveCodingSession).toHaveBeenLastCalledWith('cs-1', true)
  })

  it('sends a follow-up prompt', async () => {
    const user = userEvent.setup()
    vi.mocked(api.promptCodingSession).mockResolvedValue(makeSession({ version: 3 }))
    renderPanel(makeSession({ state: 'complete', phase: 'idle' }))
    await user.type(screen.getByRole('textbox', { name: 'Follow-up prompt' }), 'Also add tests')
    await user.click(screen.getByRole('button', { name: 'Send follow-up' }))
    expect(api.promptCodingSession).toHaveBeenCalledWith('cs-1', 'Also add tests')
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Follow-up prompt' })).toHaveValue(''))
  })

  it('restarts, restarts with the default model, creates a PR and archives a stopped session from the session menu', async () => {
    const user = userEvent.setup(), onDismiss = vi.fn()
    vi.mocked(api.restartCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued' }))
    vi.mocked(api.createCodingSessionPullRequest).mockResolvedValue(makeSession({ state: 'active', phase: 'publishing' }))
    renderPanel(makeSession({ state: 'stale', phase: 'done', modelSelection: 'gpt-5', branchName: 'flow/x', commitsByTurnActivityId: { a: [{ sha: 'abcdef123', branch: 'flow/x', message: 'Fix bug\n\nbody', signed: true, createdAt: '' }] } }), { onDismiss })
    expect(screen.getByText('abcdef1')).toBeVisible()
    expect(screen.getByText('Signed')).toBeVisible()
    const choose = async (name: string) => {
      await user.click(screen.getByRole('button', { name: 'Session details' }))
      await user.click(await screen.findByRole('menuitem', { name }))
    }
    await choose('Restart')
    expect(api.restartCodingSession).toHaveBeenLastCalledWith('cs-1')
    await choose('Restart with default model')
    expect(api.restartCodingSession).toHaveBeenLastCalledWith('cs-1', { useDefaultModel: true })
    await choose('Create pull request')
    expect(api.createCodingSessionPullRequest).toHaveBeenCalledWith('cs-1')
    await choose('Archive')
    expect(onDismiss).toHaveBeenCalled()
  })

  it('shows the session menu with the model and copies the session URL', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn(async () => undefined)
    Object.defineProperty(window.navigator, 'clipboard', { value: { writeText, readText: vi.fn() }, configurable: true })
    renderPanel(makeSession({ branchName: 'flow/x' }))
    await user.click(screen.getByRole('button', { name: 'Session details' }))
    expect(await screen.findByText('Claude Code · claude-sonnet-4-5 (Auto)', { selector: '[role=menuitem] span' })).toBeVisible()
    await user.click(screen.getByRole('menuitem', { name: 'Copy' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Session URL' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(expect.stringMatching(/\/agent$|#agent-session-task-1$/)))
  })

  it('shows the agent working with a running timer and stops it from the composer', async () => {
    const user = userEvent.setup()
    vi.mocked(api.stopCodingSession).mockResolvedValue(makeSession({ state: 'stopping', phase: 'stopping' }))
    renderPanel(makeSession({ turnStartedAt: new Date(Date.now() - 162_000).toISOString() }))
    const working = screen.getByRole('status')
    expect(working).toHaveTextContent('Thinking…')
    expect(within(working).getByLabelText('Elapsed time')).toHaveTextContent(/^02:4\d$/)
    await user.click(within(screen.getByRole('complementary', { name: 'Agent' })).getByRole('button', { name: 'Stop' }))
    expect(api.stopCodingSession).toHaveBeenCalledWith('cs-1')
  })

  it('shows "No changes yet" until the agent changes something', () => {
    const { unmount } = renderPanel(makeSession())
    expect(screen.getByText('No changes yet')).toBeVisible()
    unmount()
    renderPanel(makeSession({ diffs: [{ path: 'src/a.ts', additions: 1, deletions: 0, status: 'modified' }] }))
    expect(screen.queryByText('No changes yet')).toBeNull()
  })

  it('retries an errored session from its error card', async () => {
    const user = userEvent.setup()
    vi.mocked(api.restartCodingSession).mockResolvedValue(makeSession())
    renderPanel(makeSession({ state: 'error', phase: 'done', errorCode: 'sandboxLost' }))
    await user.click(within(screen.getByRole('alert')).getByRole('button', { name: 'Retry' }))
    expect(api.restartCodingSession).toHaveBeenCalledWith('cs-1')
  })

  it('shows an action error', async () => {
    const user = userEvent.setup()
    vi.mocked(api.stopCodingSession).mockRejectedValue(new Error('Conflict'))
    renderPanel(makeSession())
    await user.click(screen.getByRole('button', { name: 'Stop' }))
    expect(await screen.findByText('Conflict')).toBeVisible()
  })

  it.each(CODING_ERROR_CODES)('renders clear copy for %s', code => {
    renderPanel(makeSession({ state: 'error', phase: 'done', errorCode: code, codingEnvironmentId: 'env-1', branchName: 'flow/x', modelSelection: 'gpt-5', sandboxes: [{ id: 'sb', kind: 'session', status: 'failed', createdAt: '' }] }))
    const alert = screen.getByRole('alert')
    expect(alert).toHaveAttribute('data-code', code)
    expect(alert.querySelector('strong')?.textContent).toBeTruthy()
  })

  it('links to the fix for connection, signing key and credit errors', () => {
    const { unmount } = renderPanel(makeSession({ state: 'error', phase: 'done', errorCode: 'noGitHubConnection' }))
    expect(screen.getByRole('link', { name: 'Connect GitHub' })).toHaveAttribute('href', '/workspace/settings/integrations/github')
    unmount()
    const second = renderPanel(makeSession({ state: 'error', phase: 'done', errorCode: 'missingCommitSigningKey' }))
    expect(screen.getByRole('link', { name: 'Upload signing key' })).toHaveAttribute('href', '/workspace/settings/account/security/commit-signing-key')
    second.unmount()
    renderPanel(makeSession({ state: 'error', phase: 'done', errorCode: 'quotaExceeded' }))
    expect(screen.getByRole('link', { name: 'Review usage & limits' })).toHaveAttribute('href', '/workspace/settings/usage/spend-limits')
  })

  it('restarts with the default model after modelUnavailable', async () => {
    const user = userEvent.setup()
    vi.mocked(api.restartCodingSession).mockResolvedValue(makeSession())
    renderPanel(makeSession({ state: 'error', phase: 'done', errorCode: 'modelUnavailable', modelSelection: 'claude-opus-4-1' }))
    expect(screen.getAllByRole('button', { name: 'Restart with default model' })).toHaveLength(1)
    await user.click(screen.getByRole('button', { name: 'Restart with default model' }))
    expect(api.restartCodingSession).toHaveBeenCalledWith('cs-1', { useDefaultModel: true })
  })

  it('fixes a failed environment with the agent and views the setup logs', async () => {
    const user = userEvent.setup(), onOpenSession = vi.fn(), fixer = makeSession({ id: 'cs-fix', source: 'environmentSetup' })
    vi.mocked(api.fixCodingEnvironmentWithAgent).mockResolvedValue(fixer)
    vi.mocked(api.fetchCodingSandboxLogs).mockResolvedValue({ logs: 'npm ERR! missing script', complete: true })
    renderPanel(makeSession({ state: 'error', phase: 'done', errorCode: 'environmentSetupFailed', codingEnvironmentId: 'env-1', snapshotId: 'snap-1', sandboxes: [{ id: 'sb-1', kind: 'session', status: 'failed', createdAt: '' }] }), { onOpenSession })
    await user.click(screen.getByRole('button', { name: 'Fix with agent' }))
    expect(api.fixCodingEnvironmentWithAgent).toHaveBeenCalledWith('env-1', 'snap-1')
    await waitFor(() => expect(onOpenSession).toHaveBeenCalledWith(fixer))
    const alert = screen.getByRole('alert')
    await user.click(within(alert).getByRole('button', { name: 'View logs' }))
    expect(await screen.findAllByText('npm ERR! missing script')).not.toHaveLength(0)
    expect(api.fetchCodingSandboxLogs).toHaveBeenCalledWith('cs-1', 'sb-1')
  })

  it('shows admins the raw reason of sandbox start problems only', () => {
    const session = makeSession({ state: 'error', phase: 'done', errorCode: 'imagePullFailed', error: 'ErrImagePull: not found' })
    const { unmount } = renderPanel(session)
    expect(screen.queryByText('ErrImagePull: not found')).toBeNull()
    unmount()
    render(<I18nProvider><CodingSessionPanel session={session} data={data({ viewerRole: 'admin' })} onChange={vi.fn()}/></I18nProvider>)
    expect(screen.getByText('ErrImagePull: not found')).toBeVisible()
  })

  it('shows PR/MR links with state and CI, and fixes failing checks', async () => {
    const user = userEvent.setup(), onOpenSession = vi.fn()
    vi.mocked(api.startReviewCodingSession).mockResolvedValue(makeSession({ id: 'cs-ci' }))
    const review = { id: 'review-1', provider: 'github', number: 12, repositoryOwner: 'acme', repositoryName: 'app', checks: [{ id: 'c', name: 'ci', status: 'failed' }] } as unknown as CodeReview
    const pr = { sessionId: 'cs-1', codeHost: 'github' as const, hostName: 'github.com', repository: 'acme/app', number: 12, url: 'https://github.com/acme/app/pull/12', title: 'Fix login', state: 'open' as const, draft: true, headBranch: 'flow/x', baseBranch: 'main', createdAt: '', updatedAt: '' }
    const mr = { ...pr, codeHost: 'gitlab' as const, number: 3, url: 'https://gitlab.com/acme/app/-/merge_requests/3', state: 'merged' as const, draft: false }
    render(<I18nProvider><CodingSessionPanel session={makeSession({ state: 'complete', phase: 'idle', pullRequests: [pr, mr] })} data={data({ reviews: [review] })} onChange={vi.fn()} onOpenSession={onOpenSession}/></I18nProvider>)
    const items = within(screen.getByRole('list', { name: 'Pull requests' })).getAllByRole('listitem')
    expect(items[0]).toHaveAttribute('data-state', 'draft')
    expect(within(items[0]).getByText('Draft')).toBeVisible()
    expect(within(items[0]).getByText('Checks failed')).toBeVisible()
    expect(within(items[0]).getByRole('link', { name: /Open pull request #12/ })).toHaveAttribute('href', pr.url)
    expect(within(items[1]).getByText('Merged')).toBeVisible()
    expect(within(items[1]).getByRole('link', { name: /Open merge request !3/ })).toBeVisible()
    await user.click(within(items[0]).getByRole('button', { name: 'Fix failing checks' }))
    expect(api.startReviewCodingSession).toHaveBeenCalledWith('review-1', { kind: 'fix' })
    await waitFor(() => expect(onOpenSession).toHaveBeenCalled())
  })

  it('shows sandbox info and logs', async () => {
    const user = userEvent.setup()
    vi.mocked(api.fetchCodingSandboxLogs).mockResolvedValue({ logs: 'cloning…', complete: false })
    renderPanel(makeSession({ repository: 'acme/app', branchName: 'flow/fix-login', baseRef: 'main', region: 'eu', sandboxes: [{ id: 'sb-1', kind: 'session', status: 'running', size: 'large', region: 'eu-west', createdAt: '' }] }))
    const sandbox = screen.getByRole('region', { name: 'Sandbox' })
    for (const text of ['acme/app', 'flow/fix-login', 'main', 'Large', 'eu-west', 'Running']) expect(within(sandbox).getByText(text)).toBeVisible()
    await user.click(within(sandbox).getByRole('button', { name: 'View logs' }))
    expect(await screen.findByLabelText('Sandbox logs')).toHaveTextContent('cloning…')
    await user.click(screen.getByRole('button', { name: 'Refresh logs' }))
    expect(api.fetchCodingSandboxLogs).toHaveBeenCalledTimes(2)
  })

  it('links external logs directly', () => {
    renderPanel(makeSession({ sandboxes: [{ id: 'sb', kind: 'session', status: 'stopped', logsUrl: 'https://logs.example/sb', createdAt: '' }] }))
    expect(screen.getByRole('link', { name: 'View logs' })).toHaveAttribute('href', 'https://logs.example/sb')
  })

  it('shows the plan, diff summary and summary', async () => {
    const user = userEvent.setup()
    renderPanel(makeSession({ state: 'complete', phase: 'idle', summary: 'Fixed the login bug', plan: [{ content: 'Reproduce', status: 'completed' }, { content: 'Fix', status: 'inProgress' }], diffs: [{ path: 'src/a.ts', additions: 5, deletions: 2, status: 'modified' }, { path: 'src/b.ts', additions: 1, deletions: 0, status: 'added' }] }))
    expect(within(screen.getByRole('list', { name: 'Plan' })).getAllByRole('listitem')).toHaveLength(2)
    expect(screen.getByText('Fixed the login bug')).toBeVisible()
    await user.click(screen.getByText('2 files changed'))
    expect(screen.getByText('src/a.ts')).toBeVisible()
  })

  it('renders the activity stream with every activity type', async () => {
    vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities: [
      activity('1', 'prompt', 'Fix login', { actorId: viewer.id }),
      activity('2', 'thought', 'Looking at auth.ts'),
      activity('3', 'action', '', { metadata: { action: 'Ran command', parameter: 'npm test', result: '3 passed' } }),
      activity('4', 'response', 'Done, opened a PR'),
      activity('5', 'error', 'Lint failed'),
      activity('6', 'feedback', '👍'),
    ] })
    renderPanel(makeSession())
    const list = await screen.findByRole('list', { name: 'Session activity' })
    expect(within(list).getAllByRole('listitem').map(item => item.getAttribute('data-type'))).toEqual(['prompt', 'thought', 'action', 'response', 'error'])
    expect(within(list).getByText('You')).toBeVisible()
    expect(within(list).getByText('npm test')).toBeVisible()
    expect(within(list).getByText('Done, opened a PR')).toBeVisible()
    expect(within(list).getByText('Lint failed')).toBeVisible()
  })

  it('folds the steps inside an issue thread and hides the composer', async () => {
    const user = userEvent.setup()
    vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities: [activity('1', 'response', 'Answer'), activity('2', 'action', '', { metadata: { action: 'Edited', parameter: 'a.ts' } })] })
    renderPanel(makeSession({ plan: [{ content: 'Step', status: 'pending' }] }), { activity: 'steps' })
    await user.click(await screen.findByRole('button', { name: 'Show 1 steps' }))
    expect(screen.getByText('a.ts')).toBeVisible()
    expect(screen.queryByText('Answer')).toBeNull()
    expect(screen.queryByRole('list', { name: 'Plan' })).toBeNull()
    expect(screen.queryByRole('textbox', { name: 'Follow-up prompt' })).toBeNull()
  })

  it('answers an awaiting-input question on the anchor', async () => {
    const user = userEvent.setup()
    vi.mocked(getApplicationTask).mockResolvedValue({ session: { ...task, status: 'awaitingInput' }, activities: [activity('q', 'elicitation', 'Which repository?', { signal: 'select', signalMetadata: { options: [{ value: 'acme/app' }, { value: 'acme/api' }] } })] })
    vi.mocked(replyApplicationTask).mockResolvedValue({ ...task, version: 5 })
    renderPanel(makeSession({ state: 'awaitingInput', phase: 'awaitingUser' }))
    expect(screen.getByText('The agent asked a question. Answer it to continue.')).toBeVisible()
    await user.click(await screen.findByRole('button', { name: 'acme/api' }))
    await waitFor(() => expect(replyApplicationTask).toHaveBeenCalledWith('workspace', expect.objectContaining({ id: 'task-1' }), 'prompt', 'acme/api'))
  })

  it('preselects the recommended repository so Enter confirms it', async () => {
    const user = userEvent.setup()
    vi.mocked(getApplicationTask).mockResolvedValue({ session: { ...task, status: 'awaitingInput' }, activities: [activity('q', 'elicitation', 'Which repository should I work in?', { signal: 'select', signalMetadata: { selected: 'env-api', options: [{ value: 'env-web', label: 'Web (acme/web)' }, { value: 'env-api', label: 'API (acme/api)', recommended: true }] } })] })
    vi.mocked(replyApplicationTask).mockResolvedValue({ ...task, version: 5 })
    renderPanel(makeSession({ state: 'awaitingInput', phase: 'awaitingUser' }))
    const recommended = await screen.findByRole('button', { name: /API \(acme\/api\)/ })
    expect(recommended).toHaveTextContent('Recommended')
    await waitFor(() => expect(recommended).toHaveFocus())
    await user.keyboard('{Enter}')
    await waitFor(() => expect(replyApplicationTask).toHaveBeenCalledWith('workspace', expect.objectContaining({ id: 'task-1' }), 'prompt', 'env-api'))
  })

  it('ends a session waiting for an answer', async () => {
    const user = userEvent.setup()
    vi.mocked(api.dismissCodingSession).mockResolvedValue(makeSession({ state: 'stale', phase: 'done', version: 2 }))
    const { onChange } = renderPanel(makeSession({ state: 'awaitingInput', phase: 'awaitingUser' }))
    await user.click(within(screen.getByRole('group', { name: 'Needs input' })).getByRole('button', { name: 'End session' }))
    expect(api.dismissCodingSession).toHaveBeenCalledWith('cs-1')
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ state: 'stale' })))
  })

  it('names who stopped a session', () => {
    renderPanel(makeSession({ state: 'stale', phase: 'done', stoppedBy: 'user-2' }))
    expect(screen.getByText('Stopped by Teammate')).toBeVisible()
  })

  it('offers to link an account for an auth question', async () => {
    vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities: [activity('q', 'elicitation', 'Link GitHub', { signal: 'auth', signalMetadata: { url: 'https://flow.example/link' } })] })
    renderPanel(makeSession({ state: 'awaitingInput', phase: 'awaitingUser' }))
    expect(await screen.findByRole('link', { name: 'Link account' })).toHaveAttribute('href', 'https://flow.example/link')
  })

  it('refetches newer activities on realtime agent task pushes', async () => {
    renderPanel(makeSession())
    await waitFor(() => expect(getApplicationTask).toHaveBeenCalledTimes(1))
    vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities: [activity('9', 'response', 'New answer')] })
    act(() => { window.dispatchEvent(new CustomEvent(AGENT_TASK_ACTIVITY_EVENT, { detail: 'issue-1' })) })
    expect(await screen.findByText('New answer')).toBeVisible()
    expect(getApplicationTask).toHaveBeenLastCalledWith('workspace', 'task-1', '', expect.anything())
  })
})

describe('coding session view (live)', () => {
  it('loads the session and follows coding_session realtime events', async () => {
    vi.mocked(api.fetchCodingSession).mockResolvedValueOnce(makeSession({ state: 'pending', phase: 'queued' })).mockResolvedValue(makeSession({ state: 'complete', phase: 'idle', version: 2 }))
    render(<I18nProvider><CodingSessionView sessionId="cs-1" data={data()}/></I18nProvider>)
    expect(await screen.findByText('Starting')).toBeVisible()
    act(() => { dispatchCodingSessionRealtimeEvent({ type: 'coding_session.updated', aggregateId: 'cs-1', payload: { agentSessionId: 'task-1' } }) })
    expect(await screen.findByText('Finished')).toBeVisible()
    expect(api.fetchCodingSession).toHaveBeenCalledTimes(2)
  })

  it('ignores events for other sessions and reports load errors', async () => {
    vi.mocked(api.fetchCodingSession).mockResolvedValue(makeSession({ state: 'complete', phase: 'idle' }))
    render(<I18nProvider><CodingSessionView sessionId="cs-1" data={data()}/></I18nProvider>)
    await screen.findByText('Finished')
    act(() => { window.dispatchEvent(new CustomEvent(CODING_SESSION_EVENT, { detail: { sessionId: 'other', agentSessionId: 'other-task' } })) })
    await new Promise(resolve => setTimeout(resolve, 250))
    expect(api.fetchCodingSession).toHaveBeenCalledTimes(1)
  })

  it('shows a load error', async () => {
    vi.mocked(api.fetchCodingSession).mockRejectedValue(new Error('Not found'))
    render(<I18nProvider><CodingSessionView sessionId="missing" data={data()}/></I18nProvider>)
    expect(await screen.findByText('Not found')).toBeVisible()
  })

  it('only dispatches coding_session events', () => {
    const listener = vi.fn()
    window.addEventListener(CODING_SESSION_EVENT, listener)
    expect(dispatchCodingSessionRealtimeEvent({ type: 'issue.updated', aggregateId: 'x' })).toBe(false)
    expect(dispatchCodingSessionRealtimeEvent({ type: 'coding_session.created', aggregateId: 'cs-2', payload: { issueId: 'issue-9' } })).toBe(true)
    expect(listener).toHaveBeenCalledTimes(1)
    expect((listener.mock.calls[0][0] as CustomEvent).detail).toEqual({ sessionId: 'cs-2', agentSessionId: undefined, issueId: 'issue-9' })
    window.removeEventListener(CODING_SESSION_EVENT, listener)
  })
})

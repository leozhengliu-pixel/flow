import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import * as api from '@/lib/coding-sessions-api'
import { uploadAgentFile } from '@/lib/api'
import { AGENT_TASK_ACTIVITY_EVENT, getApplicationTask, type ApplicationActivity, type ApplicationTask } from '@/lib/application-agents'
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
  fetchCodingModels: vi.fn(async () => [
    { id: 'claude-sonnet-4-5', label: 'Claude Sonnet 4.5', harness: 'claudeCode' },
    { id: 'claude-opus-4-5', label: 'Claude Opus 4.5', harness: 'claudeCode' },
  ]),
  fetchCodingSessionDiff: vi.fn(async () => ({ source: 'none', additions: 0, deletions: 0, files: [] })), setCodingSessionSubscription: vi.fn(),
  fetchGitHubApp: vi.fn(async () => ({ configured: true, hostName: 'github.com', installations: [] })), startGitHubAppAuthorization: vi.fn(),
}))
vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), getApplicationTask: vi.fn(), replyApplicationTask: vi.fn() }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), uploadAgentFile: vi.fn() }))
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
  vi.mocked(api.fetchCodingSessionDiff).mockResolvedValue({ source: 'none', additions: 0, deletions: 0, files: [] })
  vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities: [] })
})

describe('coding session panel', () => {
  it.each<[CodingSessionState, string]>([
    ['pending', 'Working…'], ['active', 'Working…'], ['stopping', 'Stopping…'], ['complete', 'Finished'],
    ['awaitingInput', 'Needs input'], ['error', 'Error'], ['stale', 'Didn’t start'],
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

  it('shows phase progress, harness and model while working, but no cost', () => {
    renderPanel(makeSession({ state: 'pending', phase: 'starting', costMicros: 2_500_000 }))
    const steps = within(screen.getByRole('list', { name: 'Session progress' })).getAllByRole('listitem')
    expect(steps.find(step => step.getAttribute('aria-current') === 'step')).toHaveTextContent('Starting sandbox')
    expect(screen.getByText('Claude Code · claude-sonnet-4-5 (Auto)')).toBeVisible()
    expect(screen.queryByLabelText('Cost')).toBeNull()
    expect(screen.queryByText('$2.50')).toBeNull()
  })

  it('stops a working session from the empty composer', async () => {
    const user = userEvent.setup(), stopped = makeSession({ state: 'stopping', phase: 'stopping', version: 2 })
    vi.mocked(api.stopCodingSession).mockResolvedValue(stopped)
    const { onChange } = renderPanel(makeSession())
    await user.click(screen.getByRole('button', { name: 'Stop agent' }))
    expect(api.stopCodingSession).toHaveBeenCalledWith('cs-1')
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(stopped))
  })
  it('approves and rejects a session waiting for approval in the composer (no approval card)', async () => {
    const user = userEvent.setup()
    vi.mocked(api.approveCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued' }))
    renderPanel(makeSession({ state: 'awaitingInput', phase: 'awaitingApproval' }))
    expect(screen.queryByRole('button', { name: 'Stop agent' })).toBeNull()
    expect(screen.queryByRole('group', { name: 'Approval required' })).toBeNull()
    expect(screen.getByRole('textbox', { name: 'Send a message to Flow' })).toBeDisabled()
    expect(screen.getByText('Input needed to continue')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Approve' }))
    await user.click(screen.getByRole('button', { name: 'Continue with selected option' }))
    expect(api.approveCodingSession).toHaveBeenLastCalledWith('cs-1', false)
  })
  it('sends a follow-up prompt', async () => {
    const user = userEvent.setup()
    vi.mocked(api.promptCodingSession).mockResolvedValue(makeSession({ version: 3 }))
    renderPanel(makeSession({ state: 'complete', phase: 'idle' }))
    await user.type(screen.getByRole('textbox', { name: 'Send a message to Flow' }), 'Also add tests')
    await user.click(screen.getByRole('button', { name: 'Submit message' }))
    expect(api.promptCodingSession).toHaveBeenCalledWith('cs-1', 'Also add tests', undefined, [])
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Send a message to Flow' })).toHaveValue(''))
  })
  it('starts over with the default model and archives from the session menu; no Restart or Create pull request', async () => {
    const user = userEvent.setup(), onDismiss = vi.fn()
    vi.mocked(api.restartCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued' }))
    renderPanel(makeSession({ state: 'stale', phase: 'done', modelSelection: 'gpt-5', branchName: 'flow/x', commitsByTurnActivityId: { a: [{ sha: 'abcdef123', branch: 'flow/x', message: 'Fix bug\n\nbody', signed: true, createdAt: '' }] } }), { onDismiss })
    expect(screen.getByText('abcdef1')).toBeVisible()
    expect(screen.getByText('Signed')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Session details' }))
    expect(await screen.findByRole('menuitem', { name: 'Archive' })).toBeVisible()
    expect(screen.queryByRole('menuitem', { name: 'Restart' })).toBeNull()
    expect(screen.queryByRole('menuitem', { name: 'Create pull request' })).toBeNull()
    await user.click(screen.getByRole('menuitem', { name: 'Start over with Auto' }))
    expect(api.restartCodingSession).toHaveBeenLastCalledWith('cs-1', { useDefaultModel: true })
    await user.click(screen.getByRole('button', { name: 'Session details' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Archive' }))
    expect(onDismiss).toHaveBeenCalled()
  })

  it('shows why Auto routed the session and starts over with the next model up', async () => {
    const user = userEvent.setup()
    vi.mocked(api.restartCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued' }))
    renderPanel(makeSession({ state: 'complete', phase: 'idle', modelReason: 'The task has an exact, narrowly scoped implementation.', escalationModel: 'claude-opus-4-5' }))
    await user.click(screen.getByRole('button', { name: 'Session details' }))
    expect(await screen.findByText('The task has an exact, narrowly scoped implementation.')).toBeVisible()
    await user.click(await screen.findByRole('menuitem', { name: 'Start over with Claude Opus 4.5' }))
    expect(api.restartCodingSession).toHaveBeenLastCalledWith('cs-1', { model: 'claude-opus-4-5' })
  })

  it('offers no start over without a more capable model or while the agent works', async () => {
    const user = userEvent.setup()
    renderPanel(makeSession({ escalationModel: 'claude-opus-4-5' }))
    await user.click(screen.getByRole('button', { name: 'Session details' }))
    await screen.findByRole('menuitem', { name: 'Send stop request' })
    expect(screen.queryByRole('menuitem', { name: /Start over with/ })).toBeNull()
  })

  it('sends a stop request from the session menu while the agent works', async () => {
    const user = userEvent.setup()
    vi.mocked(api.stopCodingSession).mockRejectedValue(new Error('Conflict'))
    renderPanel(makeSession())
    await user.click(screen.getByRole('button', { name: 'Session details' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Send stop request' }))
    expect(api.stopCodingSession).toHaveBeenCalledWith('cs-1')
    expect(await screen.findByText('Conflict')).toBeVisible()
  })
  it('shows the session menu with the model and copies the session URL', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn(async () => undefined)
    Object.defineProperty(window.navigator, 'clipboard', { value: { writeText, readText: vi.fn() }, configurable: true })
    renderPanel(makeSession({ branchName: 'flow/x' }))
    await user.click(screen.getByRole('button', { name: 'Session details' }))
    expect(await screen.findByText('Claude Sonnet 4.5', { selector: '[role=menuitem] span' })).toBeVisible()
    expect(screen.getByText('Claude Code', { selector: '[role=menuitem] span' })).toBeVisible()
    expect(screen.queryByText('Cost')).toBeNull()
    await user.click(screen.getByRole('menuitem', { name: 'Copy' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Session URL' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(expect.stringMatching(/\/diff\/cs-1$/)))
  })

  it('shows the agent thinking with the shimmer and stops it from the composer', async () => {
    const user = userEvent.setup()
    vi.mocked(api.stopCodingSession).mockResolvedValue(makeSession({ state: 'stopping', phase: 'stopping' }))
    renderPanel(makeSession({ turnStartedAt: new Date(Date.now() - 162_000).toISOString() }))
    const panel = screen.getByRole('complementary', { name: 'Agent' })
    expect(within(panel).getByRole('status')).toHaveTextContent('Thinking…')
    await user.click(within(panel).getByRole('button', { name: 'Stop agent' }))
    expect(api.stopCodingSession).toHaveBeenCalledWith('cs-1')
  })
  it('shows "No changes yet" until the agent changes something', async () => {
    const { unmount } = renderPanel(makeSession())
    expect(await screen.findByText('No changes yet')).toBeVisible()
    unmount()
    vi.mocked(api.fetchCodingSessionDiff).mockResolvedValue({ source: 'summary', additions: 1, deletions: 0, files: [{ path: 'src/a.ts', additions: 1, deletions: 0, status: 'modified', hunks: [] }] })
    renderPanel(makeSession({ diffs: [{ path: 'src/a.ts', additions: 1, deletions: 0, status: 'modified' }] }))
    expect(await screen.findByRole('region', { name: 'Code changes' })).toBeVisible()
    expect(screen.queryByText('No changes yet')).toBeNull()
    expect(screen.getByText('Line changes appear here once the agent pushes its branch.')).toBeVisible()
  })

  it('shows the full diff: file tree with counts, line numbers, tinted rows and collapse per file', async () => {
    const user = userEvent.setup()
    vi.mocked(api.fetchCodingSessionDiff).mockResolvedValue({ source: 'codeHost', base: 'main', head: 'flow/fix', additions: 2, deletions: 1, files: [
      { path: 'src/app/a.ts', additions: 2, deletions: 1, status: 'modified', hunks: [{ header: '@@ -1,2 +1,3 @@', oldStart: 1, oldLines: 2, newStart: 1, newLines: 3, lines: [
        { kind: 'context', oldLine: 1, newLine: 1, text: 'const a = 1' }, { kind: 'delete', oldLine: 2, text: 'const b = 2' }, { kind: 'add', newLine: 2, text: 'const b = 3' }, { kind: 'add', newLine: 3, text: 'const c = 4' },
      ] }] },
      { path: 'logo.png', additions: 0, deletions: 0, status: 'added', binary: true, hunks: [] },
    ] })
    renderPanel(makeSession({ headSha: 'abc' }))
    const region = await screen.findByRole('region', { name: 'Code changes' })
    expect(within(region).getByText('2 files changed')).toBeVisible()
    const tree = within(region).getByRole('navigation', { name: 'Changed files' })
    expect(within(tree).getByRole('treeitem', { name: /src\/app/ })).toHaveAttribute('aria-expanded', 'true')
    expect(within(tree).getByRole('treeitem', { name: /a\.ts/ })).toHaveTextContent('+2−1')
    const table = within(region).getByRole('table', { name: 'Diff of src/app/a.ts' })
    const rows = within(table).getAllByRole('row')
    expect(rows[0]).toHaveTextContent('@@ -1,2 +1,3 @@')
    expect(rows[2]).toHaveAttribute('data-kind', 'delete')
    expect(within(rows[3]).getByLabelText('New line 2')).toHaveTextContent('2')
    expect(within(region).getByText('Binary file not shown.')).toBeVisible()
    const file = within(region).getByRole('article', { name: 'src/app/a.ts' })
    await user.click(within(file).getByRole('button', { name: 'src/app/a.ts' }))
    expect(within(region).queryByRole('table', { name: 'Diff of src/app/a.ts' })).toBeNull()
    await user.click(within(region).getByRole('button', { name: 'Collapse all' }))
    expect(within(region).queryByText('Binary file not shown.')).toBeNull()
    await user.click(within(region).getByRole('button', { name: 'Expand all' }))
    expect(within(region).getByRole('table', { name: 'Diff of src/app/a.ts' })).toBeVisible()
  })

  it('subscribes to and unsubscribes from the session in the Session details menu', async () => {
    const user = userEvent.setup()
    const session = makeSession({ creatorId: viewer.id })
    vi.mocked(api.setCodingSessionSubscription).mockResolvedValue({ ...session, unsubscribedIds: [viewer.id] })
    const { onChange } = renderPanel(session)
    await user.click(screen.getByRole('button', { name: 'Session details' }))
    const item = await screen.findByRole('menuitemcheckbox', { name: /Subscribe to session/ })
    expect(item).toHaveAttribute('aria-checked', 'true')
    await user.click(item)
    await waitFor(() => expect(api.setCodingSessionSubscription).toHaveBeenCalledWith('cs-1', false))
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ unsubscribedIds: [viewer.id] })))
  })

  it('attaches files to a follow-up with the paperclip and sends them with the prompt', async () => {
    const user = userEvent.setup()
    vi.mocked(uploadAgentFile).mockResolvedValue({ name: 'notes.md', contentType: 'text/markdown', size: 5, url: '/uploads/agent_upload_0123456789abcdef_1_notes.md' })
    vi.mocked(api.promptCodingSession).mockResolvedValue(makeSession())
    renderPanel(makeSession({ state: 'complete', phase: 'idle' }))
    await user.upload(screen.getByTestId('coding-session-file-input'), new File(['hello'], 'notes.md', { type: 'text/markdown' }))
    const files = await screen.findByRole('list', { name: 'Attachments' })
    await waitFor(() => expect(within(files).getByRole('listitem')).toHaveAttribute('data-state', 'ready'))
    await user.type(screen.getByRole('textbox', { name: 'Send a message to Flow' }), 'Use these notes')
    await user.click(screen.getByRole('button', { name: 'Submit message' }))
    expect(api.promptCodingSession).toHaveBeenCalledWith('cs-1', 'Use these notes', undefined, [{ name: 'notes.md', contentType: 'text/markdown', size: 5, url: '/uploads/agent_upload_0123456789abcdef_1_notes.md' }])
    await waitFor(() => expect(screen.queryByRole('list', { name: 'Attachments' })).toBeNull())
  })

  it('removes an attached file before sending', async () => {
    const user = userEvent.setup()
    vi.mocked(uploadAgentFile).mockResolvedValue({ name: 'a.md', contentType: 'text/markdown', size: 1, url: '/uploads/agent_upload_0123456789abcdef_1_a.md' })
    renderPanel(makeSession({ state: 'complete', phase: 'idle' }))
    await user.upload(screen.getByTestId('coding-session-file-input'), new File(['a'], 'a.md', { type: 'text/markdown' }))
    await user.click(await screen.findByRole('button', { name: 'Remove a.md' }))
    expect(screen.queryByRole('list', { name: 'Attachments' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Submit message' })).toBeDisabled()
  })

  it('retries an errored session from its error card', async () => {
    const user = userEvent.setup()
    vi.mocked(api.restartCodingSession).mockResolvedValue(makeSession())
    renderPanel(makeSession({ state: 'error', phase: 'done', errorCode: 'sandboxLost' }))
    await user.click(await screen.findByRole('button', { name: 'Retry' }))
    expect(api.restartCodingSession).toHaveBeenCalledWith('cs-1')
  })
  it.each(CODING_ERROR_CODES)('renders clear copy for %s', async code => {
    renderPanel(makeSession({ state: 'error', phase: 'done', errorCode: code, codingEnvironmentId: 'env-1', branchName: 'flow/x', modelSelection: 'gpt-5', sandboxes: [{ id: 'sb', kind: 'session', status: 'failed', createdAt: '' }] }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveAttribute('data-code', code)
    expect(alert.textContent?.trim()).toBeTruthy()
    expect(within(alert).queryByRole('button', { name: /Create (pull|merge) request/ })).toBeNull()
  })
  it('links to the fix for connection and signing key errors', async () => {
    const { unmount } = renderPanel(makeSession({ state: 'error', phase: 'done', errorCode: 'noGitHubConnection' }))
    expect(await screen.findByRole('link', { name: 'View admins' })).toHaveAttribute('href', '/workspace/members')
    unmount()
    const second = renderPanel(makeSession({ state: 'error', phase: 'done', errorCode: 'missingCommitSigningKey' }))
    expect(await screen.findByRole('link', { name: 'Add signing key' })).toHaveAttribute('href', '/workspace/settings/account/security/commit-signing-key')
    second.unmount()
    renderPanel(makeSession({ state: 'error', phase: 'done', errorCode: 'quotaExceeded' }))
    expect(await screen.findByText('The workspace or your coding spend limit is used up. Add credits or raise the limit to continue.')).toBeVisible()
  })
  it('shows admins the raw reason of sandbox start problems only', async () => {
    const session = makeSession({ state: 'error', phase: 'done', errorCode: 'imagePullFailed', error: 'ErrImagePull: not found' })
    const { unmount } = renderPanel(session)
    await screen.findByText('The sandbox image couldn’t be pulled.')
    expect(screen.queryByText('ErrImagePull: not found')).toBeNull()
    unmount()
    render(<I18nProvider><CodingSessionPanel session={session} data={data({ viewerRole: 'admin' })} onChange={vi.fn()}/></I18nProvider>)
    expect(await screen.findByText('ErrImagePull: not found')).toBeVisible()
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

  it('shows the plan above the composer, the diff and the summary', async () => {
    vi.mocked(api.fetchCodingSessionDiff).mockResolvedValue({ source: 'summary', additions: 6, deletions: 2, files: [{ path: 'src/a.ts', additions: 5, deletions: 2, status: 'modified', hunks: [] }, { path: 'src/b.ts', additions: 1, deletions: 0, status: 'added', hunks: [] }] })
    renderPanel(makeSession({ state: 'active', phase: 'running', summary: 'Fixed the login bug', plan: [{ content: 'Reproduce', status: 'completed' }, { content: 'Fix', status: 'inProgress' }], diffs: [{ path: 'src/a.ts', additions: 5, deletions: 2, status: 'modified' }, { path: 'src/b.ts', additions: 1, deletions: 0, status: 'added' }] }))
    expect(within(screen.getByRole('complementary', { name: 'Agent' })).getByRole('button', { name: 'Expand steps, step 2 of 2: Fix' })).toHaveTextContent('2/2')
    expect(await screen.findByText('2 files changed')).toBeVisible()
    expect(screen.getByRole('article', { name: 'src/a.ts' })).toBeVisible()
  })
  it('renders the activity stream with every activity type', async () => {
    const user = userEvent.setup()
    vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities: [
      activity('1', 'prompt', 'Fix login', { actorId: viewer.id }),
      activity('2', 'thought', 'Looking at auth.ts'),
      activity('3', 'action', '', { metadata: { action: 'Ran command', parameter: 'npm test', result: '3 passed' } }),
      activity('4', 'response', 'Done, opened a PR'),
      activity('5', 'error', 'Lint failed'),
      activity('6', 'feedback', '👍'),
    ] })
    renderPanel(makeSession({ state: 'complete', phase: 'idle' }))
    const stream = await screen.findByRole('group', { name: 'Session stream' })
    // The prompt carries its author's name (the viewer's own too).
    expect(await within(stream).findByText('Viewer')).toBeVisible()
    await user.click(within(stream).getByRole('button', { name: /Worked for/ }))
    expect(within(stream).getByText('Looking at auth.ts')).toBeVisible()
    expect(within(stream).getByText('npm test')).toBeVisible()
    expect(within(stream).getByText('Done, opened a PR')).toBeVisible()
    expect(within(stream).getByText('Lint failed')).toBeVisible()
    expect(within(stream).queryByText('👍')).toBeNull()
  })
  it('answers an awaiting-input question from the composer', async () => {
    const user = userEvent.setup()
    vi.mocked(getApplicationTask).mockResolvedValue({ session: { ...task, status: 'awaitingInput' }, activities: [activity('q', 'elicitation', 'Which repository?', { signal: 'select', signalMetadata: { options: [{ value: 'acme/app' }, { value: 'acme/api' }] } })] })
    vi.mocked(api.promptCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued' }))
    renderPanel(makeSession({ state: 'awaitingInput', phase: 'awaitingUser' }))
    expect(await screen.findByText('Which repository?')).toBeVisible()
    expect(screen.getByText('Input needed to continue')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'acme/api' }))
    await user.click(screen.getByRole('button', { name: 'Continue with selected option' }))
    expect(api.promptCodingSession).toHaveBeenCalledWith('cs-1', 'acme/api', undefined, [])
  })
  it('preselects the recommended repository so Enter confirms it', async () => {
    const user = userEvent.setup()
    vi.mocked(getApplicationTask).mockResolvedValue({ session: { ...task, status: 'awaitingInput' }, activities: [activity('q', 'elicitation', 'Which repository should I work in?', { signal: 'select', signalMetadata: { selected: 'env-api', options: [{ value: 'env-web', label: 'Web (acme/web)' }, { value: 'env-api', label: 'API (acme/api)', recommended: true }] } })] })
    vi.mocked(api.promptCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued' }))
    renderPanel(makeSession({ state: 'awaitingInput', phase: 'awaitingUser' }))
    const recommended = await screen.findByRole('button', { name: 'API (acme/api)' })
    await waitFor(() => expect(recommended).toHaveAttribute('aria-pressed', 'true'))
    await user.click(screen.getByRole('textbox', { name: 'Send a message to Flow' }))
    await user.keyboard('{Enter}')
    await waitFor(() => expect(api.promptCodingSession).toHaveBeenCalledWith('cs-1', 'env-api', undefined, []))
  })
  it('has no End session control while waiting for an answer', async () => {
    renderPanel(makeSession({ state: 'awaitingInput', phase: 'awaitingUser' }))
    await waitFor(() => expect(getApplicationTask).toHaveBeenCalled())
    expect(screen.queryByRole('button', { name: 'End session' })).toBeNull()
    expect(screen.queryByRole('group', { name: 'Needs input' })).toBeNull()
  })
  it('names who stopped a session', () => {
    renderPanel(makeSession({ state: 'stale', phase: 'done', stoppedBy: 'user-2' }))
    expect(screen.getByText('Stopped by Teammate')).toBeVisible()
  })

  it('offers to connect an account for an auth question', async () => {
    vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities: [activity('q', 'elicitation', 'Link GitHub', { signal: 'auth', signalMetadata: { url: 'https://flow.example/link' } })] })
    renderPanel(makeSession({ state: 'awaitingInput', phase: 'awaitingUser' }))
    expect(await screen.findByRole('link', { name: 'Connect' })).toHaveAttribute('href', 'https://flow.example/link')
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
    expect((await screen.findAllByText('Working…'))[0]).toBeVisible()
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

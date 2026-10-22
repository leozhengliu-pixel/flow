import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { registerLightboxOpener } from '@/components/editor/lightbox-bridge'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import * as api from '@/lib/coding-sessions-api'
import { getApplicationTask, type ApplicationActivity, type ApplicationTask } from '@/lib/application-agents'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CodingSessionConversation } from './coding-session-conversation'
import { makeSession } from './coding-session-test-fixtures'

vi.mock('@/lib/coding-sessions-api', () => ({
  promptCodingSession: vi.fn(), stopCodingSession: vi.fn(), restartCodingSession: vi.fn(), approveCodingSession: vi.fn(),
  fetchGitHubApp: vi.fn(async () => ({ configured: true, hostName: 'github.com', installations: [] })), startGitHubAppAuthorization: vi.fn(),
  fetchCodingSessionArtifacts: vi.fn(async () => []),
}))
vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), getApplicationTask: vi.fn() }))
vi.mock('@/components/agent/agent-rich-text', () => ({ AgentRichText: ({ content }: { content: string }) => <div>{content}</div> }))

const task = { id: 'task-1', issueId: 'issue-1', teamId: 'team-1', appUserId: 'app', creatorId: viewer.id, status: 'active', prompt: '', trigger: 'delegation', version: 4, updatedAt: '' } as ApplicationTask
let seq = 0
const at = (second: number) => new Date(Date.UTC(2026, 9, 19, 10, 0, second)).toISOString()
const activity = (type: string, body: string, extra: Partial<ApplicationActivity> = {}): ApplicationActivity => ({ id: `a${String(++seq).padStart(3, '0')}`, sessionId: 'task-1', actorId: type === 'prompt' ? viewer.id : 'app', type, body, createdAt: at(seq * 5), ...extra })
const stream = (...activities: ApplicationActivity[]) => vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities })

function renderConversation(session: CodingSession, data: BootstrapData = makeBootstrap({ reviews: [] }), props: Partial<Parameters<typeof CodingSessionConversation>[0]> = {}) {
  const onChange = vi.fn()
  const view = render(<I18nProvider><div className="coding-agent-panel"><CodingSessionConversation session={session} data={data} onChange={onChange} {...props}/></div></I18nProvider>)
  return { ...view, onChange }
}

beforeEach(() => {
  seq = 0
  vi.clearAllMocks()
  localStorage.clear()
  stream()
})
afterEach(() => localStorage.clear())

describe('work groups', () => {
  it('folds thoughts and actions into "Worked for …" and expands them with the triangle', async () => {
    const user = userEvent.setup()
    stream(activity('prompt', 'Fix the login'), activity('thought', 'Reading the code'), activity('action', 'Read', { metadata: { action: 'Read', parameter: '`src/login.ts`', result: 'export function login() {}' } }), activity('response', 'Fixed it.'))
    renderConversation(makeSession({ state: 'complete', phase: 'done' }))
    const header = await screen.findByRole('button', { name: /Worked for 5 seconds/ })
    expect(header).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('Reading the code')).toBeNull()
    await user.click(header)
    expect(screen.getByText('Reading the code')).toBeVisible()
    const action = screen.getByRole('button', { name: /Read/ })
    expect(screen.getByTitle('src/login.ts')).toBeVisible()
    expect(screen.queryByText('export function login() {}')).toBeNull()
    await user.click(action)
    expect(screen.getByText('export function login() {}')).toBeVisible()
    expect(screen.getByText('Fixed it.')).toBeVisible()
  })

  it('shows the live step with the shimmer and the latest thought while working', async () => {
    stream(activity('prompt', 'Go'), activity('thought', 'Checking the tests'), activity('action', 'Bash', { metadata: { action: 'Bash', parameter: '`npm test`' } }))
    const { container } = renderConversation(makeSession({ state: 'active', phase: 'running' }))
    const header = await screen.findByRole('button', { name: /Bash/ })
    expect(header.querySelector('.cs-shimmer[data-active]')).not.toBeNull()
    expect(screen.getByText('Checking the tests')).toBeVisible()
    expect(container.querySelector('.cs-shimmer-sweep')).not.toBeNull()
  })

  it('shows Thinking… before the first step and the prompt bubble', async () => {
    stream(activity('prompt', 'Add tests'))
    renderConversation(makeSession({ state: 'active', phase: 'running' }))
    expect(await screen.findByText('Add tests')).toBeVisible()
    expect(screen.getAllByText('Thinking…')[0]).toBeVisible()
  })

  it('renders a stop request as an italic message', async () => {
    stream(activity('prompt', 'Go'), activity('prompt', 'Stop requested', { signal: 'stop' }))
    renderConversation(makeSession({ state: 'stale', phase: 'done', stoppedBy: viewer.id }))
    const stop = await screen.findByText('Stop requested')
    expect(stop.tagName).toBe('EM')
  })
})

describe('screenshots', () => {
  it('shows the turn’s screenshots under its message and opens the image viewer; before/after pairs stay out', async () => {
    const user = userEvent.setup()
    const opened = vi.fn()
    registerLightboxOpener(opened)
    stream(activity('prompt', 'Take a screenshot'), activity('response', 'Done. The screenshot is attached.'))
    const shot = { id: 'shot-1', sessionId: 'cs-1', kind: 'screenshot', label: 'Hello from QA', url: 'https://example.test/shot.png', contentType: 'image/png', sizeBytes: 10, createdAt: at(30), turnId: 'a001' } as const
    const pair = { ...shot, id: 'before-1', label: 'before', group: 'Login' }
    renderConversation(makeSession({ state: 'complete', phase: 'done', artifacts: [shot, pair] }))
    const image = await screen.findByRole('img', { name: 'Hello from QA' })
    expect(screen.queryByRole('img', { name: 'before' })).toBeNull()
    expect(image.closest('.cs-turn')).toHaveTextContent('Done. The screenshot is attached.')
    await user.click(screen.getByRole('button', { name: 'View image' }))
    expect(opened).toHaveBeenCalledWith([{ src: 'https://example.test/shot.png', alt: 'Hello from QA', title: 'Hello from QA' }], 0)
    registerLightboxOpener(null)
  })
})

describe('turn header', () => {
  it('names the prompt’s author with its time and puts the routing note after the first prompt', async () => {
    stream(activity('prompt', 'Work on ENG-1'), activity('response', 'Done.'))
    renderConversation(makeSession({ state: 'complete', phase: 'done', modelSelection: 'auto', model: 'claude-sonnet-4-5' }))
    await screen.findByText('Done.')
    const prompt = screen.getByText('Work on ENG-1').closest('.coding-session-prompt')!
    expect(within(prompt as HTMLElement).getByText('Viewer')).toBeVisible()
    expect(prompt.querySelector('time')).toHaveAttribute('title')
    const routed = screen.getByText(/routed this session to/).closest('p')!
    expect(prompt.compareDocumentPosition(routed) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })
})

describe('plan, PR card and commits', () => {
  it('shows the plan pill with the current step and expands into the list', async () => {
    const user = userEvent.setup()
    renderConversation(makeSession({ plan: [{ content: 'Read code', status: 'completed' }, { content: 'Write fix', status: 'inProgress' }, { content: 'Drop old API', status: 'canceled' }] }))
    const pill = screen.getByRole('button', { name: 'Expand steps, step 2 of 3: Write fix' })
    expect(pill).toHaveTextContent('2/3')
    await user.click(pill)
    const list = screen.getByRole('list', { name: 'Plan' })
    expect(within(list).getAllByRole('listitem').map(item => item.getAttribute('data-status'))).toEqual(['completed', 'inProgress', 'canceled'])
    expect(screen.getByText('Write fix')).toHaveClass('cs-plan-shimmer')
    expect(screen.getByRole('button', { name: 'Collapse steps' })).toHaveTextContent(/Plan.*Updated/)
  })

  it('hides the plan without steps and folds it when the session completes', async () => {
    const user = userEvent.setup()
    const plan = [{ content: 'Write fix', status: 'inProgress' as const }]
    const view = renderConversation(makeSession({ plan }))
    await user.click(screen.getByRole('button', { name: /Expand steps/ }))
    view.rerender(<I18nProvider><CodingSessionConversation session={makeSession({ plan, state: 'complete', phase: 'done' })} data={makeBootstrap({ reviews: [] })} onChange={vi.fn()}/></I18nProvider>)
    expect(screen.queryByRole('list', { name: 'Plan' })).toBeNull()
    view.rerender(<I18nProvider><CodingSessionConversation session={makeSession({ plan: [] })} data={makeBootstrap({ reviews: [] })} onChange={vi.fn()}/></I18nProvider>)
    expect(screen.queryByRole('button', { name: /Expand steps/ })).toBeNull()
  })

  it('ends the turn that pushed with the diff chip (files, SHA, +/−) instead of a PR card', async () => {
    const prompt = activity('prompt', 'Ship it')
    stream(prompt, activity('response', 'Opened a PR.'))
    const onOpenChanges = vi.fn()
    render(<I18nProvider><CodingSessionConversation onOpenChanges={onOpenChanges} data={makeBootstrap({ reviews: [] })} onChange={vi.fn()} session={makeSession({
      state: 'complete', phase: 'done', repository: 'acme/app', codeHost: 'github',
      diffs: [{ path: 'a.ts', additions: 10, deletions: 2, status: 'modified' }, { path: 'b.ts', additions: 1, deletions: 0, status: 'added' }],
      latestPullRequest: { sessionId: 'cs-1', codeHost: 'github', hostName: 'github.com', repository: 'acme/app', number: 7, url: 'https://github.com/acme/app/pull/7', title: 'Fix login', state: 'open', draft: false, headBranch: 'flow/fix-login', baseBranch: 'main', createdAt: '', updatedAt: '' },
      commitsByTurnActivityId: { [prompt.id]: [{ sha: 'abcdef1234567', branch: 'flow/fix-login', signed: false, createdAt: '', message: 'Fix login' }] },
    })}/></I18nProvider>)
    await screen.findByText('Opened a PR.')
    expect(screen.queryByRole('article', { name: 'Pull request' })).toBeNull()
    const chip = screen.getByRole('button', { name: '2 files abcdef1 +11 -2' })
    expect(chip).toHaveTextContent('2 filesabcdef1+11-2')
    await userEvent.setup().click(chip)
    expect(onOpenChanges).toHaveBeenCalled()
  })

  it('puts the PR card after the last response when no commit is recorded', async () => {
    stream(activity('prompt', 'Ship it'), activity('response', 'Opened a PR.'))
    renderConversation(makeSession({
      state: 'complete', phase: 'done', repository: 'acme/app', codeHost: 'github',
      diffs: [{ path: 'a.ts', additions: 10, deletions: 2, status: 'modified' }, { path: 'b.ts', additions: 1, deletions: 0, status: 'added' }],
      latestPullRequest: { sessionId: 'cs-1', codeHost: 'github', hostName: 'github.com', repository: 'acme/app', number: 7, url: 'https://github.com/acme/app/pull/7', title: 'Fix login', state: 'open', draft: false, headBranch: 'flow/fix-login', baseBranch: 'main', createdAt: '', updatedAt: '' },
    }))
    await screen.findByText('Opened a PR.')
    const card = screen.getByRole('article', { name: 'Pull request' })
    expect(card).toHaveAttribute('data-status', 'open')
    expect(card).toHaveTextContent('Changed 2 files+11-2')
    expect(within(card).getByText('Open')).toBeVisible()
    expect(within(card).getByRole('link', { name: 'Open pull request Fix login' })).toHaveAttribute('href', 'https://github.com/acme/app/pull/7')
    expect(within(card).getByRole('button', { name: 'main' })).toBeVisible()
    expect(within(card).getByRole('button', { name: 'flow/fix-login' })).toBeVisible()
    expect(within(card).queryByText(/Checks/)).toBeNull()
    expect(screen.queryByRole('button', { name: /Create pull request/ })).toBeNull()
  })

  it('shows draft, review and merged states', async () => {
    const pr = { sessionId: 'cs-1', codeHost: 'github' as const, hostName: 'github.com', repository: 'acme/app', number: 7, url: 'https://github.com/acme/app/pull/7', title: 'T', headBranch: 'h', baseBranch: 'main', createdAt: '', updatedAt: '' }
    const review = { id: 'r1', slugId: 'r1', provider: 'github', number: 7, repositoryOwner: 'acme', repositoryName: 'app', status: 'approved', draft: false, checks: [], issueIds: [] }
    stream(activity('response', 'Done'))
    const view = renderConversation(makeSession({ state: 'complete', phase: 'done', latestPullRequest: { ...pr, state: 'open', draft: true } }))
    await screen.findByText('Done')
    expect(screen.getByText('Draft')).toBeVisible()
    view.unmount()
    renderConversation(makeSession({ state: 'complete', phase: 'done', latestPullRequest: { ...pr, state: 'open', draft: false } }), makeBootstrap({ reviews: [review] as never }))
    await screen.findByText('Done')
    expect(screen.getByText('Approved')).toBeVisible()
    expect(screen.getByRole('link', { name: 'Open pull request T' })).toHaveAttribute('href', '/workspace/review/r1')
  })

  it('offers the uncommitted changes pill when the diff is not in a commit yet', async () => {
    const user = userEvent.setup()
    const onOpenChanges = vi.fn()
    stream(activity('response', 'Changed files'))
    renderConversation(makeSession({ state: 'complete', phase: 'done', diffs: [{ path: 'a.ts', additions: 3, deletions: 1, status: 'modified' }] }), undefined, { onOpenChanges })
    await user.click(await screen.findByRole('button', { name: /Uncommitted changes/ }))
    expect(onOpenChanges).toHaveBeenCalled()
  })

  it('copies the last message and shows the session links', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn(async () => undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    stream(activity('response', 'All done'))
    renderConversation(makeSession({ state: 'complete', phase: 'done', externalUrls: [{ label: 'Preview', url: 'https://preview.example' }] }))
    expect(await screen.findByRole('link', { name: 'Preview' })).toHaveAttribute('href', 'https://preview.example')
    await user.click(screen.getByRole('button', { name: 'Copy message' }))
    expect(writeText).toHaveBeenCalledWith('All done')
  })
})

describe('errors', () => {
  it('offers Retry on the last error of a failed session only', async () => {
    const user = userEvent.setup()
    vi.mocked(api.restartCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued', version: 2 }))
    stream(activity('prompt', 'Go'), activity('error', 'Selected model is at capacity. (modelUnavailable)', { metadata: { errorCode: 'modelUnavailable' } }), activity('prompt', 'Again'), activity('error', 'Codex exited with code 1 (harnessFailed)', { metadata: { errorCode: 'harnessFailed' } }))
    const { onChange } = renderConversation(makeSession({ state: 'error', phase: 'done', errorCode: 'harnessFailed' }))
    await screen.findByText('Selected model is at capacity.')
    const cards = screen.getAllByRole('alert')
    expect(cards).toHaveLength(2)
    expect(within(cards[0]).queryByRole('button', { name: 'Retry' })).toBeNull()
    await user.click(within(cards[1]).getByRole('button', { name: 'Retry' }))
    expect(api.restartCodingSession).toHaveBeenCalledWith('cs-1')
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ state: 'pending' })))
  })

  it('shows the signing key pill', async () => {
    stream(activity('error', 'No key (missingCommitSigningKey)', { metadata: { errorCode: 'missingCommitSigningKey' } }))
    renderConversation(makeSession({ state: 'error', phase: 'done', errorCode: 'missingCommitSigningKey' }))
    expect(await screen.findByText('Signing key needed to proceed')).toBeVisible()
    expect(screen.getByRole('link', { name: 'Add signing key' })).toHaveAttribute('href', '/workspace/settings/account/security/commit-signing-key')
  })

  it('asks admins to enable code access and members to ask an admin', async () => {
    stream(activity('error', 'x (noCodeAccess)', { metadata: { errorCode: 'noCodeAccess' } }))
    const view = renderConversation(makeSession({ state: 'error', phase: 'done', errorCode: 'noCodeAccess' }), makeBootstrap({ reviews: [], viewerRole: 'admin' }))
    expect(await screen.findByText('GitHub code access needed')).toBeVisible()
    expect(screen.getByRole('link', { name: 'Enable' })).toHaveAttribute('href', '/workspace/settings/integrations/github')
    view.unmount()
    renderConversation(makeSession({ state: 'error', phase: 'done', errorCode: 'noCodeAccess' }), makeBootstrap({ reviews: [], members: [{ user: teammate, role: 'admin', status: 'active', joinedAt: '' }] as never }))
    expect(await screen.findByText('Request Flow code access from Teammate')).toBeVisible()
    expect(screen.getByRole('link', { name: 'View admins' })).toHaveAttribute('href', '/workspace/members')
  })

  it('asks the creator to connect GitHub, then resumes once connected; others wait', async () => {
    const user = userEvent.setup()
    vi.mocked(api.startGitHubAppAuthorization).mockResolvedValue({ url: 'https://github.com/login/oauth' })
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    stream(activity('error', 'x (requiresGithubConnection)', { metadata: { errorCode: 'requiresGithubConnection' } }))
    const view = renderConversation(makeSession({ state: 'error', phase: 'done', errorCode: 'requiresGithubConnection', creatorId: viewer.id }))
    await user.click(await screen.findByRole('button', { name: 'Connect' }))
    expect(open).toHaveBeenCalledWith('https://github.com/login/oauth', '_blank', 'noopener')
    view.unmount()
    vi.mocked(api.fetchGitHubApp).mockResolvedValue({ configured: true, hostName: 'github.com', installations: [], linkedAccount: { login: 'v', linkedAt: '' } })
    vi.mocked(api.restartCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued' }))
    const linked = renderConversation(makeSession({ state: 'error', phase: 'done', errorCode: 'requiresGithubConnection', creatorId: viewer.id }))
    expect(await screen.findByText('Code access confirmed')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Resume' }))
    expect(api.restartCodingSession).toHaveBeenCalled()
    linked.unmount()
    renderConversation(makeSession({ state: 'error', phase: 'done', errorCode: 'requiresGithubConnection', creatorId: teammate.id }))
    expect(await screen.findByText('Awaiting confirmation of code access from Teammate')).toBeVisible()
    open.mockRestore()
  })

  it('explains the session error even when its messages cannot be loaded, and retries the messages', async () => {
    const user = userEvent.setup()
    vi.mocked(getApplicationTask).mockRejectedValue(new Error('resource not found'))
    // A standalone chat session that failed before it ran: the creator must link their own GitLab account.
    const view = renderConversation(makeSession({ state: 'error', phase: 'done', issueId: undefined, codeHost: 'gitlab', errorCode: 'requiresGitlabConnection', error: 'This workspace requires your linked GitLab account for coding sessions. Link GitLab in your account settings.', creatorId: viewer.id }))
    expect(await screen.findByText(/This workspace requires coding sessions to run with your own GitLab access/)).toBeVisible()
    expect(screen.getByRole('link', { name: 'Link GitLab account' })).toHaveAttribute('href', '/workspace/settings/account/connections')
    expect(screen.getByText('Couldn’t load this session’s messages.')).toBeVisible()
    expect(screen.queryByText('resource not found')).toBeNull()
    const calls = vi.mocked(getApplicationTask).mock.calls.length
    stream()
    await user.click(within(screen.getByText('Couldn’t load this session’s messages.').closest('p')!).getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(vi.mocked(getApplicationTask).mock.calls.length).toBeGreaterThan(calls))
    await waitFor(() => expect(screen.queryByText('Couldn’t load this session’s messages.')).toBeNull())
    expect(screen.getByRole('link', { name: 'Link GitLab account' })).toBeVisible()
    view.unmount()

    // Another reader (an admin) waits for the creator; any other code shows the session's own reason.
    vi.mocked(getApplicationTask).mockRejectedValue(new Error('resource not found'))
    const other = renderConversation(makeSession({ state: 'error', phase: 'done', codeHost: 'gitlab', errorCode: 'requiresGitlabConnection', creatorId: teammate.id }))
    expect(await screen.findByText('Awaiting confirmation of code access from Teammate')).toBeVisible()
    other.unmount()
    renderConversation(makeSession({ state: 'error', phase: 'done', errorCode: 'harnessFailed', error: 'Codex exited with code 1' }))
    expect(await screen.findByText('Codex exited with code 1')).toBeVisible()
  })

  it('shows the failed-to-start card for a session the agent never answered, and a fallback without an error activity', async () => {
    stream(activity('prompt', 'Go'))
    const view = renderConversation(makeSession({ state: 'stale', phase: 'done' }))
    expect(await screen.findByText('Flow failed to start.')).toBeVisible()
    view.unmount()
    renderConversation(makeSession({ state: 'error', phase: 'done', errorCode: 'internal', error: '' }))
    expect(await screen.findByText('Something went wrong while running this session.')).toBeVisible()
  })
})

describe('composer', () => {
  it('stops an agent from the empty composer and disables Stop while a stop is pending', async () => {
    const user = userEvent.setup()
    vi.mocked(api.stopCodingSession).mockResolvedValue(makeSession({ state: 'stopping', phase: 'stopping' }))
    const view = renderConversation(makeSession({ state: 'active', phase: 'running' }))
    await user.click(screen.getByRole('button', { name: 'Stop agent' }))
    expect(api.stopCodingSession).toHaveBeenCalledWith('cs-1')
    view.unmount()
    renderConversation(makeSession({ state: 'active', phase: 'running', stopRequested: true }))
    expect(screen.getByRole('button', { name: 'Stop agent' })).toBeDisabled()
  })

  it('queues a message while the agent works and sends it once it is idle', async () => {
    const user = userEvent.setup()
    vi.mocked(api.promptCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued', version: 3 }))
    const data = makeBootstrap({ reviews: [] })
    const view = render(<I18nProvider><CodingSessionConversation session={makeSession({ state: 'active', phase: 'running' })} data={data} onChange={vi.fn()}/></I18nProvider>)
    await user.type(screen.getByRole('textbox', { name: 'Send a message to Flow' }), 'Also update the docs')
    const queue = screen.getByRole('button', { name: 'Queue message' })
    expect(queue).toHaveTextContent('Queue')
    await user.click(queue)
    expect(within(screen.getByRole('list', { name: 'Queued messages' })).getByText('Also update the docs')).toBeVisible()
    expect(api.promptCodingSession).not.toHaveBeenCalled()
    view.rerender(<I18nProvider><CodingSessionConversation session={makeSession({ state: 'complete', phase: 'idle', version: 2 })} data={data} onChange={vi.fn()}/></I18nProvider>)
    await waitFor(() => expect(api.promptCodingSession).toHaveBeenCalledWith('cs-1', 'Also update the docs', undefined, []))
    await waitFor(() => expect(screen.queryByRole('list', { name: 'Queued messages' })).toBeNull())
  })

  it('sends the scope chosen in the composer with the follow-up, then returns to Auto', async () => {
    const user = userEvent.setup()
    vi.mocked(api.promptCodingSession).mockResolvedValue(makeSession({ state: 'complete', phase: 'idle' }))
    renderConversation(makeSession({ state: 'complete', phase: 'idle' }))
    const box = screen.getByRole('textbox', { name: 'Send a message to Flow' })
    expect(screen.getByRole('button', { name: 'Scope: Auto' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'Scope: Auto' }))
    await user.click(await screen.findByRole('menuitem', { name: /Verify only/ }))
    expect(screen.getByRole('button', { name: 'Scope: Verify only' })).toHaveClass('is-explicit')
    await user.type(box, 'Check that the build passes{Enter}')
    await waitFor(() => expect(api.promptCodingSession).toHaveBeenCalledWith('cs-1', 'Check that the build passes', undefined, [], 'readOnly'))
    expect(screen.getByRole('button', { name: 'Scope: Auto' })).toBeInTheDocument()
    await user.type(box, 'Now fix it{Enter}')
    await waitFor(() => expect(api.promptCodingSession).toHaveBeenLastCalledWith('cs-1', 'Now fix it', undefined, []))
  })

  it('a queued message keeps the scope it was queued with', async () => {
    const user = userEvent.setup()
    vi.mocked(api.promptCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued', version: 3 }))
    const data = makeBootstrap({ reviews: [] })
    const view = render(<I18nProvider><CodingSessionConversation session={makeSession({ state: 'active', phase: 'running' })} data={data} onChange={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Scope: Auto' }))
    await user.click(await screen.findByRole('menuitem', { name: /Don’t publish/ }))
    await user.type(screen.getByRole('textbox', { name: 'Send a message to Flow' }), 'Try the refactor{Enter}')
    view.rerender(<I18nProvider><CodingSessionConversation session={makeSession({ state: 'complete', phase: 'idle', version: 2 })} data={data} onChange={vi.fn()}/></I18nProvider>)
    await waitFor(() => expect(api.promptCodingSession).toHaveBeenCalledWith('cs-1', 'Try the refactor', undefined, [], 'noPublish'))
  })

  it('removes a queued message, or sends it right away', async () => {
    const user = userEvent.setup()
    vi.mocked(api.promptCodingSession).mockResolvedValue(makeSession({ state: 'active', phase: 'running' }))
    renderConversation(makeSession({ state: 'active', phase: 'running' }))
    const box = screen.getByRole('textbox', { name: 'Send a message to Flow' })
    await user.type(box, 'one{Enter}')
    await user.type(box, 'two{Enter}')
    const list = screen.getByRole('list', { name: 'Queued messages' })
    await user.click(within(list).getAllByRole('button', { name: 'Remove from queue' })[0])
    expect(within(list).queryByText('one')).toBeNull()
    await user.click(within(list).getByRole('button', { name: 'Send' }))
    expect(api.promptCodingSession).toHaveBeenCalledWith('cs-1', 'two', undefined, [])
  })

  it('sends immediately with ⌘⇧Enter while the agent works', async () => {
    const user = userEvent.setup()
    vi.mocked(api.promptCodingSession).mockResolvedValue(makeSession())
    renderConversation(makeSession({ state: 'active', phase: 'running' }))
    await user.type(screen.getByRole('textbox', { name: 'Send a message to Flow' }), 'Steer now')
    await user.keyboard('{Meta>}{Shift>}{Enter}{/Shift}{/Meta}')
    expect(api.promptCodingSession).toHaveBeenCalledWith('cs-1', 'Steer now', undefined, [])
    expect(screen.queryByRole('list', { name: 'Queued messages' })).toBeNull()
  })

  it('renders a choice question as text with bulleted options, without answer buttons or a frame', async () => {
    stream(activity('elicitation', 'Should shout() append an exclamation mark?', { signal: 'select', signalMetadata: { options: [{ value: 'Yes' }, { value: 'No', label: 'No thanks' }] } }))
    renderConversation(makeSession({ state: 'awaitingInput', phase: 'awaitingUser' }))
    // The rich-text body is the markdown source in jsdom: the question, then one bullet per option.
    expect(await screen.findByText(/^Should shout\(\) append an exclamation mark\?\s+- Yes\s+- No thanks$/)).toBeVisible()
    expect(screen.queryByText('Input needed to continue')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Yes' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Select an option' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Submit message' })).toBeDisabled()
  })

  it('asks the viewer to connect an account for an auth question', async () => {
    stream(activity('elicitation', 'Link GitHub', { signal: 'auth', signalMetadata: { url: 'https://flow.example/link', providerName: 'GitHub' } }))
    renderConversation(makeSession({ state: 'awaitingInput', phase: 'awaitingUser' }))
    expect(await screen.findByText('Connect your GitHub account to continue')).toBeVisible()
    expect(screen.getByRole('link', { name: 'Connect' })).toHaveAttribute('href', 'https://flow.example/link')
    expect(screen.getByText('Flow requested Viewer to connect their GitHub account.', { exact: false })).toBeVisible()
  })

  it('approves a session from an external source in the composer', async () => {
    const user = userEvent.setup()
    vi.mocked(api.approveCodingSession).mockResolvedValue(makeSession({ state: 'pending', phase: 'queued' }))
    renderConversation(makeSession({ state: 'awaitingInput', phase: 'awaitingApproval' }))
    expect(screen.getByText('This session comes from an external source. A member must approve it before the agent starts.')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Reject' }))
    await user.click(screen.getByRole('button', { name: 'Continue with selected option' }))
    expect(api.approveCodingSession).toHaveBeenCalledWith('cs-1', true)
    await act(async () => undefined)
  })
})

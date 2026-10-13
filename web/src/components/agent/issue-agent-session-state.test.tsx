import { render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import { getApplicationTask, listApplicationTasks, type ApplicationTask } from '@/lib/application-agents'
import { IssueAgentTasks, IssueAgentTasksProvider } from './issue-agent-tasks'
import { useStandaloneAgentSessions } from './issue-agent-task-context'

vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), listApplicationTasks: vi.fn(), getApplicationTask: vi.fn() }))
vi.mock('./agent-rich-text', () => ({ AgentRichText: ({ content }: { content: string }) => <div>{content}</div> }))

const app = { ...viewer, id: 'app-one', displayName: 'Flow', app: true, appScopes: ['app:assignable', 'app:mentionable'], appTeamIds: ['team-1'] }
const base: ApplicationTask = { id: 'session', issueId: 'issue-1', teamId: 'team-1', appUserId: app.id, creatorId: viewer.id, status: 'complete', version: 3, prompt: 'Inspect', trigger: 'mention', commentId: 'comment-one', createdAt: '2026-10-12T10:00:00Z', updatedAt: '2026-10-12T10:00:00Z' }

function renderThread(task: ApplicationTask, activities = [{ id: 'r', sessionId: task.id, actorId: app.id, type: 'response', body: 'Done', createdAt: '' }]) {
  vi.mocked(listApplicationTasks).mockResolvedValue([task])
  vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities })
  const issue = makeIssue({ delegate: app }), data = makeBootstrap({ users: [viewer, app] })
  return render(<I18nProvider><IssueAgentTasksProvider issue={issue} data={data}><IssueAgentTasks issue={issue} data={data} commentId={task.commentId}/></IssueAgentTasksProvider></I18nProvider>)
}

beforeEach(() => vi.clearAllMocks())

describe('agent session state in comment threads', () => {
  it('shows the external links as chips and the plan as a checklist above the replies', async () => {
    renderThread({ ...base, externalUrls: [{ url: 'https://github.com/acme/app/pull/7', label: 'Pull request' }, { url: 'https://ci.example.test/run/1' }, { url: 'javascript:alert(1)' }], plan: [{ content: 'Read the issue', status: 'completed' }, { content: 'Draft the plan', status: 'inProgress' }, { content: 'Open a PR', status: 'pending' }, { content: 'Old idea', status: 'canceled' }] })
    const links = await screen.findByLabelText('Session links')
    expect(within(links).getAllByRole('link').map(link => [link.textContent, link.getAttribute('href')])).toEqual([['Pull request', 'https://github.com/acme/app/pull/7'], ['ci.example.test', 'https://ci.example.test/run/1']])
    const plan = screen.getByRole('list', { name: 'Plan' })
    expect(within(plan).getAllByRole('listitem').map(item => [item.textContent, item.getAttribute('data-status')])).toEqual([['Read the issue', 'completed'], ['Draft the plan', 'inProgress'], ['Open a PR', 'pending'], ['Old idea', 'canceled']])
    expect(plan.compareDocumentPosition(await screen.findByText('Done')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })
  it('says the agent did not respond instead of working forever', async () => {
    renderThread({ ...base, status: 'pending', unresponsive: true }, [])
    expect(await screen.findByText('Flow didn’t respond')).toBeVisible()
    expect(screen.queryByText('Working…')).not.toBeInTheDocument()
  })
  it('marks a stale session', async () => {
    renderThread({ ...base, status: 'stale' }, [])
    const notice = await screen.findByText('No activity from Flow for 30 minutes')
    expect(notice.closest('[data-state]')).toHaveAttribute('data-state', 'stale')
    expect(screen.queryByText('Working…')).not.toBeInTheDocument()
  })
  it('names the person who delegated the issue', async () => {
    vi.mocked(listApplicationTasks).mockResolvedValue([{ ...base, trigger: 'delegation', commentId: undefined }])
    vi.mocked(getApplicationTask).mockResolvedValue({ session: { ...base, trigger: 'delegation', commentId: undefined }, activities: [] })
    render(<I18nProvider><IssueAgentTasks issue={makeIssue({ delegate: app })} data={makeBootstrap({ users: [viewer, app] })} standalone/></I18nProvider>)
    expect(await screen.findByText(`Delegated to Flow by ${viewer.displayName}`)).toBeVisible()
  })
  it('lists sessions oldest first and exposes standalone ones for the timeline', async () => {
    const older = { ...base, id: 'older', trigger: 'delegation', commentId: undefined, createdAt: '2026-10-12T09:00:00Z' }
    const newer = { ...base, id: 'newer', trigger: 'delegation', commentId: undefined, createdAt: '2026-10-12T11:00:00Z' }
    vi.mocked(listApplicationTasks).mockResolvedValue([newer, older])
    vi.mocked(getApplicationTask).mockImplementation(async (_workspace, id) => ({ session: id === 'older' ? older : newer, activities: [] }))
    let seen: readonly { id: string }[] = []
    function Probe() { seen = useStandaloneAgentSessions(); return null }
    const issue = makeIssue({ delegate: app }), data = makeBootstrap({ users: [viewer, app] })
    const { container } = render(<I18nProvider><IssueAgentTasksProvider issue={issue} data={data}><Probe/><IssueAgentTasks issue={issue} data={data} standalone/></IssueAgentTasksProvider></I18nProvider>)
    await screen.findAllByText(`Delegated to Flow by ${viewer.displayName}`)
    expect([...container.querySelectorAll('[data-agent-task-id]')].map(node => node.getAttribute('data-agent-task-id'))).toEqual(['older', 'newer'])
    expect(seen.map(item => item.id)).toEqual(['older', 'newer'])
  })
  it('renders one session by id', async () => {
    vi.mocked(listApplicationTasks).mockResolvedValue([{ ...base, id: 'a', commentId: undefined }, { ...base, id: 'b', commentId: undefined }])
    vi.mocked(getApplicationTask).mockResolvedValue({ session: base, activities: [] })
    const { container } = render(<I18nProvider><IssueAgentTasks issue={makeIssue({ delegate: app })} data={makeBootstrap({ users: [viewer, app] })} taskId="b"/></I18nProvider>)
    await screen.findByLabelText('Agent sessions')
    expect([...container.querySelectorAll('[data-agent-task-id]')].map(node => node.getAttribute('data-agent-task-id'))).toEqual(['b'])
  })
  it('never shows the ```suggestions block as a code block in a thread', async () => {
    renderThread(base, [{ id: 'r', sessionId: base.id, actorId: app.id, type: 'response', body: 'Two fields changed.\n\n```suggestions\nShow the history\nCompare with DEV-2\n```', createdAt: '' }])
    expect(await screen.findByText('Two fields changed.')).toBeVisible()
    expect(screen.queryByText(/Show the history/)).not.toBeInTheDocument()
    expect(screen.queryByText(/suggestions/)).not.toBeInTheDocument()
  })
})

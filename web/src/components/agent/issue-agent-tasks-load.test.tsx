import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { LOCALE_STORAGE_KEY } from '@/i18n/locale'
import { ApiError } from '@/lib/api-client'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import { getApplicationTask, listApplicationTasks, type ApplicationTask } from '@/lib/application-agents'
import { IssueAgentTasks, IssueAgentTasksProvider } from './issue-agent-tasks'

vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), listApplicationTasks: vi.fn(), getApplicationTask: vi.fn() }))
vi.mock('./agent-rich-text', () => ({ AgentRichText: ({ content }: { content: string }) => <div>{content}</div> }))

const app = { ...viewer, id: 'app-one', displayName: 'Flow', app: true, appScopes: ['app:assignable', 'app:mentionable'], appTeamIds: ['team-1'] }
const task: ApplicationTask = { id: 'session', issueId: 'issue-1', teamId: 'team-1', appUserId: app.id, creatorId: viewer.id, status: 'complete', version: 3, prompt: 'Inspect', trigger: 'mention', commentId: 'comment-one', createdAt: '2026-10-12T10:00:00Z', updatedAt: '2026-10-12T10:00:00Z' }
const reply = { session: task, activities: [{ id: 'r', sessionId: task.id, actorId: app.id, type: 'response', body: 'The saved reply', createdAt: '' }] }
const busy = () => new ApiError('The workspace is busy loading for other people; retry in a moment', 503, 'UNAVAILABLE')

function renderThread() {
  vi.mocked(listApplicationTasks).mockResolvedValue([task])
  const issue = makeIssue({ delegate: app }), data = makeBootstrap({ users: [viewer, app] })
  return render(<I18nProvider><IssueAgentTasksProvider issue={issue} data={data}><IssueAgentTasks issue={issue} data={data} commentId={task.commentId}/></IssueAgentTasksProvider></I18nProvider>)
}

beforeEach(() => vi.clearAllMocks())

describe('reading an agent session that the server could not load for now', () => {
  it('says the replies could not be loaded, not that the agent failed, and Retry reads the saved reply', async () => {
    vi.mocked(getApplicationTask).mockRejectedValueOnce(busy()).mockResolvedValue(reply)
    renderThread()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the agent's replies right now. Try again in a moment.")
    expect(alert).toHaveAttribute('data-state', 'unavailable')
    expect(screen.queryByText('The agent ran into an error.')).not.toBeInTheDocument()
    expect(within(alert).queryByText('Details')).not.toBeInTheDocument()

    await userEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('The saved reply')).toBeVisible()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(getApplicationTask).toHaveBeenCalledTimes(2)
    expect(vi.mocked(getApplicationTask).mock.calls.map(call => call[1])).toEqual([task.id, task.id])
  })

  it('treats a lost connection the same way', async () => {
    vi.mocked(getApplicationTask).mockRejectedValue(new TypeError('Failed to fetch'))
    renderThread()
    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the agent's replies right now.")
    expect(screen.queryByText('The agent ran into an error.')).not.toBeInTheDocument()
  })

  it('keeps other read failures as they were', async () => {
    vi.mocked(getApplicationTask).mockRejectedValue(new ApiError('Something else broke', 500))
    renderThread()
    const alert = await screen.findByRole('alert')
    expect(alert).not.toHaveAttribute('data-state')
    expect(alert).toHaveTextContent('Something else broke')
  })

  it('shows the notice in Chinese', async () => {
    localStorage.setItem(LOCALE_STORAGE_KEY, 'zh-CN')
    try {
      vi.mocked(getApplicationTask).mockRejectedValue(busy())
      renderThread()
      expect(await screen.findByText('暂时无法加载 Agent 的回复，请稍后重试。')).toBeVisible()
      expect(screen.getByRole('button', { name: '重试' })).toBeVisible()
    } finally { localStorage.removeItem(LOCALE_STORAGE_KEY) }
  })
})

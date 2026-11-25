import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { LOCALE_STORAGE_KEY } from '@/i18n/locale'
import { ApiError } from '@/lib/api-client'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import { getApplicationTask, listApplicationTasks, replyApplicationTask, type ApplicationTask } from '@/lib/application-agents'
import { IssueAgentTasks, IssueAgentTasksProvider } from './issue-agent-tasks'

vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), listApplicationTasks: vi.fn(), getApplicationTask: vi.fn(), replyApplicationTask: vi.fn() }))
vi.mock('./agent-rich-text', () => ({ AgentRichText: ({ content }: { content: string }) => <div>{content}</div> }))

const app = { ...viewer, id: 'app-one', displayName: 'Flow', app: true, appScopes: ['app:assignable', 'app:mentionable'], appTeamIds: ['team-1'] }
const task: ApplicationTask = { id: 'session', issueId: 'issue-1', teamId: 'team-1', appUserId: app.id, creatorId: viewer.id, status: 'complete', version: 3, prompt: 'Inspect', trigger: 'mention', commentId: 'comment-one', createdAt: '2026-10-12T10:00:00Z', updatedAt: '2026-10-12T10:00:00Z' }
const failed = { session: { ...task, status: 'error' as const }, activities: [{ id: 'e', sessionId: task.id, actorId: app.id, type: 'error', body: 'Flow Agent provider stopped responding', createdAt: '' }] }
const gatewayTimeout = () => new ApiError('Request failed: 504', 504)

function renderThread() {
  const issue = makeIssue({ delegate: app }), data = makeBootstrap({ users: [viewer, app] })
  return render(<I18nProvider><IssueAgentTasksProvider issue={issue} data={data}><IssueAgentTasks issue={issue} data={data} commentId={task.commentId}/></IssueAgentTasksProvider></I18nProvider>)
}

async function sendFollowUp(text: string) {
  const user = userEvent.setup()
  await screen.findByText('The saved reply')
  await user.click(document.querySelector<HTMLButtonElement>('.issue-agent-start-reply')!)
  await user.type(screen.getByRole('textbox'), text)
  await user.click(document.querySelector<HTMLButtonElement>('.issue-agent-reply-tools button[type="submit"]')!)
  return user
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(listApplicationTasks).mockResolvedValue([task])
  vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities: [{ id: 'r', sessionId: task.id, actorId: app.id, type: 'response', body: 'The saved reply', createdAt: '' }] })
})

describe('a follow-up that could not be sent', () => {
  it('says the follow-up was not sent, keeps the text, and Resend sends it again', async () => {
    vi.mocked(replyApplicationTask).mockRejectedValueOnce(gatewayTimeout()).mockResolvedValue({ ...task, status: 'pending', version: 4 })
    renderThread()
    const user = await sendFollowUp('One more question')

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveAttribute('data-state', 'send-failed')
    expect(alert).toHaveTextContent("Couldn't send your follow-up.")
    expect(screen.queryByText('The agent ran into an error.')).not.toBeInTheDocument()
    expect(screen.queryByText(/took too long to respond/)).not.toBeInTheDocument()
    // The server's own text stays available under Details.
    expect(within(alert).getByText('Details')).toBeInTheDocument()
    expect(within(alert).getByText('Request failed: 504')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Reply to agent' })).toHaveValue('One more question')
    expect(within(alert).queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()

    // Resend sends the follow-up again; it does not only reread the saved activities.
    await user.click(within(alert).getByRole('button', { name: 'Resend' }))
    await waitFor(() => expect(replyApplicationTask).toHaveBeenCalledTimes(2))
    expect(vi.mocked(replyApplicationTask).mock.calls.map(call => call.slice(2))).toEqual([['prompt', 'One more question', undefined], ['prompt', 'One more question', undefined]])
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(screen.queryByRole('textbox', { name: 'Reply to agent' })).not.toBeInTheDocument()
  })

  it('treats a busy server as a send failure, not as a read notice or an agent error', async () => {
    vi.mocked(replyApplicationTask).mockRejectedValue(new ApiError('The workspace is busy loading for other people; retry in a moment', 503, 'UNAVAILABLE'))
    renderThread()
    await sendFollowUp('Still there?')
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't send your follow-up.")
    expect(alert).not.toHaveTextContent("Couldn't load the agent's replies")
    expect(screen.queryByText('The agent ran into an error.')).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Reply to agent' })).toHaveValue('Still there?')
  })

  it('clears the notice once the text is edited, and Send sends the edit', async () => {
    vi.mocked(replyApplicationTask).mockRejectedValueOnce(gatewayTimeout()).mockResolvedValue({ ...task, status: 'pending', version: 4 })
    renderThread()
    const user = await sendFollowUp('First try')
    await screen.findByRole('alert')
    await user.type(screen.getByRole('textbox', { name: 'Reply to agent' }), ', edited')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Send reply' }))
    await waitFor(() => expect(replyApplicationTask).toHaveBeenLastCalledWith('workspace', task, 'prompt', 'First try, edited', undefined))
  })

  it('names a failed retry request and resends it', async () => {
    vi.mocked(getApplicationTask).mockResolvedValue(failed)
    vi.mocked(listApplicationTasks).mockResolvedValue([failed.session])
    vi.mocked(replyApplicationTask).mockRejectedValueOnce(gatewayTimeout()).mockResolvedValue({ ...task, status: 'pending', version: 4 })
    renderThread()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Retry' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't ask the agent to try again.")
    await user.click(within(alert).getByRole('button', { name: 'Resend' }))
    await waitFor(() => expect(replyApplicationTask).toHaveBeenCalledTimes(2))
    expect(vi.mocked(replyApplicationTask).mock.calls.map(call => call[2])).toEqual(['retry', 'retry'])
  })

  it('says it in Chinese', async () => {
    localStorage.setItem(LOCALE_STORAGE_KEY, 'zh-CN')
    try {
      vi.mocked(replyApplicationTask).mockRejectedValue(gatewayTimeout())
      renderThread()
      await sendFollowUp('还有一个问题')
      const alert = await screen.findByRole('alert')
      expect(within(alert).getByText('无法发送你的后续消息。')).toBeVisible()
      expect(within(alert).getByRole('button', { name: '重新发送' })).toBeVisible()
      expect(screen.getByRole('textbox')).toHaveValue('还有一个问题')
    } finally { localStorage.removeItem(LOCALE_STORAGE_KEY) }
  })
})

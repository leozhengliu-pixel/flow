import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'
import type { WorkspaceRole } from '@/types/flow'
import { AGENT_TASK_ACTIVITY_EVENT, archiveApplicationTask, getApplicationTask, listApplicationTasks, type ApplicationActivity, type ApplicationTask } from '@/lib/application-agents'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { IssueAgentTasks, IssueAgentTasksProvider } from './issue-agent-tasks'

vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), listApplicationTasks: vi.fn(), getApplicationTask: vi.fn(), archiveApplicationTask: vi.fn() }))
vi.mock('@/components/ui/action-dialog-service', async original => ({ ...await original<typeof import('@/components/ui/action-dialog-service')>(), confirmAction: vi.fn() }))
vi.mock('./agent-rich-text', () => ({ AgentRichText: ({ content }: { content: string }) => <div>{content}</div> }))

const app = { ...viewer, id: 'app-one', displayName: 'Agent', app: true, appScopes: ['app:assignable'], appTeamIds: ['team-1'] }
const base: ApplicationTask = { id: 'session', issueId: 'issue-1', teamId: 'team-1', appUserId: app.id, creatorId: viewer.id, status: 'complete', version: 3, prompt: 'Inspect', trigger: 'delegation', updatedAt: '' }
const activity = (id: string, type: string, body: string, actorId = app.id): ApplicationActivity => ({ id, sessionId: base.id, actorId, type, body, createdAt: '' })

function setup(task: ApplicationTask, activities: ApplicationActivity[], options: { viewerRole?: WorkspaceRole; commentId?: string } = {}) {
  vi.mocked(listApplicationTasks).mockResolvedValue([task])
  vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities })
  const issue = makeIssue({ delegate: app }), data = makeBootstrap({ users: [viewer, teammate, app], viewerRole: options.viewerRole ?? 'member' })
  const tree = options.commentId
    ? <IssueAgentTasksProvider issue={issue} data={data}><IssueAgentTasks issue={issue} data={data} commentId={options.commentId}/></IssueAgentTasksProvider>
    : <IssueAgentTasks issue={issue} data={data}/>
  return render(<I18nProvider>{tree}</I18nProvider>)
}

const sessionSection = () => document.querySelector('[data-agent-task-id="session"]')

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(confirmAction).mockResolvedValue(true)
  vi.mocked(archiveApplicationTask).mockResolvedValue({ session: { ...base, status: 'canceled', archivedAt: '2026-10-26T00:00:00Z' }, archived: true, alreadyArchived: false, stopped: true, previousStatus: 'active' })
})

describe('archiving a whole agent session', () => {
  it('archives a delegated session from its header menu after a confirmation that names the scope', async () => {
    const user = userEvent.setup()
    setup(base, [activity('p', 'prompt', 'Try again', viewer.id), activity('r', 'response', 'Done')])
    expect(await screen.findByText('Done')).toBeInTheDocument()
    const header = document.querySelector('.issue-agent-delegation') as HTMLElement
    await user.click(within(header).getByRole('button', { name: 'Agent session options' }))
    expect((await screen.findAllByRole('menuitem')).map(item => item.textContent)).toEqual(['Copy link to session', 'Archive session…'])
    await user.click(screen.getByRole('menuitem', { name: 'Archive session…' }))
    const [title, dialog] = vi.mocked(confirmAction).mock.calls[0]
    expect(title).toBe('Archive this agent session?')
    expect(dialog?.description).toContain('all of its messages, including the replies people sent the agent')
    expect(dialog?.description).toContain('A running session is stopped first')
    expect(dialog?.description).toContain('The execution log, the audit log, reports and documents it created, attachments and linked issues are kept')
    expect(dialog?.description).not.toContain('The comment that mentioned the agent stays')
    expect(dialog?.confirmLabel).toBe('Archive session')
    await waitFor(() => expect(archiveApplicationTask).toHaveBeenCalledWith('workspace', 'session'))
    // The person's reply and the agent's answer leave with the session, not one by one.
    await waitFor(() => expect(sessionSection()).toBeNull())
    expect(screen.queryByText('Try again')).not.toBeInTheDocument()
    expect(screen.queryByText('Done')).not.toBeInTheDocument()
  })

  it('offers it on a failed comment-triggered run next to the reply-only Delete, and keeps the mention comment', async () => {
    const user = userEvent.setup()
    const failed = { ...base, status: 'error' as const, trigger: 'mention', commentId: 'comment-one' }
    setup(failed, [activity('e', 'error', 'Model failed')], { commentId: 'comment-one' })
    await user.click(await screen.findByRole('button', { name: 'Comment options' }))
    const items = (await screen.findAllByRole('menuitem')).map(item => item.textContent)
    expect(items.slice(-2)).toEqual(['Delete', 'Archive session…'])
    await user.click(screen.getByRole('menuitem', { name: 'Archive session…' }))
    expect(vi.mocked(confirmAction).mock.calls[0][1]?.description).toContain('The comment that mentioned the agent stays.')
    await waitFor(() => expect(archiveApplicationTask).toHaveBeenCalledWith('workspace', 'session'))
    await waitFor(() => expect(sessionSection()).toBeNull())
  })

  it('reaches a canceled session through a reply a person sent or its reply row, and a running one through its working row', async () => {
    const user = userEvent.setup()
    const canceled = { ...base, status: 'canceled' as const, trigger: 'mention', commentId: 'comment-one' }
    let view = setup(canceled, [activity('p', 'prompt', 'Use the other branch', viewer.id), activity('c', 'canceled', '', viewer.id)], { commentId: 'comment-one' })
    const message = (await screen.findByText('Use the other branch')).closest('.issue-agent-message') as HTMLElement
    expect(within(message).getByRole('button', { name: 'Agent session options' })).toBeInTheDocument()
    view.unmount()

    // Stopped before the agent said anything: no message row, so the reply row carries the menu.
    view = setup(canceled, [activity('c', 'canceled', '', viewer.id)], { commentId: 'comment-one' })
    await waitFor(() => expect(getApplicationTask).toHaveBeenCalled())
    const footer = await waitFor(() => { const element = document.querySelector('.issue-agent-idle-tools'); if (!element) throw new Error('no reply row'); return element as HTMLElement })
    expect(within(footer).getByRole('button', { name: 'Agent session options' })).toBeInTheDocument()
    view.unmount()

    const running = { ...base, status: 'active' as const, trigger: 'mention', commentId: 'comment-one' }
    setup(running, [activity('t', 'thought', 'Thinking')], { commentId: 'comment-one' })
    const row = await screen.findByRole('status')
    await user.click(within(row).getByRole('button', { name: 'Agent session options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Archive session…' }))
    await waitFor(() => expect(archiveApplicationTask).toHaveBeenCalledWith('workspace', 'session'))
  })

  it('does nothing when the confirmation is dismissed', async () => {
    const user = userEvent.setup()
    vi.mocked(confirmAction).mockResolvedValue(false)
    setup(base, [activity('r', 'response', 'Done')])
    await screen.findByText('Done')
    const header = document.querySelector('.issue-agent-delegation') as HTMLElement
    await user.click(within(header).getByRole('button', { name: 'Agent session options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Archive session…' }))
    await waitFor(() => expect(confirmAction).toHaveBeenCalled())
    expect(archiveApplicationTask).not.toHaveBeenCalled()
    expect(screen.getByText('Done')).toBeInTheDocument()
  })

  it('is not offered to a member who did not start the session; admins get it', async () => {
    const user = userEvent.setup()
    const someoneElses = { ...base, creatorId: teammate.id, commentId: 'comment-one', trigger: 'mention' }
    const view = setup(someoneElses, [activity('r', 'response', 'Done')], { commentId: 'comment-one' })
    await user.click(await screen.findByRole('button', { name: 'Comment options' }))
    expect((await screen.findAllByRole('menuitem')).map(item => item.textContent)).not.toContain('Archive session…')
    expect(screen.queryByRole('button', { name: 'Agent session options' })).not.toBeInTheDocument()
    view.unmount()

    setup(someoneElses, [activity('r', 'response', 'Done')], { commentId: 'comment-one', viewerRole: 'admin' })
    await user.click(await screen.findByRole('button', { name: 'Comment options' }))
    expect((await screen.findAllByRole('menuitem')).map(item => item.textContent)).toContain('Archive session…')
  })

  it('shows the server error and keeps the session when the archive is refused', async () => {
    const user = userEvent.setup()
    vi.mocked(archiveApplicationTask).mockRejectedValue(new Error('Only the person who started this agent session or a workspace admin can archive it'))
    setup(base, [activity('r', 'response', 'Done')])
    await screen.findByText('Done')
    const header = document.querySelector('.issue-agent-delegation') as HTMLElement
    await user.click(within(header).getByRole('button', { name: 'Agent session options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Archive session…' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Only the person who started this agent session')
    expect(screen.getByText('Done')).toBeInTheDocument()
  })

  it('drops a session another client archived on the realtime push, and an archived one never shows', async () => {
    const view = setup(base, [activity('r', 'response', 'Done')])
    expect(await screen.findByText('Done')).toBeInTheDocument()
    // Another tab archived it: the list (which leaves archived sessions out) reloads on the agent_task push.
    vi.mocked(listApplicationTasks).mockResolvedValue([])
    await act(async () => { window.dispatchEvent(new CustomEvent(AGENT_TASK_ACTIVITY_EVENT, { detail: makeIssue().id })) })
    await waitFor(() => expect(sessionSection()).toBeNull())
    view.unmount()

    // A late read that still carries the session (an older list) does not bring it back once it is archived.
    setup({ ...base, version: 4, archivedAt: '2026-10-26T00:00:00Z' }, [activity('r', 'response', 'Done')])
    await waitFor(() => expect(getApplicationTask).toHaveBeenCalled())
    expect(sessionSection()).toBeNull()
    expect(screen.queryByText('Done')).not.toBeInTheDocument()
  })
})

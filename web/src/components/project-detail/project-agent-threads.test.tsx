import type { ComponentProps } from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project, viewer } from '@/test/fixtures'
import { getApplicationTask, listApplicationTasks, replyApplicationTask, type ApplicationActivity, type ApplicationTask } from '@/lib/application-agents'
import { IssueAgentTasks, IssueAgentTasksProvider } from '@/components/agent/issue-agent-tasks'
import type { Comment, ProjectUpdate } from '@/types/flow'
import { ProjectActivity } from './project-activity'

vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), listApplicationTasks: vi.fn(), getApplicationTask: vi.fn(), replyApplicationTask: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), listProjectHistory: vi.fn(async () => ({ nodes: [], nextCursor: '', total: 0 })) }))
vi.mock('@/lib/route-pages', () => ({ AgentChatPanel: () => null }))
vi.mock('@/components/agent/agent-rich-text', () => ({ AgentRichText: ({ content }: { content: string }) => <div>{content}</div> }))
// The thread composer is a full editor; a form keeps these tests on where the agent session renders.
vi.mock('@/components/editor/composer', () => ({ Composer: ({ placeholder }: { placeholder: string }) => <form aria-label={placeholder}/> }))

const agent = { ...viewer, id: 'app-agent', name: 'agent', displayName: 'Agent', app: true, active: true, appScopes: ['app:mentionable'], appTeamIds: ['team-1'] }
const data = makeBootstrap({ users: [viewer, agent] })
const at = (minutesAgo: number) => new Date(Date.now() - minutesAgo * 60_000).toISOString()
const root = { id: 'c-root', version: 1, body: '@Agent what is the status?', user: viewer, createdAt: at(10), reactions: {} } as Comment
const session = (overrides: Partial<ApplicationTask>): ApplicationTask => ({ id: 'task-1', issueId: '', teamId: 'team-1', commentId: root.id, appUserId: agent.id, creatorId: viewer.id, status: 'complete', version: 3, prompt: root.body, trigger: 'mention', createdAt: at(10), updatedAt: at(9), ...overrides })
const reply = (body: string, id = 'r1'): ApplicationActivity => ({ id, sessionId: 'task-1', actorId: agent.id, type: 'response', body, createdAt: at(9) })

function serve(tasks: ApplicationTask[], activities: Record<string, ApplicationActivity[]>) {
  vi.mocked(listApplicationTasks).mockResolvedValue(tasks)
  vi.mocked(getApplicationTask).mockImplementation(async (_workspace, id) => ({ session: tasks.find(task => task.id === id)!, activities: activities[id] ?? [] }))
}

function renderActivity(overrides: Partial<ComponentProps<typeof ProjectActivity>> = {}) {
  const props = {
    activities: [], drafts: [], issueData: data, project: { ...project, createdAt: '2026-01-01T00:00:00.000Z', comments: [root] }, projectUpdates: [], users: data.users, viewer, viewerRole: 'member',
    onCommentProject: vi.fn(async () => ({}) as never), onCommentProjectUpdate: vi.fn(async () => ({}) as never), onCreateIssue: vi.fn(), onCreateUpdate: vi.fn(), onDeleteProjectComment: vi.fn(), onDeleteUpdate: vi.fn(),
    onProjectCommentThreadSubscription: vi.fn(async () => undefined), onReactProjectComment: vi.fn(), onReactProjectUpdate: vi.fn(), onResolveProjectComment: vi.fn(async () => undefined), onUpdateProjectComment: vi.fn(), onUpdateProjectUpdate: vi.fn(),
    onUploadProjectUpdateAttachment: vi.fn(), onDeleteProjectUpdateAttachment: vi.fn(),
    ...overrides,
  } as unknown as ComponentProps<typeof ProjectActivity>
  return render(<I18nProvider><IssueAgentTasksProvider resourceType="project" issue={{ id: project.id }} data={data}>
    <ProjectActivity {...props}/>
    <IssueAgentTasks resourceType="project" issue={{ id: project.id }} data={data} standalone/>
  </IssueAgentTasksProvider></I18nProvider>)
}

beforeEach(() => { vi.clearAllMocks() })

describe('agent sessions in project threads', () => {
  it('answers inside the project comment thread, counts its reply and takes the reply box', async () => {
    const user = userEvent.setup()
    serve([session({})], { 'task-1': [reply('Backlog with 0% progress')] })
    const { container } = renderActivity()
    const card = container.querySelector<HTMLElement>('#comment-c-root')!
    // Collapsed, the thread counts the agent's reply like a comment (the reference app: "1 comment").
    const toggle = await within(card).findByRole('button', { name: 'Open 1 comment' })
    expect(screen.queryByText('Backlog with 0% progress')).toBeNull()
    await user.click(toggle)
    const thread = card.querySelector<HTMLElement>('.project-activity__thread')!
    expect(await within(thread).findByText('Backlog with 0% progress')).toBeVisible()
    expect(within(thread).getByRole('region', { name: /Agent Agent session/ })).toBeInTheDocument()
    // The session's composer continues the conversation; the plain reply composer is gone.
    expect(within(thread).queryByRole('form', { name: 'Leave a reply…' })).toBeNull()
    await user.click(within(thread).getByRole('button', { name: /Leave a reply/ }))
    vi.mocked(replyApplicationTask).mockResolvedValue(session({ status: 'pending', version: 4 }))
    await user.type(within(thread).getByRole('textbox', { name: 'Reply to agent' }), 'And the risks?')
    await user.click(within(thread).getByRole('button', { name: 'Send reply' }))
    await waitFor(() => expect(replyApplicationTask).toHaveBeenCalledWith('workspace', expect.objectContaining({ id: 'task-1' }), 'prompt', 'And the risks?', undefined))
    // Not repeated under the feed: only sessions without a source comment sit there.
    expect(container.querySelectorAll('[data-agent-task-id="task-1"]')).toHaveLength(1)
  })

  it('shows the Working… row in the thread while the agent works', async () => {
    const user = userEvent.setup()
    serve([session({ status: 'active' })], { 'task-1': [] })
    const { container } = renderActivity()
    const card = container.querySelector<HTMLElement>('#comment-c-root')!
    await waitFor(() => expect(listApplicationTasks).toHaveBeenCalled())
    await user.click(within(card).getByRole('button', { name: 'Open comments' }))
    const status = await within(card).findByRole('status')
    expect(status).toHaveClass('issue-agent-working')
    expect(status).toHaveTextContent('Agent')
    expect(status).toHaveTextContent('Working…')
  })

  it('keeps a session a reply started in that reply\'s thread', async () => {
    const user = userEvent.setup()
    const human = { ...root, id: 'c-reply', parentId: root.id, body: '@Agent and the date?', createdAt: at(5) } as Comment
    serve([session({ commentId: human.id })], { 'task-1': [reply('Target date is not set')] })
    const { container } = renderActivity({ project: { ...project, createdAt: '2026-01-01T00:00:00.000Z', comments: [root, human] } } as never)
    const card = container.querySelector<HTMLElement>('#comment-c-root')!
    await user.click(await within(card).findByRole('button', { name: 'Open 2 comments' }))
    expect(await within(card).findByText('Target date is not set')).toBeVisible()
  })

  it('answers inside a project update\'s comments', async () => {
    const user = userEvent.setup()
    const comment = { id: 'uc-1', version: 1, body: '@Agent summarize', user: viewer, createdAt: at(4), reactions: {} } as Comment
    const update: ProjectUpdate = { id: 'update-1', projectId: project.id, body: 'Shipped', health: 'onTrack', createdAt: at(6), user: viewer, comments: [comment], reactions: {}, attachments: [] } as ProjectUpdate
    serve([session({ commentId: comment.id })], { 'task-1': [reply('On track, still in backlog')] })
    const { container } = renderActivity({ project: { ...project, createdAt: '2026-01-01T00:00:00.000Z', comments: [] }, projectUpdates: [update] } as never)
    const entry = container.querySelector<HTMLElement>('[data-update-id="update-1"]')!
    await user.click(await within(entry).findByRole('button', { name: '2 comments' }))
    const comments = entry.querySelector<HTMLElement>('.project-activity__comments')!
    expect(await within(comments).findByText('On track, still in backlog')).toBeVisible()
    expect(within(comments).queryByRole('textbox', { name: 'Add comment' })).toBeNull()
    expect(within(comments).getByRole('button', { name: /Leave a reply/ })).toBeInTheDocument()
  })
})

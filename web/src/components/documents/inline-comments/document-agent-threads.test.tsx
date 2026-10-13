import { render, screen, within } from '@testing-library/react'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import { getApplicationTask, listApplicationTasks, type ApplicationActivity, type ApplicationTask } from '@/lib/application-agents'
import { IssueAgentTasks, IssueAgentTasksProvider } from '@/components/agent/issue-agent-tasks'
import type { Comment, FlowDocument } from '@/types/flow'
import { DocumentInlineComments } from './document-inline-comments'

vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), listApplicationTasks: vi.fn(), getApplicationTask: vi.fn() }))
vi.mock('@/components/agent/agent-rich-text', () => ({ AgentRichText: ({ content }: { content: string }) => <div>{content}</div> }))
vi.mock('@/components/editor/composer', () => ({ Composer: ({ placeholder }: { placeholder: string }) => <form aria-label={placeholder}/> }))
vi.mock('@/components/activity/rich-comment', () => ({ RichComment: ({ body }: { body: string }) => <p>{body}</p> }))

beforeAll(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
  window.matchMedia = ((query: string) => ({ matches: 1470 >= Number(/min-width: (\d+)px/.exec(query)?.[1] ?? 0), media: query, addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {}, onchange: null, dispatchEvent: () => false })) as typeof window.matchMedia
})

const agent = { ...viewer, id: 'app-agent', name: 'agent', displayName: 'Agent', app: true, active: true, appScopes: ['app:mentionable'], appTeamIds: ['team-1'] }
const flowDocument = { id: 'document-1', slugId: 'plan-1a2b3c4d5e6f', title: 'Plan', content: '', creator: viewer, projectIds: [], teamIds: ['team-1'], subscriberIds: [viewer.id], favorite: false, createdAt: '2026-09-01T02:30:00.000Z', updatedAt: '2026-09-01T04:49:00.000Z', revisions: [] } as FlowDocument
const data = makeBootstrap({ users: [viewer, teammate, agent], documents: [flowDocument] })
const root: Comment = { id: 'comment-1', version: 1, body: '@Agent suggest a better word', reactions: {}, createdAt: '2026-10-01T10:00:00.000Z', user: viewer }
const task: ApplicationTask = { id: 'task-1', issueId: '', teamId: 'team-1', commentId: root.id, appUserId: agent.id, creatorId: viewer.id, status: 'complete', version: 2, prompt: root.body, trigger: 'mention', createdAt: root.createdAt, updatedAt: root.createdAt }
const reply: ApplicationActivity = { id: 'r1', sessionId: task.id, actorId: agent.id, type: 'response', body: 'multiple', createdAt: '2026-10-01T10:01:00.000Z' }

function renderPage(session: ApplicationTask, activities: ApplicationActivity[]) {
  vi.mocked(listApplicationTasks).mockResolvedValue([session])
  vi.mocked(getApplicationTask).mockResolvedValue({ session, activities })
  return render(<I18nProvider><IssueAgentTasksProvider resourceType="document" issue={{ id: flowDocument.id }} data={data}>
    <DocumentInlineComments data={data} document={flowDocument} comments={[root]} editor={null} shell={document.createElement('div')} onDraftChange={vi.fn()} canComment canEdit onReload={vi.fn(async () => undefined)}/>
    <section aria-label="Activity"><IssueAgentTasks resourceType="document" issue={{ id: flowDocument.id }} data={data} standalone/></section>
  </IssueAgentTasksProvider></I18nProvider>)
}

beforeEach(() => { vi.clearAllMocks() })

describe('agent sessions in document comment cards', () => {
  it('answers inside the comment card, not in the Activity section below the document', async () => {
    renderPage(task, [reply])
    const card = await screen.findByRole('group', { name: 'Comment thread' })
    expect(await within(card).findByText('multiple')).toBeVisible()
    const sessions = card.querySelector('.issue-agent-threads')!
    expect(sessions).toHaveClass('is-document-card')
    // The card's reply row continues the session ("Reply…", like the card's own row); the reference app's card has no votes.
    expect(within(card).getByRole('button', { name: /Reply…/ })).toHaveClass('issue-agent-start-reply')
    expect(within(card).queryByRole('button', { name: /React with thumbs up/ })).toBeNull()
    expect(card.querySelector('.document-thread-reply-trigger')).toBeNull()
    expect(within(screen.getByRole('region', { name: 'Activity' })).queryByText('multiple')).toBeNull()
  })

  it('shows the Working… row in the card while the agent works', async () => {
    renderPage({ ...task, status: 'active' }, [])
    const card = await screen.findByRole('group', { name: 'Comment thread' })
    const status = await within(card).findByRole('status')
    expect(status).toHaveTextContent('Agent')
    expect(status).toHaveTextContent('Working…')
  })

  it('offers the asked question\'s options in the card', async () => {
    const question: ApplicationActivity = { id: 'q1', sessionId: task.id, actorId: agent.id, type: 'elicitation', body: 'Which tone?', createdAt: reply.createdAt, signal: 'select', signalMetadata: { options: [{ value: 'Formal' }, { value: 'Casual' }] } }
    renderPage({ ...task, status: 'awaitingInput' }, [question])
    const card = await screen.findByRole('group', { name: 'Comment thread' })
    expect(await within(card).findByRole('button', { name: 'Formal' })).toBeEnabled()
    expect(within(card).getByRole('button', { name: 'Casual' })).toBeEnabled()
  })
})

import { render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, Comment, FlowDocument, Notification } from '@/types/flow'

const api = vi.hoisted(() => ({ listDocumentComments: vi.fn(), updateInboxNotification: vi.fn(async () => undefined) }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), ...api }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/components/activity/rich-comment', () => ({ RichComment: ({ body }: { body: string }) => <p>{body}</p> }))

import { InboxAppPage } from './inbox-app-page'

const agent = { ...viewer, id: 'app-flow', name: 'flow', displayName: 'Flow', app: true }
const flowDocument = { id: 'document-1', slugId: 'plan-1a2b3c4d5e6f', title: 'Plan', content: '', creator: viewer, projectIds: [], teamIds: [], subscriberIds: [], favorite: false, createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z', revisions: [] } as unknown as FlowDocument
const comment = (id: string, body: string, user = viewer, parentId?: string) => ({ id, version: 1, body, parentId, reactions: {}, createdAt: '2026-10-02T00:00:00Z', user }) as unknown as Comment

function agentReply(overrides: Partial<Notification>): Notification {
  return {
    id: 'n', recipientId: viewer.id, type: 'agentReply', issueId: '', commentId: 'mention', actor: agent, category: 'comments',
    groupKey: 'g', occurrenceCount: 2, latestActorIds: [agent.id], favorite: false, text: 'Flow replied: Two fields changed.',
    payload: { excerpt: 'Two fields changed.', agentActivity: 'response' }, createdAt: '2026-10-02T00:00:00Z', updatedAt: '2026-10-02T01:00:00Z',
    ...overrides,
  } as Notification
}

const documentReply = agentReply({ id: 'n-document', sourceType: 'document', sourceId: flowDocument.id, title: 'Plan', payload: { excerpt: 'Two fields changed.', agentActivity: 'response', documentSlugId: flowDocument.slugId } })
const projectReply = agentReply({ id: 'n-project', sourceType: 'project', sourceId: project.id, projectId: project.id, commentId: 'project-reply', title: project.name, payload: { excerpt: 'Which priority?', agentActivity: 'elicitation' } })

function bootstrap() {
  const projectWithThread = { ...project, comments: [comment('project-root', 'Kickoff notes', teammate), comment('project-reply', '@Flow summarize this', viewer, 'project-root')] }
  return makeBootstrap({ notifications: [documentReply, projectReply], documents: [flowDocument], projects: [projectWithThread], reviews: [], comments: {}, userSettings: {} } as unknown as Partial<BootstrapData>)
}

function renderInbox(data: BootstrapData) {
  const noop = vi.fn(async () => undefined)
  return render(<I18nProvider><TooltipProvider>
    <InboxAppPage data={data} onReload={noop} onOpenIssue={vi.fn()} onDeleteRelation={noop} onCreateSubIssue={noop} onReactIssue={noop} onCreateComment={noop} onEditComment={noop} onDeleteComment={noop} onReactComment={noop} onUploadAttachment={noop} onDeleteAttachment={noop}/>
  </TooltipProvider></I18nProvider>)
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.clearAllMocks()
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
  api.listDocumentComments.mockResolvedValue([comment('mention', '@Flow what changed?')])
})
afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

describe('agent replies in document and project threads', () => {
  it('lists one row per thread with the resource, the agent line and no occurrence count', () => {
    window.history.replaceState(null, '', '/workspace/inbox')
    renderInbox(bootstrap())
    const rows = screen.getAllByRole('link')
    const documentRow = rows.find(row => row.textContent?.includes('Plan'))!
    expect(within(documentRow).getByText('Document')).toBeInTheDocument()
    expect(within(documentRow).getByText('Flow replied: Two fields changed.')).toBeInTheDocument()
    const projectRow = rows.find(row => row.textContent?.includes(project.name))!
    expect(within(projectRow).getByText('Project')).toBeInTheDocument()
    expect(within(projectRow).getByText('Flow is waiting for your input: Which priority?')).toBeInTheDocument()
    expect(rows.some(row => /updates/.test(row.textContent ?? ''))).toBe(false)
  })

  it('opens a document reply on its thread with a link to the comment', async () => {
    window.history.replaceState(null, '', `/workspace/inbox?notification=${documentReply.id}`)
    renderInbox(bootstrap())
    expect(await screen.findByRole('heading', { name: 'Plan' })).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('@Flow what changed?')).toBeInTheDocument())
    expect(screen.getByText('Flow replied')).toBeInTheDocument()
    expect(screen.getAllByText('Two fields changed.').length).toBeGreaterThan(0)
    expect(screen.getByRole('link', { name: /Open document/ }).getAttribute('href')).toBe(`/workspace/document/${flowDocument.slugId}#comment-mention`)
  })

  it('opens a project reply on its thread with a link to the comment on the Activity tab', async () => {
    window.history.replaceState(null, '', `/workspace/inbox?notification=${projectReply.id}`)
    renderInbox(bootstrap())
    expect(await screen.findByRole('heading', { name: project.name })).toBeInTheDocument()
    expect(screen.getByText('Kickoff notes')).toBeInTheDocument()
    expect(screen.getByText('@Flow summarize this')).toBeInTheDocument()
    expect(screen.getByText('Flow is waiting for your input')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Open project/ }).getAttribute('href')).toBe(`/workspace/project/${project.slugId}/activity#comment-project-reply`)
  })

  it('reads the rows in Chinese', () => {
    window.history.replaceState(null, '', '/workspace/inbox')
    localStorage.setItem('flow:locale', 'zh-CN')
    renderInbox(bootstrap())
    expect(screen.getByText('Flow 回复了：Two fields changed.')).toBeInTheDocument()
    expect(screen.getByText('Flow 正在等待你的回复：Which priority?')).toBeInTheDocument()
  })
})

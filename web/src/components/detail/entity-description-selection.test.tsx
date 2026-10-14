import type { ComponentProps } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mentionFixture } from '@/components/editor/mentions/mention-fixtures'
import { MentionShell } from '@/test/mention-host-harness'
import type { DescriptionSelectionActions } from '@/components/issue/editor/structured-blocks'
import type { Initiative } from '@/types/flow'

// The real rich editor needs a browser selection; this stand-in hands the test the actions the floating toolbar gets.
vi.mock('@/components/issue/issue-description-editor', () => ({
  IssueDescriptionEditor: ({ ariaLabel, selectionActions }: { ariaLabel?: string; selectionActions?: DescriptionSelectionActions }) => <div role="textbox" aria-label={ariaLabel}>
    {selectionActions?.onAskAgent && <button type="button" onClick={() => selectionActions.onAskAgent?.({ from: 1, to: 14, text: 'Ship the beta\nby Friday' })}>{`Ask agent in ${ariaLabel}`}</button>}
    {selectionActions?.onCreateIssue && <button type="button" onClick={() => selectionActions.onCreateIssue?.({ from: 1, to: 14, text: 'Ship the beta' })}>{`Create issue in ${ariaLabel}`}</button>}
    {selectionActions?.onComment && <button type="button" onClick={() => selectionActions.onComment?.({ from: 1, to: 14, text: 'Ship the beta' })}>Comment</button>}
  </div>,
}))
vi.mock('@/lib/toast', () => ({ toast: Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn(), info: vi.fn() }) }))

import { ProjectOverview } from '@/components/project-detail/project-overview'
import { InitiativeDescriptionField } from '@/components/initiatives/initiative-detail-page'
import { OPEN_FLOATING_AGENT_EVENT } from '@/components/agent/agent-floating-request'
import { CREATE_ISSUE_INLINE_EVENT, type InlineIssueRequest } from '@/components/editor/selection-issue'
import type { Issue } from '@/types/flow'

const data = mentionFixture()

/** Records what the page asks of the app: floating-chat requests and in-place issue creations. */
function listen() {
  const chats: unknown[] = []
  const creates: InlineIssueRequest['input'][] = []
  const onChat = (event: Event) => chats.push((event as CustomEvent).detail)
  const onCreate = (event: Event) => {
    const detail = (event as CustomEvent<InlineIssueRequest>).detail
    creates.push(detail.input)
    detail.result = Promise.resolve({ ...data.issues[0], id: 'issue-new', title: detail.input.title } as Issue)
  }
  window.addEventListener(OPEN_FLOATING_AGENT_EVENT, onChat)
  window.addEventListener(CREATE_ISSUE_INLINE_EVENT, onCreate)
  return { chats, creates, stop: () => { window.removeEventListener(OPEN_FLOATING_AGENT_EVENT, onChat); window.removeEventListener(CREATE_ISSUE_INLINE_EVENT, onCreate) } }
}

function projectProps() {
  const project = data.projects[0]
  return {
    issueData: data, project: { ...project, teamIds: [data.teams[0].id], milestones: [], resources: [], customers: [] }, projects: [project], initiatives: [], documents: data.documents,
    projectStatuses: [project.status], projectUpdates: [], users: data.users, teams: data.teams, labels: [], labelGroups: [], projectIssues: [],
    save: vi.fn(), onTabChange: vi.fn(), onCreateResource: vi.fn(), onUpdateResource: vi.fn(), onDeleteResource: vi.fn(),
    onCreateMilestone: vi.fn(), onUpdateMilestone: vi.fn(), onDeleteMilestone: vi.fn(),
  } as unknown as ComponentProps<typeof ProjectOverview>
}

describe('Ask agent on a project and an initiative description', () => {
  beforeEach(() => { localStorage.setItem('flow:locale', 'en-US') })
  afterEach(() => { vi.unstubAllGlobals() })

  it('opens the floating chat with the selection as a quote chip, and offers an inline comment', () => {
    const app = listen()
    render(<MentionShell data={data}><ProjectOverview {...projectProps()}/></MentionShell>)
    expect(screen.getByRole('button', { name: 'Comment' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Ask agent in Project description' }))
    app.stop()
    // The floating chat brings the page (the project) as context.
    expect(app.chats).toEqual([{ quote: 'Ship the beta\nby Friday' }])
  })

  it('does the same on an initiative description', () => {
    const app = listen()
    const initiative = data.initiatives[0] as Initiative
    render(<MentionShell data={data}><InitiativeDescriptionField initiative={initiative} workspace={data.workspace.urlKey} data={data} label="Initiative description" value="" onCommit={vi.fn()}/></MentionShell>)
    fireEvent.click(screen.getByRole('button', { name: 'Ask agent in Initiative description' }))
    app.stop()
    expect(app.chats).toEqual([{ quote: 'Ship the beta\nby Friday' }])
  })

  it('starts an inline comment on the selection beside the project description', () => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    render(<MentionShell data={data}><ProjectOverview {...projectProps()}/></MentionShell>)
    expect(screen.queryByRole('complementary', { name: 'Comments' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Comment' }))
    expect(screen.getByRole('complementary', { name: 'Comments' })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Add a comment…' })).toBeInTheDocument()
  })

  it('offers an inline comment on an initiative description when the page handles it', () => {
    const initiative = data.initiatives[0] as Initiative
    const onComment = vi.fn()
    render(<MentionShell data={data}><InitiativeDescriptionField initiative={initiative} workspace={data.workspace.urlKey} data={data} label="Initiative description" value="" onCommit={vi.fn()} onComment={onComment}/></MentionShell>)
    fireEvent.click(screen.getByRole('button', { name: 'Comment' }))
    expect(onComment).toHaveBeenCalledWith({ from: 1, to: 14, text: 'Ship the beta' })
  })

  it('makes the issue right away in the project and its team (no dialog)', () => {
    const app = listen()
    const props = projectProps()
    render(<MentionShell data={data}><ProjectOverview {...props}/></MentionShell>)
    fireEvent.click(screen.getByRole('button', { name: 'Create issue in Project description' }))
    app.stop()
    expect(app.creates).toEqual([expect.objectContaining({ title: 'Ship the beta', description: '', teamId: data.teams[0].id, projectId: props.project.id, parentId: undefined })])
  })

  it('makes an initiative\'s issue in the first team, outside any project', () => {
    const app = listen()
    const initiative = data.initiatives[0] as Initiative
    render(<MentionShell data={data}><InitiativeDescriptionField initiative={initiative} workspace={data.workspace.urlKey} data={data} label="Initiative description" value="" onCommit={vi.fn()}/></MentionShell>)
    fireEvent.click(screen.getByRole('button', { name: 'Create issue in Initiative description' }))
    app.stop()
    expect(app.creates).toEqual([expect.objectContaining({ title: 'Ship the beta', teamId: data.teams[0].id, projectId: undefined })])
  })
})

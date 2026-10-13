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
    {selectionActions?.onComment && <button type="button">Comment</button>}
  </div>,
}))
const chat = vi.hoisted(() => vi.fn())
vi.mock('@/lib/route-pages', () => ({ AgentChatPanel: (props: Record<string, unknown>) => { chat(props); return <div data-testid="agent-chat">{String(props.initialPrompt)}</div> } }))

import { ProjectOverview } from '@/components/project-detail/project-overview'
import { InitiativeDescriptionField } from '@/components/initiatives/initiative-detail-page'
import { CREATE_ISSUE_FROM_SELECTION } from './description-selection-actions'

const data = mentionFixture()

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
  beforeEach(() => { localStorage.setItem('flow:locale', 'en-US'); chat.mockClear() })
  afterEach(() => { vi.unstubAllGlobals() })

  it('opens the chat on the project with the selection quoted, and offers no inline comment', () => {
    const props = projectProps()
    render(<MentionShell data={data}><ProjectOverview {...props}/></MentionShell>)
    expect(screen.queryByRole('button', { name: 'Comment' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Ask agent in Project description' }))
    expect(screen.getByTestId('agent-chat')).toHaveTextContent('About this selection from the project description:')
    expect(chat).toHaveBeenLastCalledWith(expect.objectContaining({
      initialPrompt: 'About this selection from the project description:\n\n> Ship the beta\n> by Friday\n\n',
      pageContext: expect.objectContaining({ type: 'project', id: props.project.id, label: props.project.name }),
      open: true,
    }))
    expect(chat.mock.calls.at(-1)![0]).not.toHaveProperty('autoSubmit', true)
  })

  it('opens the chat on the initiative as its context', () => {
    const initiative = data.initiatives[0] as Initiative
    render(<MentionShell data={data}><InitiativeDescriptionField initiative={initiative} workspace={data.workspace.urlKey} data={data} label="Initiative description" value="" onCommit={vi.fn()}/></MentionShell>)
    fireEvent.click(screen.getByRole('button', { name: 'Ask agent in Initiative description' }))
    expect(chat).toHaveBeenLastCalledWith(expect.objectContaining({
      initialPrompt: 'About this selection from the initiative description:\n\n> Ship the beta\n> by Friday\n\n',
      pageContext: { type: 'initiative', id: initiative.id, label: initiative.name },
    }))
  })

  it('makes an issue from the selection, linking the project it belongs to', () => {
    const received: CustomEvent[] = []
    const listener = (event: Event) => received.push(event as CustomEvent)
    window.addEventListener(CREATE_ISSUE_FROM_SELECTION, listener)
    const props = projectProps()
    render(<MentionShell data={data}><ProjectOverview {...props}/></MentionShell>)
    fireEvent.click(screen.getByRole('button', { name: 'Create issue in Project description' }))
    window.removeEventListener(CREATE_ISSUE_FROM_SELECTION, listener)
    expect(received).toHaveLength(1)
    expect(received[0].detail).toEqual({ text: expect.stringContaining('Ship the beta\n\nFrom http'), teamId: data.teams[0].id, projectId: props.project.id })
  })
})

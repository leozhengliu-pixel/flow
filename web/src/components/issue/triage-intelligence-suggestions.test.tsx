import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TriageIntelligenceSuggestions } from './triage-intelligence-suggestions'
import { makeBootstrap, makeIssue, project, viewer } from '@/test/fixtures'
import type { IssueSuggestion } from '@/types/flow'

const mocks = vi.hoisted(() => ({
  acceptIssueSuggestion: vi.fn(),
  dismissIssueSuggestion: vi.fn(),
  fetchIssueRecord: vi.fn(),
  fetchIssueSuggestions: vi.fn(),
  refreshIssueSuggestions: vi.fn(),
}))

vi.mock('@/lib/api', () => mocks)

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

describe('TriageIntelligenceSuggestions', () => {
  beforeEach(() => {
    globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver
    vi.clearAllMocks()
    mocks.acceptIssueSuggestion.mockResolvedValue({})
    mocks.dismissIssueSuggestion.mockResolvedValue({})
    mocks.fetchIssueRecord.mockImplementation(async (id: string) => makeIssue({ id }))
    mocks.fetchIssueSuggestions.mockResolvedValue({ issueId: 'issue-triage', suggestions: [] })
    mocks.refreshIssueSuggestions.mockResolvedValue([])
  })

  it('renders active suggestions and accepts one', async () => {
    const user = userEvent.setup()
    const issue = makeIssue({
      id: 'issue-triage',
      identifier: 'TST-20',
      title: 'Vehicle marketplace preview',
      state: { id: 'state-backlog', name: 'Backlog', color: '#777', type: 'backlog', position: 0 },
      triagedAt: undefined,
      suggestionsGeneratedAt: '2026-09-13T01:00:00.000Z',
    })
    const suggestion: IssueSuggestion = {
      id: 'suggestion-1',
      issueId: issue.id,
      type: 'assignee',
      state: 'active',
      stateChangedAt: '2026-09-13T01:00:00.000Z',
      metadata: { rank: 1, score: 0.8, reasons: ['Assigned to a closely related issue.'] },
      suggestedUserId: viewer.id,
      createdAt: '2026-09-13T01:00:00.000Z',
      updatedAt: '2026-09-13T01:00:00.000Z',
    }
    const data = makeBootstrap({
      issues: [issue],
      issueSuggestions: [suggestion],
      workspaceSettings: {
        featureFlags: { 'triage-intelligence': true },
        featureSettings: {
          triageIntelligence: {
            assigneeAction: 'suggest', projectAction: 'suggest', labelAction: 'suggest',
            teamAction: 'suggest', duplicateAction: 'suggest', relatedAction: 'suggest',
          },
        },
      } as never,
    })
    mocks.fetchIssueSuggestions.mockResolvedValue({ issueId: issue.id, suggestions: [suggestion] })

    render(<TriageIntelligenceSuggestions issue={issue} data={data} />)

    await waitFor(() => expect(mocks.fetchIssueSuggestions).toHaveBeenCalledWith(issue.id, expect.any(AbortSignal)))
    expect(screen.getByText('Triage Intelligence')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Assign to user: Viewer' }))
    expect(await screen.findByText('Why this assignee was suggested')).toBeInTheDocument()
    expect(screen.getByText('Assigned to a closely related issue.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Accept suggestion' }))
    expect(mocks.acceptIssueSuggestion).toHaveBeenCalledWith(issue.id, suggestion.id)
    expect(mocks.fetchIssueRecord).toHaveBeenCalledWith(issue.id, undefined, data.workspace.urlKey)
    await waitFor(() => expect(screen.queryByText('Viewer')).not.toBeInTheDocument())
  })

  it('renders related issue links with the project color', async () => {
    const issue = makeIssue({
      id: 'issue-triage',
      state: { id: 'state-backlog', name: 'Backlog', color: '#777', type: 'backlog', position: 0 },
      triagedAt: undefined,
      suggestionsGeneratedAt: '2026-09-13T01:00:00.000Z',
    })
    const data = makeBootstrap({
      issues: [issue],
      issueSuggestions: [{
        id: 'suggestion-2', issueId: issue.id, type: 'project', state: 'active',
        stateChangedAt: '2026-09-13T01:00:00.000Z', metadata: { rank: 1 },
        suggestedProjectId: project.id, createdAt: '2026-09-13T01:00:00.000Z', updatedAt: '2026-09-13T01:00:00.000Z',
      }],
      workspaceSettings: { featureFlags: { 'triage-intelligence': true } } as never,
    })
    mocks.fetchIssueSuggestions.mockResolvedValue({
      issueId: issue.id,
      suggestions: [{
        id: 'suggestion-2', issueId: issue.id, type: 'project', state: 'active',
        stateChangedAt: '2026-09-13T01:00:00.000Z', metadata: { rank: 1 },
        suggestedProjectId: project.id, createdAt: '2026-09-13T01:00:00.000Z', updatedAt: '2026-09-13T01:00:00.000Z',
      }],
    })

    render(<TriageIntelligenceSuggestions issue={issue} data={data} />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Add to project: Project one' }).querySelector('svg')).toHaveStyle({ color: project.color }))
  })

  it('stops showing the analyzing state when a completed empty analysis is returned', async () => {
    const issue = makeIssue({
      id: 'issue-empty-triage',
      state: { id: 'state-backlog', name: 'Backlog', color: '#777', type: 'backlog', position: 0 },
      triagedAt: undefined,
    })
    mocks.fetchIssueSuggestions.mockResolvedValue({
      issueId: issue.id,
      suggestionsGeneratedAt: '2026-09-13T01:00:00.000Z',
      suggestions: [],
    })

    render(<TriageIntelligenceSuggestions issue={issue} data={makeBootstrap({
      issues: [issue],
      workspaceSettings: { featureFlags: { 'triage-intelligence': true } } as never,
    })}/>)

    await waitFor(() => expect(screen.queryByText('Analyzing related work and inferring properties…')).not.toBeInTheDocument())
  })
})

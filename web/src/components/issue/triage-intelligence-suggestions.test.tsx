import { render, screen, waitFor, within } from '@testing-library/react'
import type { ReactElement } from 'react'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TriageIntelligenceSuggestions } from './triage-intelligence-suggestions'
import { makeBootstrap, makeIssue, project, viewer } from '@/test/fixtures'
import type { BootstrapData, Issue, IssueSuggestion } from '@/types/flow'

const mocks = vi.hoisted(() => ({
  acceptIssueSuggestion: vi.fn(),
  createRelation: vi.fn(),
  dismissIssueSuggestion: vi.fn(),
  fetchIssueRecord: vi.fn(),
  fetchIssueSuggestions: vi.fn(),
  refreshIssueSuggestions: vi.fn(),
}))

vi.mock('@/lib/api', () => mocks)
const renderCard = (node: ReactElement) => render(<I18nProvider><TooltipProvider>{node}</TooltipProvider></I18nProvider>)

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const GENERATED = '2026-09-13T01:00:00.000Z'
const backlogState = { id: 'state-backlog', name: 'Backlog', color: '#777', type: 'backlog', position: 0 } as Issue['state']

function triageIssue(overrides: Partial<Issue> = {}) {
  return makeIssue({ id: 'issue-triage', identifier: 'TST-20', title: 'Vehicle marketplace preview', state: backlogState, triagedAt: undefined, ...overrides })
}

function suggestion(overrides: Partial<IssueSuggestion>): IssueSuggestion {
  return {
    id: 'suggestion-1', issueId: 'issue-triage', type: 'assignee', state: 'active', stateChangedAt: GENERATED,
    metadata: { rank: 1 }, createdAt: GENERATED, updatedAt: GENERATED, ...overrides,
  }
}

function bootstrap(issues: Issue[], overrides: Partial<BootstrapData> = {}) {
  return makeBootstrap({
    issues,
    teamSettings: { 'team-1': { triageEnabled: true } } as never,
    workspaceSettings: { featureFlags: { 'triage-intelligence': true } } as never,
    ...overrides,
  })
}

function respond(issueId: string, suggestions: IssueSuggestion[], extra: Record<string, unknown> = { suggestionsGeneratedAt: GENERATED }) {
  mocks.fetchIssueSuggestions.mockResolvedValue({ issueId, suggestions, ...extra })
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

  it('shows the hover card for a property chip and accepts it', async () => {
    const user = userEvent.setup()
    const issue = triageIssue({ suggestionsGeneratedAt: GENERATED })
    const assignee = suggestion({ suggestedUserId: viewer.id, metadata: { rank: 1, score: 0.8, reasons: ['Assigned to a closely related issue.'] } })
    const data = bootstrap([issue], { issueSuggestions: [assignee] })
    respond(issue.id, [assignee])

    renderCard(<TriageIntelligenceSuggestions issue={issue} data={data} />)

    await waitFor(() => expect(mocks.fetchIssueSuggestions).toHaveBeenCalledWith(issue.id, expect.any(AbortSignal)))
    expect(screen.getByText('Triage Intelligence')).toBeInTheDocument()
    expect(screen.getByText('Suggestions')).toBeInTheDocument()
    await user.hover(screen.getByRole('button', { name: 'Assign to user: Viewer' }))
    expect(await screen.findByText('Why this assignee was suggested')).toBeInTheDocument()
    expect(screen.getByText('Assigned to a closely related issue.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Accept user suggestion' }))
    expect(mocks.acceptIssueSuggestion).toHaveBeenCalledWith(issue.id, assignee.id)
    expect(mocks.fetchIssueRecord).toHaveBeenCalledWith(issue.id, undefined, data.workspace.urlKey)
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Assign to user: Viewer' })).not.toBeInTheDocument())
  })

  it('dismisses a property suggestion from the hover card', async () => {
    const user = userEvent.setup()
    const issue = triageIssue({ suggestionsGeneratedAt: GENERATED })
    const projectSuggestion = suggestion({ id: 'suggestion-2', type: 'project', suggestedProjectId: project.id, metadata: { rank: 1, reasons: ['Matches the Project one fixture.'] } })
    respond(issue.id, [projectSuggestion])

    renderCard(<TriageIntelligenceSuggestions issue={issue} data={bootstrap([issue])} />)

    await user.click(await screen.findByRole('button', { name: 'Add to project: Project one' }))
    const card = await screen.findByRole('dialog', { name: 'Why this project was suggested' })
    expect(within(card).getByText('Summary')).toBeInTheDocument()
    expect(within(card).getByText('Matches the Project one fixture.')).toBeInTheDocument()
    await user.click(within(card).getByRole('button', { name: 'Dismiss suggestion' }))
    expect(mocks.dismissIssueSuggestion).toHaveBeenCalledWith(issue.id, projectSuggestion.id)
    expect(mocks.acceptIssueSuggestion).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.getByText('No suggestions found')).toBeInTheDocument())
  })

  it('renders project chips with the project colour', async () => {
    const issue = triageIssue({ suggestionsGeneratedAt: GENERATED })
    respond(issue.id, [suggestion({ id: 'suggestion-2', type: 'project', suggestedProjectId: project.id })])

    renderCard(<TriageIntelligenceSuggestions issue={issue} data={bootstrap([issue])} />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Add to project: Project one' }).querySelector('svg')).toHaveStyle({ color: project.color }))
  })

  it('applies a related issue suggestion', async () => {
    const user = userEvent.setup()
    const issue = triageIssue({ suggestionsGeneratedAt: GENERATED })
    const other = makeIssue({ id: 'issue-other', identifier: 'TST-3', title: 'Import your data' })
    const relatedSuggestion = suggestion({ id: 'suggestion-3', type: 'relatedIssue', suggestedIssueId: other.id })
    respond(issue.id, [relatedSuggestion])

    renderCard(<TriageIntelligenceSuggestions issue={issue} data={bootstrap([issue, other])} />)

    expect(await screen.findByText('Related to')).toBeInTheDocument()
    expect(screen.getByText('TST-3')).toBeInTheDocument()
    expect(screen.getByText('Import your data')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Apply related issue TST-3' }))
    expect(mocks.acceptIssueSuggestion).toHaveBeenCalledWith(issue.id, relatedSuggestion.id)
  })

  it("marks the current issue with another relation from the suggestion's menu, with Linear's hover card", async () => {
    const user = userEvent.setup()
    const issue = triageIssue({ suggestionsGeneratedAt: GENERATED })
    const other = makeIssue({ id: 'issue-other', identifier: 'TST-3', title: 'Import your data', priorityLabel: 'No priority', assignee: undefined })
    const relatedSuggestion = suggestion({ id: 'suggestion-3', type: 'relatedIssue', suggestedIssueId: other.id, metadata: { reasons: ['Both mention imports.'] } })
    respond(issue.id, [relatedSuggestion])
    mocks.createRelation.mockResolvedValue({})
    mocks.fetchIssueRecord.mockResolvedValue(issue)
    renderCard(<TriageIntelligenceSuggestions issue={issue} data={bootstrap([issue, other])} />)

    await user.hover(await screen.findByText('Import your data'))
    const card = await screen.findByRole('dialog', { name: 'Why this looks related' })
    expect(within(card).getByText('Both mention imports.')).toBeInTheDocument()
    expect(within(card).getByText('Unassigned')).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Accept related suggestion' })).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Dismiss suggestion' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Suggestion options' }))
    expect(await screen.findByText('Mark current issue as')).toBeInTheDocument()
    await user.click(screen.getByRole('menuitem', { name: 'Blocked by' }))
    await waitFor(() => expect(mocks.createRelation).toHaveBeenCalledWith(issue.id, 'blocked_by', other.id))
    expect(mocks.dismissIssueSuggestion).toHaveBeenCalledWith(issue.id, relatedSuggestion.id)
  })

  it('shows the pending shimmer while suggestions are being generated', async () => {
    const issue = triageIssue()
    respond(issue.id, [], {})

    renderCard(<TriageIntelligenceSuggestions issue={issue} data={bootstrap([issue])} />)

    expect(await screen.findByRole('status')).toHaveTextContent('Finding suggestions…')
    expect(screen.getByRole('region', { name: 'Triage Intelligence' })).toHaveAttribute('data-state', 'pending')
    expect(screen.queryByText('No suggestions found')).not.toBeInTheDocument()
  })

  it('shows the empty state and runs again', async () => {
    const user = userEvent.setup()
    const issue = triageIssue()
    respond(issue.id, [])

    renderCard(<TriageIntelligenceSuggestions issue={issue} data={bootstrap([issue])} />)

    expect(await screen.findByText('No suggestions found')).toBeInTheDocument()
    const assignee = suggestion({ suggestedUserId: viewer.id })
    respond(issue.id, [assignee])
    await user.click(screen.getByRole('button', { name: 'Run again' }))
    expect(mocks.refreshIssueSuggestions).toHaveBeenCalledWith(issue.id)
    expect(await screen.findByRole('button', { name: 'Assign to user: Viewer' })).toBeInTheDocument()
    expect(screen.queryByText('No suggestions found')).not.toBeInTheDocument()
  })

  it('offers Run again, Dismiss all and Show thinking from the card menu', async () => {
    const user = userEvent.setup()
    const issue = triageIssue({ suggestionsGeneratedAt: GENERATED })
    const assignee = suggestion({ suggestedUserId: viewer.id, metadata: { rank: 1, source: 'ai', reasons: ['Owns the importer.'] } })
    const projectSuggestion = suggestion({ id: 'suggestion-2', type: 'project', suggestedProjectId: project.id, metadata: { rank: 2 } })
    respond(issue.id, [assignee, projectSuggestion], { suggestionsGeneratedAt: GENERATED, thinking: 'Compared the issue against recent importer work.' })

    renderCard(<TriageIntelligenceSuggestions issue={issue} data={bootstrap([issue])} />)
    await screen.findByRole('button', { name: 'Assign to user: Viewer' })

    await user.click(screen.getByRole('button', { name: 'Triage Intelligence options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Show thinking…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Triage Intelligence thinking' })
    expect(within(dialog).getByText('Compared the issue against recent importer work.')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Close' }))

    await user.click(screen.getByRole('button', { name: 'Triage Intelligence options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Run again' }))
    await waitFor(() => expect(mocks.refreshIssueSuggestions).toHaveBeenCalledWith(issue.id))

    await user.click(screen.getByRole('button', { name: 'Triage Intelligence options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Dismiss all suggestions' }))
    await waitFor(() => expect(mocks.dismissIssueSuggestion).toHaveBeenCalledTimes(2))
    expect(await screen.findByText('No suggestions found')).toBeInTheDocument()
  })

  it('falls back to the combined reasons when no thinking was recorded', async () => {
    const user = userEvent.setup()
    const issue = triageIssue({ suggestionsGeneratedAt: GENERATED })
    respond(issue.id, [suggestion({ suggestedUserId: viewer.id, metadata: { rank: 1, reasons: ['Owns the importer.'] } })])

    renderCard(<TriageIntelligenceSuggestions issue={issue} data={bootstrap([issue])} />)
    await screen.findByRole('button', { name: 'Assign to user: Viewer' })
    await user.click(screen.getByRole('button', { name: 'Triage Intelligence options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Show thinking…' }))
    expect(within(await screen.findByRole('dialog')).getByText('Owns the importer.')).toBeInTheDocument()
  })

  it('renders nothing for an issue outside triage without suggestions', () => {
    const issue = makeIssue()
    const { container } = renderCard(<TriageIntelligenceSuggestions issue={issue} data={bootstrap([issue])} />)
    expect(container).toBeEmptyDOMElement()
    expect(mocks.fetchIssueSuggestions).not.toHaveBeenCalled()
  })

  it('keeps showing active suggestions on an accepted issue', async () => {
    const issue = makeIssue({ id: 'issue-triage', triagedAt: GENERATED, suggestionsGeneratedAt: GENERATED })
    respond(issue.id, [suggestion({ suggestedUserId: viewer.id })])

    renderCard(<TriageIntelligenceSuggestions issue={issue} data={bootstrap([issue])} />)
    expect(await screen.findByRole('button', { name: 'Assign to user: Viewer' })).toBeInTheDocument()
  })
})

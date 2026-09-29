import type { ComponentProps } from 'react'
import { fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { backlog, makeBootstrap, makeIssue, started } from '@/test/fixtures'
import type { BootstrapData, Issue } from '@/types/flow'
import { TriageHeaderActions } from './triage-actions'
import { useTriageShortcuts } from './use-triage-shortcuts'
import { DetailPane } from '@/components/detail/detail-pane'

const api = vi.hoisted(() => ({
  updateIssue: vi.fn(),
  createComment: vi.fn(),
  createRelation: vi.fn(),
  fetchIssueSuggestions: vi.fn(),
  fetchIssueRecord: vi.fn(),
  refreshIssueSuggestions: vi.fn(),
  acceptIssueSuggestion: vi.fn(),
  dismissIssueSuggestion: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<object>()), ...api }))
vi.mock('@/components/issue/issue-description-editor', () => ({ IssueDescriptionEditor: () => <div /> }))
vi.mock('@/components/editor/composer', () => ({ Composer: () => <div /> }))

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }

const canceled = { id: 'state-canceled', name: 'Canceled', color: '#999', type: 'canceled', position: 3 } as Issue['state']

function setup(issueOverrides: Partial<Issue> = {}, dataOverrides: Partial<BootstrapData> = {}) {
  const issue = makeIssue({ state: backlog, triagedAt: undefined, ...issueOverrides })
  const data = makeBootstrap({
    issues: [issue],
    states: [backlog, started, canceled],
    teamSettings: { 'team-1': { triageEnabled: true } } as never,
    ...dataOverrides,
  })
  return { issue, data }
}

function wrap(node: React.ReactNode) {
  return <MemoryRouter><I18nProvider><TooltipProvider>{node}</TooltipProvider></I18nProvider></MemoryRouter>
}

describe('triage shortcuts (Linear mapping)', () => {
  it('maps 1 Accept, 2 Decline, 3 and M M Duplicate, H Snooze', () => {
    const onAction = vi.fn()
    renderHook(() => useTriageShortcuts(true, onAction))
    for (const key of ['1', '2', '3', 'h']) fireEvent.keyDown(document.body, { key })
    fireEvent.keyDown(document.body, { key: 'm' })
    fireEvent.keyDown(document.body, { key: 'm' })
    expect(onAction.mock.calls.map(call => call[0])).toEqual(['accept', 'decline', 'duplicate', 'snooze', 'duplicate'])
  })

  it('ignores typing, modifiers, a single M and disabled hosts', () => {
    const onAction = vi.fn()
    const { rerender } = renderHook(({ enabled }) => useTriageShortcuts(enabled, onAction), { initialProps: { enabled: true } })
    const input = document.createElement('input')
    document.body.append(input)
    fireEvent.keyDown(input, { key: '2' })
    fireEvent.keyDown(document.body, { key: '2', metaKey: true })
    fireEvent.keyDown(document.body, { key: 'H', shiftKey: true })
    fireEvent.keyDown(document.body, { key: 'm' })
    rerender({ enabled: false })
    fireEvent.keyDown(document.body, { key: '1' })
    expect(onAction).not.toHaveBeenCalled()
    input.remove()
  })
})

describe('TriageHeaderActions', () => {
  beforeEach(() => {
    globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver
    vi.clearAllMocks()
    api.updateIssue.mockImplementation(async (id: string, input: object) => makeIssue({ id, ...input, triagedAt: '2026-09-28T00:00:00.000Z' }))
    api.createComment.mockResolvedValue({})
  })

  it('renders Accept, Decline, duplicate and snooze controls with shortcut hints', () => {
    const { issue, data } = setup()
    render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={vi.fn()} />))
    expect(screen.getByRole('button', { name: 'Accept' })).toHaveAttribute('aria-keyshortcuts', '1')
    expect(screen.getByRole('button', { name: 'Decline' })).toHaveAttribute('aria-keyshortcuts', '2')
    expect(screen.getByRole('button', { name: 'Mark as duplicate' })).toHaveAttribute('aria-keyshortcuts', '3')
    expect(screen.getByRole('button', { name: 'Snooze' })).toHaveAttribute('aria-keyshortcuts', 'H')
  })

  it('opens the matching action from the keyboard', async () => {
    const { issue, data } = setup()
    render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={vi.fn()} />))
    fireEvent.keyDown(document.body, { key: '2' })
    expect(await screen.findByRole('textbox', { name: 'Comment for declining issue' })).toBeInTheDocument()
    fireEvent.keyDown(document.body, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Comment for declining issue' })).not.toBeInTheDocument())
    fireEvent.keyDown(document.body, { key: 'h' })
    expect(await screen.findByRole('menu', { name: 'Snooze until' })).toBeInTheDocument()
  })

  it('accepts into the team default status with an optional comment', async () => {
    const user = userEvent.setup()
    const onDone = vi.fn()
    const { issue, data } = setup()
    render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={onDone} />))
    await user.click(screen.getByRole('button', { name: 'Accept' }))
    await user.type(await screen.findByRole('textbox', { name: 'Comment for accepting issue' }), 'Looks good')
    await user.click(screen.getByRole('button', { name: 'Accept issue' }))
    await waitFor(() => expect(api.updateIssue).toHaveBeenCalledWith(issue.id, { stateId: started.id, expectedVersion: issue.version }))
    expect(api.createComment).toHaveBeenCalledWith(issue.id, 'Looks good')
    await waitFor(() => expect(onDone).toHaveBeenCalled())
  })

  it('requires a priority when the team asks for one', async () => {
    const user = userEvent.setup()
    const { issue, data } = setup({ priority: 0 }, { teamSettings: { 'team-1': { triageEnabled: true, triageRequirePriority: true } } as never })
    render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={vi.fn()} />))
    await user.click(screen.getByRole('button', { name: 'Accept' }))
    expect(await screen.findByText('Set a priority before moving this issue out of triage.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Accept issue' })).toBeDisabled()
  })

  it('declines into the canceled status', async () => {
    const user = userEvent.setup()
    const { issue, data } = setup()
    render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={vi.fn()} />))
    await user.click(screen.getByRole('button', { name: 'Decline' }))
    await user.click(await screen.findByRole('button', { name: 'Decline issue' }))
    await waitFor(() => expect(api.updateIssue).toHaveBeenCalledWith(issue.id, { stateId: canceled.id, expectedVersion: issue.version }))
  })
})

describe('issue page in triage', () => {
  beforeEach(() => {
    globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver
  })

  function renderPane(issueOverrides: Partial<Issue>) {
    const { issue, data } = setup(issueOverrides, { cycleSettings: {}, documents: [], members: [], drafts: [], reviews: [], favorites: [], customers: [], teamMembers: [], userSettings: {}, projectTemplates: [], agentSkills: [], customerRequests: [], issueSlas: [], slaRules: [] } as never)
    data.workspaceSettings = { ...data.workspaceSettings, featureFlags: { releases: false, 'customer-requests': false, 'triage-intelligence': false } }
    const props = { issue, data, comments: [], activities: [], onClose: vi.fn(), onUpdate: vi.fn(), onDelete: vi.fn(), onCreateSubIssue: vi.fn(), onReactIssue: vi.fn(), onComment: vi.fn(), onEditComment: vi.fn(), onDeleteComment: vi.fn(), onReactComment: vi.fn(), onRelation: vi.fn(), onDeleteRelation: vi.fn(), onUpload: vi.fn(), onDeleteAttachment: vi.fn() } as ComponentProps<typeof DetailPane>
    return render(wrap(<DetailPane {...props} />))
  }

  it('shows Triage status and header actions while the issue is in triage', () => {
    renderPane({})
    expect(screen.getByRole('group', { name: 'Triage actions' }).closest('.issue-header')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Accept' })).toBeInTheDocument()
    expect(screen.getAllByLabelText(/Current status is Triage/).length).toBeGreaterThan(0)
  })

  it('hides triage actions once the issue has been triaged', () => {
    renderPane({ state: started, triagedAt: '2026-09-28T00:00:00.000Z' })
    expect(screen.queryByRole('group', { name: 'Triage actions' })).toBeNull()
    expect(screen.queryByLabelText(/Current status is Triage/)).toBeNull()
  })
})

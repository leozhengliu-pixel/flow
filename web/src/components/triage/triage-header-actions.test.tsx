import type { ComponentProps } from 'react'
import { fireEvent, render, renderHook, screen, waitFor, within } from '@testing-library/react'
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
  listIssueRecords: vi.fn(),
  fetchIssueRecord: vi.fn(),
  refreshIssueSuggestions: vi.fn(),
  acceptIssueSuggestion: vi.fn(),
  dismissIssueSuggestion: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<object>()), ...api }))
// The detail pane's description editor is replaced by a plain field; the comment field of the dialogs is covered with the real editor in triage-action-dialogs.test.tsx.
vi.mock('@/components/issue/issue-description-editor', () => ({ IssueDescriptionEditor: ({ ariaLabel, value, onChange }: { ariaLabel?: string; value: string; onChange?: (snapshot: { markdown: string }) => void }) => <textarea aria-label={ariaLabel} value={value} onChange={event => onChange?.({ markdown: event.target.value })}/> }))
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
    api.createRelation.mockResolvedValue({})
    api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  })

  it("renders Linear's four round icon buttons with shortcut hints", () => {
    const { issue, data } = setup()
    render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={vi.fn()} />))
    expect(screen.getByRole('button', { name: 'Accept issue from triage' })).toHaveAttribute('aria-keyshortcuts', '1')
    expect(screen.getByRole('button', { name: 'Decline triage issue' })).toHaveAttribute('aria-keyshortcuts', '2')
    expect(screen.getByRole('button', { name: 'Mark triage issue as duplicate' })).toHaveAttribute('aria-keyshortcuts', '3')
    expect(screen.getByRole('button', { name: 'Snooze triage issue' })).toHaveAttribute('aria-keyshortcuts', 'H')
    expect(screen.queryByRole('button', { name: 'Unsnooze' })).toBeNull()
  })

  it('opens the matching dialog from the keyboard', async () => {
    const { issue, data } = setup()
    render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={vi.fn()} />))
    fireEvent.keyDown(document.body, { key: '2' })
    expect(await screen.findByRole('textbox', { name: 'Comment for declining issue' })).toBeInTheDocument()
    fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Comment for declining issue' })).not.toBeInTheDocument())
    fireEvent.keyDown(document.body, { key: 'h' })
    expect(await screen.findByPlaceholderText('Try: 4 pm, 2 days, in 5 weeks…')).toBeInTheDocument()
    const labels = screen.getAllByRole('option').map(option => option.querySelector('.command-option-label')?.textContent)
    expect(labels).toEqual(['An hour from now', 'Tomorrow', 'Next week', 'A month from now', 'Custom…'])
  })

  it('accepts into the team default status with an optional comment', async () => {
    const user = userEvent.setup()
    const onDone = vi.fn()
    const { issue, data } = setup()
    render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={onDone} />))
    await user.click(screen.getByRole('button', { name: 'Accept issue from triage' }))
    const dialog = await screen.findByRole('dialog', { name: 'Accept issue…' })
    await user.type(within(dialog).getByRole('textbox', { name: 'Comment for accepting issue' }), 'Looks good')
    await user.click(within(dialog).getByRole('button', { name: 'Accept' }))
    await waitFor(() => expect(api.updateIssue).toHaveBeenCalledWith(issue.id, { stateId: started.id, expectedVersion: issue.version }))
    expect(api.createComment).toHaveBeenCalledWith(issue.id, 'Looks good')
    await waitFor(() => expect(onDone).toHaveBeenCalled())
  })

  it('requires a priority when the team asks for one', async () => {
    const user = userEvent.setup()
    const { issue, data } = setup({ priority: 0 }, { teamSettings: { 'team-1': { triageEnabled: true, triageRequirePriority: true } } as never })
    render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={vi.fn()} />))
    await user.click(screen.getByRole('button', { name: 'Accept issue from triage' }))
    expect(await screen.findByText('Set a priority before moving this issue out of triage.')).toBeInTheDocument()
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Accept' })).toBeDisabled()
  })

  it('declines into the canceled status', async () => {
    const user = userEvent.setup()
    const { issue, data } = setup()
    render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={vi.fn()} />))
    await user.click(screen.getByRole('button', { name: 'Decline triage issue' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Decline issue…' })).getByRole('button', { name: 'Decline' }))
    await waitFor(() => expect(api.updateIssue).toHaveBeenCalledWith(issue.id, { stateId: canceled.id, expectedVersion: issue.version }))
  })

  it('explains, instead of silently doing nothing, when the team has no canceled status', async () => {
    const user = userEvent.setup()
    const { issue, data } = setup({}, { states: [backlog, started] })
    render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={vi.fn()} />))
    await user.click(screen.getByRole('button', { name: 'Mark triage issue as duplicate' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('no canceled status')
    await user.keyboard('{Escape}')
    await user.click(screen.getByRole('button', { name: 'Decline triage issue' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('no canceled status')
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Decline' })).toBeDisabled()
  })

  it('marks a duplicate picked from the server search', async () => {
    const user = userEvent.setup()
    const target = makeIssue({ id: 'remote', identifier: 'TST-42', title: 'Remote original', state: started })
    api.listIssueRecords.mockResolvedValue({ items: [target], hasMore: false, total: 1 })
    const { issue, data } = setup()
    render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={vi.fn()} />))
    fireEvent.keyDown(document.body, { key: '3' })
    const input = await screen.findByPlaceholderText('Search for issue to mark as duplicate of…')
    await user.type(input, 'original')
    await waitFor(() => expect(api.listIssueRecords).toHaveBeenCalledWith(expect.objectContaining({ q: 'original' }), expect.anything()))
    await user.click(await screen.findByRole('option', { name: /TST-42/ }))
    await waitFor(() => expect(api.createRelation).toHaveBeenCalledWith(issue.id, 'duplicate', 'remote'))
    expect(api.updateIssue).toHaveBeenCalledWith(issue.id, { stateId: canceled.id, expectedVersion: issue.version })
  })

  it('snoozes with natural language and unsnoozes a snoozed issue', async () => {
    const user = userEvent.setup()
    const { issue, data } = setup()
    const { unmount } = render(wrap(<TriageHeaderActions issue={issue} data={data} onDone={vi.fn()} />))
    fireEvent.keyDown(document.body, { key: 'h' })
    await user.type(await screen.findByPlaceholderText('Try: 4 pm, 2 days, in 5 weeks…'), 'in 2 days')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(api.updateIssue).toHaveBeenCalledWith(issue.id, { snoozedUntil: expect.any(String) }))
    const until = Date.parse(api.updateIssue.mock.calls[0][1].snoozedUntil)
    expect(Math.round((until - Date.now()) / 86_400_000)).toBe(2)
    unmount()
    const snoozed = { ...issue, snoozedUntil: new Date(Date.now() + 86_400_000).toISOString() }
    render(wrap(<TriageHeaderActions issue={snoozed} data={data} onDone={vi.fn()} />))
    await user.click(screen.getByRole('button', { name: 'Unsnooze' }))
    await waitFor(() => expect(api.updateIssue).toHaveBeenLastCalledWith(issue.id, { snoozedUntil: '' }))
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
    expect(screen.getByRole('button', { name: 'Accept issue from triage' })).toBeInTheDocument()
    expect(screen.getAllByLabelText(/Current status is Triage/).length).toBeGreaterThan(0)
  })

  it('hides triage actions once the issue has been triaged', () => {
    renderPane({ state: started, triagedAt: '2026-09-28T00:00:00.000Z' })
    expect(screen.queryByRole('group', { name: 'Triage actions' })).toBeNull()
    expect(screen.queryByLabelText(/Current status is Triage/)).toBeNull()
  })
})

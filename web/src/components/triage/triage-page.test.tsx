import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { backlog, makeBootstrap, makeIssue, started, teammate } from '@/test/fixtures'
import type { BootstrapData, Issue, WorkflowState } from '@/types/flow'
import { TriagePage, type TriagePageProps } from './triage-page'
import { readTriageDisplay, triageDisplayKey, triageState } from './triage-model'

const api = vi.hoisted(() => ({
  listIssueRecords: vi.fn(),
  updateIssue: vi.fn(),
  createRelation: vi.fn(),
  createComment: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<object>()), ...api }))

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
const canceled = { id: 'state-canceled', name: 'Canceled', color: '#999', type: 'canceled', position: 3 } as WorkflowState
const day = 86_400_000

function triageIssue(number: number, overrides: Partial<Issue> = {}) {
  return makeIssue({
    id: `issue-${number}`, identifier: `TST-${number}`, number, title: `Triage issue ${number}`, state: backlog, triagedAt: undefined,
    creator: teammate, statusChangedAt: new Date(Date.now() - number * day).toISOString(), createdAt: new Date(Date.now() - number * day).toISOString(), ...overrides,
  })
}

function setup(issues: Issue[], overrides: Partial<BootstrapData> = {}) {
  return makeBootstrap({
    issues: [...issues, makeIssue({ id: 'accepted', identifier: 'TST-99', title: 'Already accepted', state: started })],
    states: [backlog, started, canceled],
    teamSettings: { 'team-1': { triageEnabled: true } } as never,
    favorites: [],
    teamMembers: [],
    ...overrides,
  })
}

function renderPage(props: Partial<TriagePageProps> & { data: BootstrapData }) {
  const team = props.data.teams[0]
  return render(<MemoryRouter><I18nProvider><TooltipProvider><TriagePage team={team} renderIssue={issue => <div data-testid="issue-view">{issue.identifier} view</div>} {...props}/></TooltipProvider></I18nProvider></MemoryRouter>)
}

describe('TriagePage (Linear)', () => {
  beforeEach(() => {
    globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver
    localStorage.clear()
    vi.clearAllMocks()
    api.updateIssue.mockImplementation(async (id: string, input: object) => makeIssue({ id, ...input }))
    api.createRelation.mockResolvedValue({})
    api.createComment.mockResolvedValue({})
    api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  })
  afterEach(() => localStorage.clear())

  it('renders Linear two-line rows: title and identifier, creator and when it was added', () => {
    renderPage({ data: setup([triageIssue(1, { dueDate: '2026-12-01' }), triageIssue(2)]) })
    const rows = screen.getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText('Triage issue 1')).toBeInTheDocument()
    expect(within(rows[0]).getByText('TST-1')).toBeInTheDocument()
    expect(within(rows[0]).getByText('Teammate')).toBeInTheDocument()
    expect(within(rows[0]).getByText('1d ago')).toBeInTheDocument()
    expect(within(rows[0]).getByText('Dec 1')).toBeInTheDocument()
    expect(rows[0]).toHaveAttribute('href', '/workspace/issue/TST-1/triage-issue-1')
    // No group header, no count in the header, and the accepted issue is not listed.
    expect(screen.queryByText('Already accepted')).toBeNull()
    expect(screen.getByRole('heading', { name: 'Triage' }).parentElement?.textContent).toBe('Triage')
    expect(screen.getByText('2 issues to triage')).toBeInTheDocument()
  })

  it('persists display options per team and user and orders by them', async () => {
    const user = userEvent.setup()
    const data = setup([triageIssue(1, { priority: 4 }), triageIssue(2, { priority: 1 }), triageIssue(3, { priority: 0 })])
    renderPage({ data })
    const titles = () => screen.getAllByRole('listitem').map(row => within(row).getAllByText(/^Triage issue/)[0].textContent)
    expect(titles()).toEqual(['Triage issue 1', 'Triage issue 2', 'Triage issue 3'])
    await user.click(screen.getByRole('button', { name: 'Display options' }))
    const menu = await screen.findByRole('dialog', { name: 'Display options' })
    await user.click(within(menu).getByRole('button', { name: 'Newest first' }))
    expect(titles()).toEqual(['Triage issue 3', 'Triage issue 2', 'Triage issue 1'])
    await user.click(within(menu).getByRole('button', { name: 'ID' }))
    expect(screen.queryByText('TST-1')).toBeNull()
    const key = triageDisplayKey(data.workspace.id, data.viewer.id, 'team-1')
    expect(readTriageDisplay(key)).toMatchObject({ newestFirst: false, properties: ['dueDate'] })
    await user.click(within(menu).getByRole('combobox', { name: 'Ordering' }))
    await user.click(await screen.findByRole('option', { name: 'Priority' }))
    // Oldest first reverses the natural (urgent first) priority order.
    expect(titles()).toEqual(['Triage issue 3', 'Triage issue 1', 'Triage issue 2'])
    expect(readTriageDisplay(key).ordering).toBe('priority')
  })

  it('hides snoozed issues until Show snoozed is on', async () => {
    const user = userEvent.setup()
    renderPage({ data: setup([triageIssue(1), triageIssue(2, { snoozedUntil: new Date(Date.now() + day).toISOString() })]) })
    expect(screen.getAllByRole('listitem')).toHaveLength(1)
    await user.click(screen.getByRole('button', { name: 'Display options' }))
    await user.click(await screen.findByRole('checkbox', { name: 'Show snoozed' }))
    expect(screen.getAllByRole('listitem')).toHaveLength(2)
  })

  it('moves with J/K (selecting the first issue first) and returns with Escape', async () => {
    const onSelectIssue = vi.fn()
    const data = setup([triageIssue(1), triageIssue(2)])
    const { rerender } = renderPage({ data, onSelectIssue })
    fireEvent.keyDown(document.body, { key: 'j' })
    expect(onSelectIssue).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'issue-1' }), { replace: false, sequence: ['issue-1', 'issue-2'] })
    rerender(<MemoryRouter><I18nProvider><TooltipProvider><TriagePage data={data} team={data.teams[0]} onSelectIssue={onSelectIssue} selectedIdentifier="TST-1" detail={<div data-testid="issue-view">detail</div>}/></TooltipProvider></I18nProvider></MemoryRouter>)
    expect(screen.getByTestId('issue-view')).toBeInTheDocument()
    expect(screen.getAllByRole('listitem')[0]).toHaveAttribute('aria-current', 'page')
    fireEvent.keyDown(document.body, { key: 'ArrowDown' })
    expect(onSelectIssue).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'issue-2' }), expect.objectContaining({ replace: true }))
    rerender(<MemoryRouter><I18nProvider><TooltipProvider><TriagePage data={data} team={data.teams[0]} onSelectIssue={onSelectIssue} selectedIdentifier="TST-2" detail={<div data-testid="issue-view">detail</div>}/></TooltipProvider></I18nProvider></MemoryRouter>)
    fireEvent.keyDown(document.body, { key: 'k' })
    expect(onSelectIssue).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'issue-1' }), expect.objectContaining({ replace: true }))
    fireEvent.keyDown(document.body, { key: 'Escape' })
    expect(onSelectIssue).toHaveBeenLastCalledWith(null, expect.anything())
  })

  it('selects the next issue once the selected one leaves triage', () => {
    const onSelectIssue = vi.fn()
    const first = triageIssue(1), second = triageIssue(2)
    const data = setup([first, second])
    const view = (next: BootstrapData) => <MemoryRouter><I18nProvider><TooltipProvider><TriagePage data={next} team={next.teams[0]} onSelectIssue={onSelectIssue} selectedIdentifier="TST-1" detail={<div/>}/></TooltipProvider></I18nProvider></MemoryRouter>
    const { rerender } = render(view(data))
    rerender(view({ ...data, issues: data.issues.map(issue => issue.id === first.id ? { ...issue, state: started, triagedAt: new Date().toISOString() } : issue) }))
    expect(onSelectIssue).toHaveBeenLastCalledWith(expect.objectContaining({ id: second.id }), expect.objectContaining({ replace: true }))
  })

  it('offers Accept / Decline / Mark as duplicate / Snooze / Copy URL in the row menu', async () => {
    const user = userEvent.setup()
    const onReload = vi.fn()
    renderPage({ data: setup([triageIssue(1)]), onReload })
    fireEvent.contextMenu(screen.getByRole('listitem'))
    const menu = await screen.findByRole('menu')
    expect(within(menu).getAllByRole('menuitem').map(item => item.textContent)).toEqual(['Accept…1', 'Decline…2', 'Mark as duplicate…3', 'SnoozeH', 'Copy URL⌘⇧,'])
    await user.click(within(menu).getByRole('menuitem', { name: /Decline/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Decline issue…' })
    expect(within(dialog).getByText('TST-1 · Triage issue 1')).toBeInTheDocument()
    await user.type(within(dialog).getByRole('textbox', { name: 'Comment for declining issue' }), 'Not for us')
    await user.click(within(dialog).getByRole('button', { name: 'Decline' }))
    await waitFor(() => expect(api.updateIssue).toHaveBeenCalledWith('issue-1', { stateId: canceled.id, expectedVersion: undefined }))
    expect(api.createComment).toHaveBeenCalledWith('issue-1', 'Not for us')
    await waitFor(() => expect(onReload).toHaveBeenCalled())
  })

  it('snoozes from the row menu presets', async () => {
    const user = userEvent.setup()
    renderPage({ data: setup([triageIssue(1)]) })
    fireEvent.contextMenu(screen.getByRole('listitem'))
    await user.click(await screen.findByRole('menuitem', { name: /Snooze/ }))
    const preset = await screen.findByRole('menuitem', { name: /An hour from now/ })
    expect(screen.getByRole('menuitem', { name: /Custom…/ })).toBeInTheDocument()
    fireEvent.click(preset)
    await waitFor(() => expect(api.updateIssue).toHaveBeenCalledWith('issue-1', { snoozedUntil: expect.any(String) }))
    const until = Date.parse(api.updateIssue.mock.calls[0][1].snoozedUntil)
    expect(until - Date.now()).toBeGreaterThan(55 * 60_000)
  })

  it('opens the create dialog from the empty state', async () => {
    const user = userEvent.setup()
    const onCreateIssue = vi.fn()
    renderPage({ data: setup([]), onCreateIssue })
    expect(screen.getByText('No issues to triage')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Create triage issue' }))
    expect(onCreateIssue).toHaveBeenCalled()
  })

  it('files created issues in the team triage status', () => {
    const teamBacklog = { ...backlog, id: 'team-backlog', teamId: 'team-1' } as WorkflowState
    expect(triageState([started, teamBacklog], 'team-1')?.id).toBe('team-backlog')
    expect(triageState([started, backlog, teamBacklog], 'team-1', 'team-backlog')?.id).toBe('team-backlog')
    expect(triageState([started], 'team-1')).toBeUndefined()
  })

  it('shows the triage-disabled state instead of listing backlog issues', async () => {
    const user = userEvent.setup()
    const onNavigate = vi.fn()
    renderPage({ data: setup([triageIssue(1)], { teamSettings: { 'team-1': { triageEnabled: false } } as never }), onNavigate })
    expect(screen.getByText('Triage is turned off')).toBeInTheDocument()
    expect(screen.queryByRole('listitem')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Go to triage settings' }))
    expect(onNavigate).toHaveBeenCalledWith('/workspace/settings/teams/TST/triage')
    expect(api.listIssueRecords).not.toHaveBeenCalled()
  })

  it('loads triage from the server with a skeleton in paged workspaces', async () => {
    let resolve: (value: unknown) => void = () => undefined
    api.listIssueRecords.mockReturnValue(new Promise(done => { resolve = done }))
    renderPage({ data: setup([], { issueCollectionPaged: true } as never) })
    expect(screen.getByRole('status', { name: 'Loading triage' })).toBeInTheDocument()
    expect(api.listIssueRecords.mock.calls[0][0]).toMatchObject({ teamId: 'team-1', archived: 'false', filter: { and: [{ field: 'status', operator: 'in', values: ['backlog'] }, { field: 'triagedAt', operator: 'isEmpty' }] } })
    resolve({ items: [triageIssue(5)], hasMore: false, total: 1 })
    expect(await screen.findByText('Triage issue 5')).toBeInTheDocument()
    expect(screen.queryByRole('status', { name: 'Loading triage' })).toBeNull()
  })

  it('opens Add filter with F, without a Status filter', async () => {
    renderPage({ data: setup([triageIssue(1)]) })
    fireEvent.keyDown(document.body, { key: 'f' })
    expect(await screen.findByPlaceholderText(/Add Filter/i)).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /^Status/ })).toBeNull()
    expect(screen.getByRole('option', { name: /Priority/ })).toBeInTheDocument()
  })
})

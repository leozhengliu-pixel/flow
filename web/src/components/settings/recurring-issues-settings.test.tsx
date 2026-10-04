import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { toDateInput } from '@/lib/recurrence'
import { backlog, makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import type { BootstrapData, Issue } from '@/types/flow'

const api = vi.hoisted(() => ({
  listRecurringIssues: vi.fn(),
  createRecurringIssue: vi.fn(),
  updateIssue: vi.fn(),
  fetchIssueRecord: vi.fn(),
}))
vi.mock('@/lib/api', async (importOriginal) => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { RecurringIssuesSettingsPage } from './recurring-issues-settings'
import { TeamWorkflowSettings } from './team-workflow-settings'

const team = makeIssue().team
function bootstrap(overrides: Partial<BootstrapData> = {}) {
  return makeBootstrap({
    viewerRole: 'admin',
    teamMembers: [{ teamId: team.id, userId: viewer.id, role: 'member' }],
    teamSettings: { [team.id]: { teamId: team.id, timezone: 'Etc/UTC', triageEnabled: true, templatePermission: 'allMembers', defaultStateId: 'state-started' } },
    issueCollectionRevision: 1,
    ...overrides,
  } as Partial<BootstrapData>)
}
function renderPage(data: BootstrapData, props: Partial<React.ComponentProps<typeof RecurringIssuesSettingsPage>> = {}) {
  const onNavigateSubPath = vi.fn()
  const view = render(<MemoryRouter><I18nProvider><RecurringIssuesSettingsPage data={data} team={team} onBack={vi.fn()} onNavigateSubPath={onNavigateSubPath} onReload={vi.fn().mockResolvedValue(undefined)} {...props}/></I18nProvider></MemoryRouter>)
  return { ...view, onNavigateSubPath }
}
const plusDays = (days: number) => { const date = new Date(); return toDateInput(new Date(date.getFullYear(), date.getMonth(), date.getDate() + days)) }

describe('recurring issues settings', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    for (const mock of Object.values(api)) mock.mockReset()
  })
  afterEach(() => { vi.unstubAllGlobals(); window.history.replaceState(null, '', '/') })

  it('lists the team\'s schedules from the API, never from the paged client cache', async () => {
    const cached = makeIssue({ id: 'cached', identifier: 'TST-9', title: 'Only in the client cache', recurrence: 'FREQ=WEEKLY', dueDate: plusDays(3) })
    const served = makeIssue({ id: 'served', identifier: 'TST-2', title: 'Weekly sync notes', recurrence: 'FREQ=WEEKLY', dueDate: '2026-10-09', icon: 'Calendar', subIssueCount: 2 })
    api.listRecurringIssues.mockResolvedValue({ issues: [served] })
    renderPage(bootstrap({ issues: [cached], issueCollectionPaged: true } as Partial<BootstrapData>))
    expect(await screen.findByText('Weekly sync notes')).toBeInTheDocument()
    expect(api.listRecurringIssues).toHaveBeenCalledWith(team.id, expect.any(AbortSignal))
    expect(screen.queryByText('Only in the client cache')).not.toBeInTheDocument()
    const row = screen.getByText('Weekly sync notes').closest('.recurring-issue-row') as HTMLElement
    expect(within(row).getByText('TST-2')).toBeInTheDocument()
    expect(within(row).getByText(/Repeats every week/)).toBeInTheDocument()
    expect(within(row).getByText(/Next due Oct 16/)).toBeInTheDocument()
    expect(within(row).getByText(/2 sub-issues/)).toBeInTheDocument()
    expect(within(row).getByText(/Due Oct 9/)).toBeInTheDocument()
  })

  it('shows Linear\'s header, empty card and New recurring issue action', async () => {
    api.listRecurringIssues.mockResolvedValue({ issues: [] })
    const user = userEvent.setup()
    const { onNavigateSubPath } = renderPage(bootstrap())
    expect(screen.getByRole('heading', { name: 'Recurring issues' })).toBeInTheDocument()
    expect(screen.getByText(/A new instance is created after each due date passes/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Docs/ })).toHaveAttribute('target', '_blank')
    expect(await screen.findByText('No recurring issues')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'New recurring issue' }))
    expect(onNavigateSubPath).toHaveBeenCalledWith('new')
  })

  it('stops a schedule from the row menu', async () => {
    const issue = makeIssue({ id: 'served', title: 'Rotate keys', recurrence: 'FREQ=MONTHLY', dueDate: '2026-10-09' })
    // The refetch after stopping no longer lists the issue.
    api.listRecurringIssues.mockResolvedValueOnce({ issues: [issue] }).mockResolvedValue({ issues: [] })
    api.updateIssue.mockResolvedValue({ ...issue, recurrence: undefined })
    const user = userEvent.setup()
    renderPage(bootstrap())
    await screen.findByText('Rotate keys')
    await user.click(screen.getByRole('button', { name: `Recurring issue actions ${issue.identifier}` }))
    expect(await screen.findByRole('menuitem', { name: 'Edit schedule' })).toBeInTheDocument()
    await user.click(screen.getByRole('menuitem', { name: 'Stop recurring' }))
    expect(api.updateIssue).toHaveBeenCalledWith('served', { recurrence: '' })
    await waitFor(() => expect(screen.queryByText('Rotate keys')).not.toBeInTheDocument())
  })

  it('refetches when realtime issue changes reach this team\'s recurring issues', async () => {
    api.listRecurringIssues.mockResolvedValue({ issues: [] })
    const data = bootstrap()
    const view = renderPage(data)
    await waitFor(() => expect(api.listRecurringIssues).toHaveBeenCalledTimes(1))
    const created = makeIssue({ id: 'new-instance', recurrence: 'FREQ=WEEKLY', recurrenceSeriesId: 'series', dueDate: plusDays(7) })
    view.rerender(<MemoryRouter><I18nProvider><RecurringIssuesSettingsPage data={{ ...data, issues: [...data.issues, created] }} team={team} onBack={vi.fn()} onNavigateSubPath={vi.fn()} onReload={vi.fn()}/></I18nProvider></MemoryRouter>)
    await waitFor(() => expect(api.listRecurringIssues).toHaveBeenCalledTimes(2))
    window.dispatchEvent(new CustomEvent('flow-issue-query-invalidated', { detail: { workspaceKey: data.workspace.urlKey, force: true } }))
    await waitFor(() => expect(api.listRecurringIssues).toHaveBeenCalledTimes(3))
  })

  it('creates the first instance with its schedule, first due date and sub-issues', async () => {
    api.createRecurringIssue.mockResolvedValue({ issue: makeIssue(), subIssues: [] })
    const user = userEvent.setup({ delay: null })
    const { onNavigateSubPath } = renderPage(bootstrap(), { subPath: 'new' })
    expect(screen.getByRole('heading', { name: 'New recurring issue' })).toBeInTheDocument()
    expect(screen.getByText(/Create the first instance of your recurring issue below/)).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Change status' })).toHaveTextContent('Triage')
    expect(screen.getByLabelText('First due')).toHaveValue(plusDays(7))
    expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
    await user.type(screen.getByRole('textbox', { name: 'Issue title' }), 'Weekly report')
    fireEvent.change(screen.getByLabelText('First due'), { target: { value: plusDays(10) } })
    await user.click(screen.getByRole('combobox', { name: 'Repeat interval' }))
    await user.click(await screen.findByRole('option', { name: '2' }))
    await user.click(screen.getByRole('combobox', { name: 'Repeat unit' }))
    await user.click(await screen.findByRole('option', { name: 'months' }))
    await user.click(screen.getByRole('button', { name: 'Sub-issues' }))
    const composer = screen.getByText('Create sub-issue').closest('section') as HTMLElement
    expect(within(composer).queryByRole('combobox', { name: 'Change team' })).not.toBeInTheDocument()
    await user.type(within(composer).getByRole('textbox', { name: 'Issue title' }), 'Collect numbers')
    await user.click(within(composer).getByRole('button', { name: 'Add sub-issue' }))
    expect(screen.getByText('Collect numbers')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(api.createRecurringIssue).toHaveBeenCalledWith(team.id, {
      title: 'Weekly report',
      description: '',
      stateId: backlog.id,
      priority: 0,
      labelIds: [],
      dueDate: plusDays(10),
      recurrence: 'FREQ=MONTHLY;INTERVAL=2',
      subIssues: [{ title: 'Collect numbers', priority: 0, labelIds: [] }],
    })
    await waitFor(() => expect(onNavigateSubPath).toHaveBeenCalledWith(undefined))
  }, 15000)

  it('offers sub-issues only the labels the recurring issue\'s team can use', async () => {
    const otherTeam = { ...team, id: 'team-ops', key: 'OPS', name: 'Ops' }
    const data = bootstrap({
      teams: [team, otherTeam],
      labels: [
        { id: 'label-workspace', name: 'Bug', color: '#eb5757', resourceType: 'issue' },
        { id: 'label-own', name: 'OwnTeamOnly', color: '#5e6ad2', resourceType: 'issue', scope: team.id },
        { id: 'label-ops', name: 'OpsOnly', color: '#4cb782', resourceType: 'issue', scope: otherTeam.id },
      ],
      labelGroups: [],
    } as Partial<BootstrapData>)
    const user = userEvent.setup({ delay: null })
    renderPage(data, { subPath: 'new' })
    await user.click(screen.getByRole('button', { name: 'Sub-issues' }))
    const composer = screen.getByText('Create sub-issue').closest('section') as HTMLElement
    await user.click(within(composer).getByRole('combobox', { name: 'Change labels' }))
    expect(await screen.findByRole('option', { name: /OwnTeamOnly/ })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /Bug/ })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /OpsOnly/ })).not.toBeInTheDocument()
  }, 15000)

  it('converts an existing issue (?fromIssue=) by saving its schedule and first due date onto it', async () => {
    const source = makeIssue({ id: 'source', title: 'Pay invoices', dueDate: plusDays(5), priority: 3 })
    api.updateIssue.mockResolvedValue({ ...source, recurrence: 'FREQ=WEEKLY' })
    window.history.replaceState(null, '', '/workspace/settings/teams/TST/recurring-issues/new?fromIssue=source')
    const user = userEvent.setup()
    renderPage(bootstrap({ issues: [source] }))
    expect(screen.getByRole('heading', { name: 'Make recurring' })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Issue title' })).toHaveValue('Pay invoices')
    expect(screen.getByLabelText('First due')).toHaveValue(plusDays(5))
    expect(screen.queryByRole('button', { name: 'Sub-issues' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(api.updateIssue).toHaveBeenCalledWith('source', expect.objectContaining({ title: 'Pay invoices', priority: 3, recurrence: 'FREQ=WEEKLY', dueDate: plusDays(5) }))
    expect(api.createRecurringIssue).not.toHaveBeenCalled()
    await waitFor(() => expect(window.location.search).toBe(''))
  })

  it('routes team settings recurring-issues/new to the full page', async () => {
    api.listRecurringIssues.mockResolvedValue({ issues: [] as Issue[] })
    render(<MemoryRouter><I18nProvider><TeamWorkflowSettings data={bootstrap()} team={team} section="recurring-issues" subPath="new" onNavigate={vi.fn()} onReload={vi.fn()}/></I18nProvider></MemoryRouter>)
    expect(screen.getByRole('heading', { name: 'New recurring issue' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Recurring issues/ })).toBeInTheDocument()
  })
})

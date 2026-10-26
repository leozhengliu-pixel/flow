import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { BootstrapData, Issue, SavedView, Team, WorkflowState } from '@/types/flow'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { issueToExplorerRow } from './issue-explorer-model'
import { SavedViewInsightsPanel } from './saved-view-panels'

vi.mock('@/lib/api', () => ({ listIssueRecords: vi.fn() }))

const parentTeam = { id: 'team-parent', key: 'PAR', name: 'Parent', color: '#5e6ad2' } as Team
const subTeam = { id: 'team-sub', key: 'SUB', name: 'Sub', color: '#26b5ce' } as Team
const parentDone = { id: 'p-done', teamId: parentTeam.id, name: 'Done', type: 'completed', position: 1, color: '#5e6ad2' } as WorkflowState
const parentTodo = { id: 'p-todo', teamId: parentTeam.id, name: 'Todo', type: 'unstarted', position: 0, color: '#e2e2e2' } as WorkflowState
const subDone = { id: 's-done', teamId: subTeam.id, name: 'Done', type: 'completed', position: 1, color: '#00aa55' } as WorkflowState
const subTodo = { id: 's-todo', teamId: subTeam.id, name: 'Todo', type: 'unstarted', position: 0, color: '#cccccc' } as WorkflowState

const issue = (id: string, team: Team, state: WorkflowState, extra: Partial<Issue> = {}) => makeIssue({ id, identifier: `${team.key}-${id}`, title: `Issue ${id}`, team, state, ...extra })
const issues = [
  issue('1', parentTeam, parentDone), issue('2', subTeam, subDone), issue('3', subTeam, subDone),
  issue('4', subTeam, subTodo), issue('5', parentTeam, parentTodo),
  issue('6', subTeam, subDone, { archivedAt: '2026-08-03T00:00:00.000Z' }),
]
const data: BootstrapData = makeBootstrap({
  issues, teams: [parentTeam, subTeam], states: [parentTodo, parentDone, subTodo, subDone],
  teamSettings: { [subTeam.id]: { parentTeamId: parentTeam.id, inheritWorkflowStatuses: false } } as unknown as BootstrapData['teamSettings'],
})
const allRows = issues.map(item => issueToExplorerRow(item, 'workspace', issues, data))
const rows = allRows.filter((_, index) => !issues[index].archivedAt)

function setup(insights: Record<string, unknown>, onDrillChange?: (rows: MyIssuesRowData[] | undefined) => void) {
  const view = { id: 'status-merge', name: 'Parent issues', insights } as unknown as SavedView
  return render(<MemoryRouter><I18nProvider><SavedViewInsightsPanel data={data} allRows={allRows} rows={rows} view={view} onSave={vi.fn(async () => undefined)} onClose={vi.fn()} onDrillChange={onDrillChange}/></I18nProvider></MemoryRouter>)
}
const tableLines = () => within(screen.getByRole('table', { name: 'Insights table' })).getAllByRole('row').map(row => within(row).queryAllByRole('cell').map(cell => cell.textContent)).filter(line => line.length)

beforeEach(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
  localStorage.clear()
  localStorage.setItem('flow:saved-view:status-merge:insights-intro', 'dismissed')
})
afterEach(() => vi.restoreAllMocks())

describe('insights over a team and its sub-teams', () => {
  it('shows one row per status name and type, and drills into every team’s issues in it', () => {
    const onDrillChange = vi.fn()
    setup({ slice: 'status' }, onDrillChange)
    expect(tableLines()).toEqual([['Todo', '2'], ['Done', '3']])
    fireEvent.click(screen.getByRole('button', { name: 'Done, Issue count: 3' }))
    const drilled = onDrillChange.mock.calls.at(-1)![0] as MyIssuesRowData[]
    expect(drilled.map(row => row.id).sort()).toEqual(['1', '2', '3'])
  })

  it('counts archived issues in the merged row only when they are shown', () => {
    setup({ slice: 'status', showArchived: true })
    expect(tableLines()).toEqual([['Todo', '2'], ['Done', '4']])
  })

  it('merges status segment columns too', () => {
    setup({ slice: 'priority', segment: 'status' })
    const headers = within(screen.getByRole('table', { name: 'Insights table' })).getAllByRole('columnheader').map(header => header.textContent)
    expect(headers.filter(header => header === 'Done')).toHaveLength(1)
    expect(headers.filter(header => header === 'Todo')).toHaveLength(1)
  })

  it('exports the merged rows as Markdown and CSV', async () => {
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    setup({ slice: 'status' })
    const user = userEvent.setup()
    // After user-event installs its own clipboard stub.
    const writeText = vi.fn(async () => undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Copy insights as Markdown' }))
    await act(async () => undefined)
    expect(writeText).toHaveBeenCalledWith('| Status | Issue count |\n| --- | --- |\n| Todo | 2 |\n| Done | 3 |')
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Export insights as CSV…' }))
    const link = click.mock.contexts.at(-1) as HTMLAnchorElement
    expect(decodeURIComponent(link.href.split(',').slice(1).join(','))).toBe('﻿"Status","Issue count"\n"Todo","2"\n"Done","3"')
  })
})

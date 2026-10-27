import type { ReactNode } from 'react'
import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { BootstrapData, Issue, Team, WorkflowState } from '@/types/flow'
import { listIssueRecordGroups, listIssueRecords } from '@/lib/api'
import { createIssueDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import { filterChipItem } from '@/components/my-issues/my-issues-filter-chips'
import { issueFiltersToQueryAst, toggleFilterOption, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import { teamStateIdFor } from '@/lib/team-workflow'
import { buildIssueGroups, groupMoveUpdate } from './issue-grouping'
import { applyExplorerFilters, explorerFilterOptions, explorerPropertyOptions, issueToExplorerRow } from './issue-explorer-model'
import { pagedGroupQuery, pagedStatusGroups } from './paged-issue-groups'
import { PagedIssueList } from './paged-issue-list'
import { clearPagedIssueListCache } from './paged-issue-list-cache'

vi.mock('@/lib/api', () => ({ listIssueRecordGroups: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('react-virtuoso', () => {
  return { Virtuoso: () => null, GroupedVirtuoso: ({ groupCounts, groupContent, itemContent }: { groupCounts: number[]; groupContent: (index: number) => ReactNode; itemContent: (index: number, group: number) => ReactNode }) => {
    let offset = 0
    return <div>{groupCounts.map((count, group) => { const base = offset; offset += count; return <section key={group}>{groupContent(group)}{Array.from({ length: Math.min(10, count) }, (_, index) => <div key={index}>{itemContent(base + index, group)}</div>)}</section> })}</div>
  } }
})

afterEach(() => clearPagedIssueListCache())

const parentTeam = { id: 'team-parent', key: 'PAR', name: 'Parent', color: '#5e6ad2' } as Team
const subTeam = { id: 'team-sub', key: 'SUB', name: 'Sub', color: '#26b5ce' } as Team
const state = (teamId: string, id: string, name: string, type: WorkflowState['type'], position: number, color: string) => ({ id, teamId, name, type, position, color }) as WorkflowState
const parentStates = [state(parentTeam.id, 'p-todo', 'Todo', 'unstarted', 0, '#e2e2e2'), state(parentTeam.id, 'p-done', 'Done', 'completed', 1, '#5e6ad2')]
// The sub-team's own workflow: same names and types, its own IDs, plus a canceled "Done" that must stay apart.
const subStates = [state(subTeam.id, 's-todo', 'todo ', 'unstarted', 0, '#cccccc'), state(subTeam.id, 's-done', 'Done', 'completed', 1, '#00aa55'), state(subTeam.id, 's-done-canceled', 'Done', 'canceled', 2, '#999999')]
const byId = new Map([...parentStates, ...subStates].map(item => [item.id, item]))
const issue = (id: string, team: Team, stateId: string) => makeIssue({ id, identifier: `${team.key}-${id}`, title: `Issue ${id}`, team, state: byId.get(stateId)! })
const issues = [issue('1', parentTeam, 'p-done'), issue('2', subTeam, 's-done'), issue('3', subTeam, 's-todo'), issue('4', parentTeam, 'p-todo'), issue('5', subTeam, 's-done-canceled')]

function dataset(list: Issue[] = issues): BootstrapData {
  return makeBootstrap({
    issues: list,
    teams: [parentTeam, subTeam],
    states: [...parentStates, ...subStates],
    teamSettings: { [subTeam.id]: { parentTeamId: parentTeam.id, inheritWorkflowStatuses: false } } as unknown as BootstrapData['teamSettings'],
  })
}
const rowsOf = (data: BootstrapData, list = issues) => list.map(item => issueToExplorerRow(item, 'workspace', list, data))
const statusFilter = (values: string[]): MyIssuesAppliedFilter => ({ id: 'f', field: 'status', fieldLabel: 'Status', operator: 'is', value: values[0], valueLabel: 'Done', values: values.map(value => ({ value, valueLabel: 'Done' })) })

describe('status groups across team workflows in lists', () => {
  it('shows one group per name and type, named and coloured by the parent team', () => {
    const data = dataset()
    const groups = buildIssueGroups(rowsOf(data), createIssueDisplayOptions({ grouping: 'status', completedWindow: 'all' }), { data })
    expect(groups.map(group => [group.id, group.label, group.issues.map(item => item.id).sort()])).toEqual([
      ['p-todo', 'Todo', ['3', '4']], ['p-done', 'Done', ['1', '2']], ['s-done-canceled', 'Done', ['5']],
    ])
    expect(groups[1].state?.color).toBe('#5e6ad2')
    expect(groups[1].createContext).toEqual({ stateId: 'p-done' })
  })

  it('adds one empty group per merged status, and a team board keeps its own status IDs', () => {
    const data = dataset()
    const empty = buildIssueGroups([], createIssueDisplayOptions({ grouping: 'status', completedWindow: 'all', showEmptyGroups: true }), { data })
    expect(empty.map(group => group.id)).toEqual(['p-todo', 'p-done', 's-done-canceled'])
    const subRows = rowsOf(data).filter(row => row.teamId === subTeam.id)
    const board = buildIssueGroups(subRows, createIssueDisplayOptions({ grouping: 'status', completedWindow: 'all', showEmptyGroups: true }), { data, states: subStates })
    expect(board.map(group => group.id)).toEqual(['s-todo', 's-done', 's-done-canceled'])
  })

  it('moves each issue dropped on a merged column to its own team’s status, and "+" creates in the team’s match', () => {
    const data = dataset()
    const [parentRow, subRow] = rowsOf(data)
    expect(groupMoveUpdate(subRow, 'status', 'p-done', { data })).toEqual({ stateId: 's-done' })
    expect(groupMoveUpdate(parentRow, 'status', 'p-done', { data })).toEqual({ stateId: 'p-done' })
    // A board scoped to the sub-team's workflow still finds the parent's status of a merged column.
    expect(groupMoveUpdate(subRow, 'status', 'p-todo', { data, states: subStates })).toEqual({ stateId: 's-todo' })
    expect(teamStateIdFor(data, subTeam.id, 'p-done')).toBe('s-done')
    expect(teamStateIdFor(data, parentTeam.id, 'p-done')).toBe('p-done')
  })
})

describe('status filter across team workflows', () => {
  it('offers one option per merged status with every status as an alias and the summed count', () => {
    const data = dataset()
    const options = explorerFilterOptions('status', explorerPropertyOptions(data, issues))!
    expect(options.map(option => [option.id, option.label, option.count, option.aliases])).toEqual([
      ['p-todo', 'Todo', 2, ['p-todo', 's-todo']], ['p-done', 'Done', 2, ['p-done', 's-done']], ['s-done-canceled', 'Done', 1, undefined],
    ])
    // The status pickers still list each team's own statuses.
    expect(explorerPropertyOptions(data, issues).status).toHaveLength(5)
  })

  it('filters every status of the group, in memory and in the server query', () => {
    const data = dataset()
    expect(applyExplorerFilters(issues, [statusFilter(['p-done'])], data).map(item => item.id)).toEqual(['1', '2'])
    expect(issueFiltersToQueryAst([statusFilter(['p-done'])], { data })).toEqual({ and: [{ field: 'status', operator: 'in', values: ['p-done', 's-done'] }] })
    // A canceled "Done" is another status.
    expect(applyExplorerFilters(issues, [statusFilter(['s-done-canceled'])], data).map(item => item.id)).toEqual(['5'])
    expect(applyExplorerFilters(issues, [{ ...statusFilter(['p-done']), operator: 'isNot' }], data).map(item => item.id)).toEqual(['3', '4', '5'])
  })

  it('resolves a saved view that stored one team’s status to the merged option', () => {
    const data = dataset()
    const saved = statusFilter(['s-done'])
    expect(applyExplorerFilters(issues, [saved], data).map(item => item.id)).toEqual(['1', '2'])
    const options = explorerFilterOptions('status', explorerPropertyOptions(data, issues))!
    const done = options.find(option => option.id === 'p-done')!
    expect(filterChipItem(saved, options).values.map(value => [value.id, value.label])).toEqual([['p-done', 'Done']])
    expect(filterChipItem(statusFilter(['s-done', 'p-done']), options).values.map(value => value.id)).toEqual(['p-done'])
    // Toggling the merged option off removes the stored status too.
    expect(toggleFilterOption([saved], 'status', 'Status', done)).toEqual([])
  })
})

describe('server-backed status groups across team workflows', () => {
  beforeEach(() => vi.clearAllMocks())

  it('merges the server’s per-status counts and pages a merged group by all of its statuses', () => {
    const data = dataset()
    expect(pagedStatusGroups([{ value: 's-done', count: 20 }, { value: 'p-todo', count: 1 }, { value: 'p-done', count: 10 }, { value: 's-done-canceled', count: 2 }], data)).toEqual([
      { value: 'p-todo', count: 1 }, { value: 'p-done', count: 30 }, { value: 's-done-canceled', count: 2 },
    ])
    expect(pagedGroupQuery({ groupBy: 'status' }, 'p-done', data)).toEqual({ groupBy: 'status', groupValue: undefined, groupValues: ['p-done', 's-done'] })
    expect(pagedGroupQuery({ groupBy: 'status' }, 's-done-canceled', data)).toEqual({ groupBy: 'status', groupValue: 's-done-canceled' })
    expect(pagedGroupQuery({ groupBy: 'priority' }, '1', data)).toEqual({ groupBy: 'priority', groupValue: '1' })
  })

  it('renders one Done group with the combined count', async () => {
    const data = { ...dataset([]), issueCollectionPaged: true }
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'p-done', count: 10 }, { value: 's-done', count: 20 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [issue('1', parentTeam, 'p-done'), issue('2', subTeam, 's-done')], hasMore: false, total: -1 })
    render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByText('Issue 2')
    expect(screen.getAllByText('Done')).toHaveLength(1)
    expect(screen.getByText('Done').closest('header')).toHaveTextContent('30')
    expect(listIssueRecords).toHaveBeenCalledWith(expect.objectContaining({ groupValues: ['p-done', 's-done'], groupValue: undefined }), expect.any(AbortSignal))
  })
})

import { describe, expect, it } from 'vitest'
import { makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, Issue, Team, WorkflowState } from '@/types/flow'
import { DEFAULT_INSIGHTS, type SavedViewInsightsConfig } from './insight-config'
import { buildInsightData } from './insight-data'
import { insightTargetMatches } from './insight-interaction'
import { timeInStatusTree } from './insight-options'
import { applyExplorerFilters, issueToExplorerRow } from './issue-explorer-model'

const parentTeam = { id: 'team-parent', key: 'PAR', name: 'Parent', color: '#5e6ad2' } as Team
const subTeam = { id: 'team-sub', key: 'SUB', name: 'Sub', color: '#26b5ce' } as Team
const state = (teamId: string, id: string, name: string, type: WorkflowState['type'], position: number, color: string) => ({ id, teamId, name, type, position, color }) as WorkflowState

const parentStates = [
  state(parentTeam.id, 'p-todo', 'Todo', 'unstarted', 0, '#e2e2e2'),
  state(parentTeam.id, 'p-progress', 'In Progress', 'started', 1, '#f2c94c'),
  state(parentTeam.id, 'p-review', 'In Review', 'started', 2, '#0f783c'),
  state(parentTeam.id, 'p-done', 'Done', 'completed', 3, '#5e6ad2'),
  state(parentTeam.id, 'p-canceled', 'Canceled', 'canceled', 4, '#95a2b3'),
]
// An independent workflow: the same names and types, its own IDs and colours, and "Done" also used for a canceled status.
const subStates = [
  state(subTeam.id, 's-todo', 'Todo', 'unstarted', 0, '#cccccc'),
  state(subTeam.id, 's-progress', 'In Progress', 'started', 1, '#ffaa00'),
  state(subTeam.id, 's-done', 'Done', 'completed', 2, '#00aa55'),
  state(subTeam.id, 's-done-canceled', 'Done', 'canceled', 3, '#999999'),
]
const byId = new Map([...parentStates, ...subStates].map(item => [item.id, item]))

let serial = 0
function issues(team: Team, stateId: string, count: number, extra: Partial<Issue> = {}) {
  return Array.from({ length: count }, () => { serial += 1; return makeIssue({ id: `issue-${serial}`, identifier: `${team.key}-${serial}`, number: serial, team, state: byId.get(stateId)!, ...extra }) })
}

function dataset(list: Issue[], options: { inherit?: boolean } = {}): BootstrapData {
  return makeBootstrap({
    issues: list,
    teams: [parentTeam, subTeam],
    states: options.inherit ? parentStates : [...parentStates, ...subStates],
    teamSettings: { [subTeam.id]: { parentTeamId: parentTeam.id, inheritWorkflowStatuses: Boolean(options.inherit) } } as unknown as BootstrapData['teamSettings'],
  })
}

const statusConfig: SavedViewInsightsConfig = { ...DEFAULT_INSIGHTS, slice: 'status', segment: 'none' }
function insight(list: Issue[], config: SavedViewInsightsConfig = statusConfig, data = dataset(list)) {
  return buildInsightData(list.map(issue => issueToExplorerRow(issue, 'workspace', list, data)), config, data)
}
const table = (result: ReturnType<typeof insight>) => result.rows.map(row => [row.label, row.total])

describe('status insights across team workflows', () => {
  it('merges a parent and an independent sub-team status with the same name and type into one group', () => {
    const list = [...issues(parentTeam, 'p-done', 10), ...issues(subTeam, 's-done', 20)]
    const result = insight(list)
    expect(table(result)).toEqual([['Done', 30]])
    // The display comes from the parent (the shallowest team in the hierarchy), not from whichever issue came first.
    expect(insight([...list].reverse()).rows[0]).toMatchObject({ label: 'Done', color: '#5e6ad2' })
    expect(result.rows[0]).toMatchObject({ label: 'Done', color: '#5e6ad2' })
    expect(result.rows[0].stateIds).toEqual(['p-done', 's-done'])
  })

  it('drills into the union of the merged statuses’ issues, each issue once', () => {
    const list = [...issues(parentTeam, 'p-done', 10), ...issues(subTeam, 's-done', 20), ...issues(subTeam, 's-todo', 3)]
    const result = insight(list)
    const done = result.rows.find(row => row.label === 'Done')!
    const drilled = result.samples.filter(sample => insightTargetMatches(sample, { slice: done.id })).map(sample => sample.item.id)
    expect(drilled).toHaveLength(30)
    expect(new Set(drilled).size).toBe(30)
    expect(new Set(drilled)).toEqual(new Set(list.filter(issue => issue.state.name === 'Done').map(issue => issue.id)))
  })

  it('keeps a single team’s statuses as they were', () => {
    const list = [...issues(parentTeam, 'p-todo', 1), ...issues(parentTeam, 'p-progress', 2), ...issues(parentTeam, 'p-review', 3), ...issues(parentTeam, 'p-done', 4), ...issues(parentTeam, 'p-canceled', 5)]
    const result = insight(list, statusConfig, dataset(list, { inherit: true }))
    expect(table(result)).toEqual([['Todo', 1], ['In Progress', 2], ['In Review', 3], ['Done', 4], ['Canceled', 5]])
    expect(result.rows.map(row => row.color)).toEqual(['#e2e2e2', '#f2c94c', '#0f783c', '#5e6ad2', '#95a2b3'])
    expect(result.rows.map(row => row.stateIds)).toEqual([['p-todo'], ['p-progress'], ['p-review'], ['p-done'], ['p-canceled']])
  })

  it('leaves an inheriting sub-team on the parent’s statuses (same IDs, one group each)', () => {
    const list = [...issues(parentTeam, 'p-done', 2), ...issues(subTeam, 'p-done', 5), ...issues(subTeam, 'p-progress', 1)]
    const result = insight(list, statusConfig, dataset(list, { inherit: true }))
    expect(table(result)).toEqual([['In Progress', 1], ['Done', 7]])
    expect(result.rows.map(row => row.stateIds)).toEqual([['p-progress'], ['p-done']])
  })

  it('never merges the same name across different status types', () => {
    const list = [...issues(parentTeam, 'p-done', 2), ...issues(subTeam, 's-done', 3), ...issues(subTeam, 's-done-canceled', 4)]
    const result = insight(list)
    expect(table(result)).toEqual([['Done', 5], ['Done', 4]])
    expect(result.rows.map(row => row.stateIds)).toEqual([['p-done', 's-done'], ['s-done-canceled']])
    expect(new Set(result.rows.map(row => row.id)).size).toBe(2)
  })

  it('keeps different names of one type apart, ordered by type then workflow position', () => {
    const list = [...issues(subTeam, 's-progress', 1), ...issues(parentTeam, 'p-review', 2), ...issues(parentTeam, 'p-progress', 3), ...issues(subTeam, 's-todo', 4), ...issues(subTeam, 's-done-canceled', 1), ...issues(parentTeam, 'p-done', 1)]
    expect(table(insight(list))).toEqual([['Todo', 4], ['In Progress', 4], ['In Review', 2], ['Done', 1], ['Done', 1]])
  })

  it('compares names trimmed and case-insensitively, keeping the parent’s spelling', () => {
    const shouty = state(subTeam.id, 's-done', '  DONE ', 'completed', 2, '#00aa55')
    const list = [...issues(subTeam, 's-done', 2, { state: shouty }), ...issues(parentTeam, 'p-done', 1)]
    const data = makeBootstrap({ ...dataset(list), states: [...parentStates, shouty] })
    expect(table(insight(list, statusConfig, data))).toEqual([['Done', 3]])
  })

  it('merges a status segment the same way, in the table cells and the drill-down', () => {
    const list = [...issues(parentTeam, 'p-done', 2, { assignee: viewer }), ...issues(subTeam, 's-done', 3, { assignee: viewer }), ...issues(subTeam, 's-progress', 1, { assignee: teammate }), ...issues(parentTeam, 'p-progress', 4, { assignee: teammate })]
    const result = insight(list, { ...statusConfig, slice: 'assignee', segment: 'status' })
    expect(result.segments.map(segment => [segment.label, segment.count, segment.color])).toEqual([['In Progress', 5, '#f2c94c'], ['Done', 5, '#5e6ad2']])
    const done = result.segments.find(segment => segment.label === 'Done')!
    expect(result.rows.find(row => row.label === 'Viewer')!.segments[done.id]).toBe(5)
    expect(result.samples.filter(sample => insightTargetMatches(sample, { segment: done.id }))).toHaveLength(5)
  })

  it('applies the page’s filters before merging', () => {
    const list = [...issues(parentTeam, 'p-done', 2, { assignee: viewer }), ...issues(subTeam, 's-done', 3, { assignee: viewer }), ...issues(subTeam, 's-done', 4, { assignee: teammate })]
    const data = dataset(list)
    const filtered = applyExplorerFilters(list, [{ id: 'assignee-filter', field: 'assignee', fieldLabel: 'Assignee', operator: 'is', value: viewer.id, valueLabel: 'Viewer', values: [{ value: viewer.id, valueLabel: 'Viewer' }] }], data)
    expect(table(insight(filtered, statusConfig, data))).toEqual([['Done', 5]])
  })

  it('gives the status type dimension the parent’s colour whatever the row order', () => {
    const list = [...issues(subTeam, 's-done', 2), ...issues(parentTeam, 'p-done', 1)]
    const result = insight(list, { ...statusConfig, slice: 'statusType' })
    expect(result.rows.map(row => [row.label, row.total, row.color])).toEqual([['Completed', 3, '#5e6ad2']])
  })

  it('resolves a stored "time in status" state ID to its whole merged group', () => {
    const at = (day: number) => `2026-08-0${day}T00:00:00.000Z`
    const list = [...issues(parentTeam, 'p-done', 1), ...issues(subTeam, 's-done', 1)]
    const data = dataset(list)
    const rows = list.map(issue => ({ ...issueToExplorerRow(issue, 'workspace', list, data), statusIntervals: [{ stateId: issue.state.id, stateType: 'completed', enteredAt: at(1), exitedAt: at(2) }] }))
    const result = buildInsightData(rows, { ...statusConfig, measure: 'timeInStatus', timeInStatusIds: ['p-done'] }, data)
    expect(result.samples).toHaveLength(2)
    const done = timeInStatusTree(data).find(node => node.label === 'Done')!
    expect(done.values).toEqual(['p-done', 's-done'])
  })
})

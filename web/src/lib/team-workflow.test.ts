import { describe, expect, it } from 'vitest'
import type { BootstrapData, TeamSettings, WorkflowState } from '@/types/flow'
import { statesForTeams, teamDefaultState, teamEquivalentState, teamForWorkflowState, teamHasWorkflowState, teamStateChanges, teamStateIdFor, teamSubIssueState, teamWorkflowStateOfType, teamWorkflowStates, workflowStatusOptions } from './team-workflow'

const state = (id: string, type: WorkflowState['type'], position: number, over: Partial<WorkflowState> = {}) => ({ id, name: id, color: '#888888', type, position, ...over }) as WorkflowState
const settings = (over: Partial<TeamSettings>) => over as TeamSettings
const data = {
  states: [
    state('ws-backlog', 'backlog', 0), state('ws-todo', 'unstarted', 1),
    state('a-done', 'completed', 3, { teamId: 'a' }), state('a-ready', 'unstarted', 1, { teamId: 'a' }), state('a-intake', 'backlog', 0, { teamId: 'a' }),
    state('child-copy', 'unstarted', 0, { teamId: 'child' }),
  ],
  teamSettings: {
    a: settings({ defaultStateId: 'a-done' }),
    child: settings({ defaultStateId: 'child-copy', inheritWorkflowStatuses: true, parentTeamId: 'a' }),
    loop: settings({ inheritWorkflowStatuses: true, parentTeamId: 'loop' }),
  },
} as unknown as Pick<BootstrapData, 'states' | 'teamSettings'>

describe('team workflow', () => {
  it('resolves custom, inherited and workspace workflows like the API', () => {
    expect(teamWorkflowStates(data, 'a').map(item => item.id)).toEqual(['a-intake', 'a-ready', 'a-done'])
    expect(teamWorkflowStates(data, 'child').map(item => item.id)).toEqual(['a-intake', 'a-ready', 'a-done'])
    expect(teamWorkflowStates(data, 'plain').map(item => item.id)).toEqual(['ws-backlog', 'ws-todo'])
    expect(teamWorkflowStates(data, 'loop')).toEqual([])
  })

  it('picks the configured default, then the first unstarted status of the team', () => {
    expect(teamDefaultState(data, 'a')?.id).toBe('a-done')
    expect(teamDefaultState(data, 'child')?.id).toBe('a-ready')
    expect(teamDefaultState(data, 'plain')?.id).toBe('ws-todo')
    expect(teamDefaultState(data, 'loop')).toBeUndefined()
  })

  it('checks a status against the team workflow and finds a team for it', () => {
    expect(teamHasWorkflowState(data, 'a', 'ws-todo')).toBe(false)
    expect(teamHasWorkflowState(data, 'child', 'child-copy')).toBe(false)
    expect(teamHasWorkflowState(data, 'child', 'a-ready')).toBe(true)
    expect(teamForWorkflowState(data, ['plain', 'a'], 'a-ready')).toBe('a')
    expect(teamForWorkflowState(data, ['plain', 'a'], 'missing')).toBeUndefined()
    expect(teamWorkflowStateOfType(data, 'child', 'backlog')?.id).toBe('a-intake')
    expect(teamWorkflowStateOfType(data, 'a', 'backlog')?.id).toBe('a-intake')
  })

  it('starts a new issue in the first valid candidate, else the team default', () => {
    expect(teamStateIdFor(data, 'a', 'ws-todo', 'a-ready')).toBe('a-ready')
    expect(teamStateIdFor(data, 'a', 'ws-todo')).toBe('a-done')
    expect(teamStateIdFor(data, 'child', 'child-copy')).toBe('a-ready')
    expect(teamStateIdFor(data, 'loop', 'ws-todo')).toBe('')
    // Before statuses load the requested status is kept so it can be checked later.
    expect(teamStateIdFor({ states: [], teamSettings: {} }, 'a', 'a-ready')).toBe('a-ready')
  })

  it('starts a sub-issue in the first unstarted status of the effective workflow', () => {
    expect(teamSubIssueState(data, 'a')?.id).toBe('a-ready')
    expect(teamSubIssueState(data, 'child')?.id).toBe('a-ready')
    expect(teamSubIssueState(data, 'plain')?.id).toBe('ws-todo')
  })

  it('maps a status of another workflow to the team equivalent, like a team move', () => {
    const todo = data.states.find(item => item.id === 'ws-todo')!
    expect(teamEquivalentState(data, 'plain', todo)?.id).toBe('ws-todo')
    expect(teamEquivalentState(data, 'a', todo)?.id).toBe('a-ready')
    expect(teamEquivalentState(data, 'child', { id: 'a-done', name: 'a-done', type: 'completed' })?.id).toBe('a-done')
    expect(teamEquivalentState(data, 'plain', { id: 'a-done', name: 'a-done', type: 'completed' })).toBeUndefined()
  })

  it('offers the statuses every team can use for a change across teams', () => {
    expect(statesForTeams(data, ['a', 'child']).map(item => item.id)).toEqual(['a-intake', 'a-ready', 'a-done'])
    // Nothing shared: the first team's workflow, mapped per team on apply.
    expect(statesForTeams(data, ['plain', 'a']).map(item => item.id)).toEqual(['ws-backlog', 'ws-todo'])
    expect(statesForTeams(data, [undefined])).toEqual([])
    const options = [{ id: 'ws-todo' }, { id: 'a-ready' }, { id: 'child-copy' }, { id: 'triage' }]
    expect(workflowStatusOptions(data, ['child'], options).map(item => item.id)).toEqual(['a-ready', 'triage'])
    expect(workflowStatusOptions(data, ['plain'], options).map(item => item.id)).toEqual(['ws-todo', 'triage'])
    expect(workflowStatusOptions(undefined, ['plain'], options)).toBe(options)
  })

  it('splits a status change so each team gets its own status', () => {
    expect(teamStateChanges(data, [{ id: '1', teamId: 'plain' }, { id: '2', teamId: 'a' }, { id: '3', teamId: 'child' }, { id: '4' }], 'ws-todo')).toEqual([
      { stateId: 'ws-todo', ids: ['1', '4'] }, { stateId: 'a-ready', ids: ['2', '3'] },
    ])
    // A team without an equivalent is left out.
    expect(teamStateChanges(data, [{ id: '1', teamId: 'plain' }, { id: '2', teamId: 'a' }], 'a-done')).toEqual([{ stateId: 'a-done', ids: ['2'] }])
  })
})

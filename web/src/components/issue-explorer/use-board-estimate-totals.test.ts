import { renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { listIssueRecordGroups } from '@/lib/api'
import { makeBootstrap } from '@/test/fixtures'
import type { WorkflowState } from '@/types/flow'
import { useBoardEstimateTotals } from './use-board-estimate-totals'

vi.mock('@/lib/api', () => ({ listIssueRecordGroups: vi.fn() }))

describe('useBoardEstimateTotals', () => {
  beforeEach(() => vi.clearAllMocks())

  it('reads every group total from one aggregate request, however many teams use estimates', async () => {
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'p1', count: 3, estimatePoints: 7 }, { value: 'p2', count: 2, estimatePoints: 0 }, { value: 'p3', count: 1 }] })
    const base = makeBootstrap()
    const teams = [...base.teams, { ...base.teams[0], id: 'team-2', key: 'TWO' }, { ...base.teams[0], id: 'team-3', key: 'THR' }]
    const teamSettings = Object.fromEntries(teams.map(team => [team.id, { teamId: team.id, estimateType: 'fibonacci' }])) as never
    const query = { groupBy: 'priority', filter: { field: 'priority', operator: 'in', values: ['1'] } }
    const { result } = renderHook(() => useBoardEstimateTotals(query, [{ value: 'p1' }, { value: 'p2' }, { value: 'p3' }], { states: base.states, teams, teamSettings }, 0))
    await waitFor(() => expect(result.current).toEqual({ p1: 7, p2: 0 }))
    expect(listIssueRecordGroups).toHaveBeenCalledTimes(1)
    expect(listIssueRecordGroups).toHaveBeenCalledWith({ ...query, aggregate: 'estimatePoints' }, expect.any(AbortSignal))
  })

  it('adds up the statuses that one merged status group shows', async () => {
    const base = makeBootstrap()
    const todo = (id: string, teamId: string) => ({ id, name: 'Todo', color: '#777777', type: 'unstarted', position: 0, teamId }) as WorkflowState
    const states = [todo('todo-a', base.teams[0].id), todo('todo-b', 'team-2')]
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'todo-a', count: 1, estimatePoints: 3 }, { value: 'todo-b', count: 1, estimatePoints: 5 }] })
    const teamSettings = { [base.teams[0].id]: { teamId: base.teams[0].id, estimateType: 'tShirt' } } as never
    const { result } = renderHook(() => useBoardEstimateTotals({ groupBy: 'status' }, [{ value: 'todo-a' }], { states, teams: base.teams, teamSettings }, 0))
    await waitFor(() => expect(result.current).toBeDefined())
    expect(Object.values(result.current!)).toEqual([8])
    expect(listIssueRecordGroups).toHaveBeenCalledTimes(1)
  })

  it('makes no request without a query, groups or a team that uses estimates', () => {
    const base = makeBootstrap()
    const estimating = { ...base, teamSettings: { [base.teams[0].id]: { teamId: base.teams[0].id, estimateType: 'fibonacci' } } } as never
    renderHook(() => useBoardEstimateTotals(undefined, [{ value: 'p1' }], estimating, 0))
    renderHook(() => useBoardEstimateTotals({ groupBy: 'priority' }, [], estimating, 0))
    renderHook(() => useBoardEstimateTotals({ groupBy: 'priority' }, [{ value: 'p1' }], { ...base, teamSettings: { [base.teams[0].id]: { teamId: base.teams[0].id, estimateType: 'notUsed' } } } as never, 0))
    expect(listIssueRecordGroups).not.toHaveBeenCalled()
  })
})

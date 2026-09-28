import { describe, expect, it } from 'vitest'
import { computeBurnUpSeries, burnUpPath, isWeekendUtc } from './burn-up-model'
import { makeIssue } from '@/test/fixtures'

describe('computeBurnUpSeries (LS-0102)', () => {
  const cycle = {
    id: 'cycle-1',
    startsAt: '2026-09-01T00:00:00.000Z',
    endsAt: '2026-09-05T00:00:00.000Z',
  }

  it('builds honest per-day Scope/Started/Completed series', () => {
    const issues = [
      makeIssue({
        id: 'a',
        cycleId: 'cycle-1',
        createdAt: '2026-09-01T10:00:00.000Z',
        startedAt: '2026-09-02T10:00:00.000Z',
        completedAt: '2026-09-04T10:00:00.000Z',
        state: { id: 'done', name: 'Done', type: 'completed', color: '#0f0', position: 1 },
      }),
      makeIssue({
        id: 'b',
        cycleId: 'cycle-1',
        createdAt: '2026-09-02T10:00:00.000Z',
        startedAt: '2026-09-03T10:00:00.000Z',
        state: { id: 'start', name: 'In Progress', type: 'started', color: '#00f', position: 1 },
      }),
      makeIssue({
        id: 'c',
        cycleId: 'cycle-1',
        createdAt: '2026-09-03T10:00:00.000Z',
        state: { id: 'todo', name: 'Todo', type: 'unstarted', color: '#888', position: 1 },
      }),
    ]

    const series = computeBurnUpSeries(cycle, issues, { asOf: '2026-09-05T00:00:00.000Z' })
    expect(series.days).toHaveLength(5)
    expect(series.days[0]).toMatchObject({ date: '2026-09-01', scope: 1, started: 0, completed: 0 })
    expect(series.days[1]).toMatchObject({ date: '2026-09-02', scope: 2, started: 1, completed: 0 })
    expect(series.days[2]).toMatchObject({ date: '2026-09-03', scope: 3, started: 2, completed: 0 })
    expect(series.days[3]).toMatchObject({ date: '2026-09-04', scope: 3, started: 2, completed: 1 })
    expect(series.days[4]).toMatchObject({ date: '2026-09-05', scope: 3, started: 2, completed: 1 })
    expect(series.completed).toBe(1)
    expect(series.scope).toBe(3)
  })

  it('supports estimate measure and path geometry', () => {
    const issues = [
      makeIssue({
        id: 'a',
        cycleId: 'cycle-1',
        estimate: 5,
        createdAt: '2026-09-01T00:00:00.000Z',
        completedAt: '2026-09-02T00:00:00.000Z',
        state: { id: 'done', name: 'Done', type: 'completed', color: '#0f0', position: 1 },
      }),
    ]
    const series = computeBurnUpSeries(cycle, issues, { measure: 'estimate', asOf: '2026-09-05' })
    expect(series.days[0].scope).toBe(5)
    expect(series.days[1].completed).toBe(5)
    expect(burnUpPath([0, 5], 5, 100, 100)).toContain('M')
    expect(isWeekendUtc('2026-09-05')).toBe(true) // Saturday
  })
})

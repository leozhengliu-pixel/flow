import { addDays, format, startOfDay } from 'date-fns'
import { describe, expect, it } from 'vitest'

import { buildProgressData } from './project-progress-data'
import type { Issue } from '@/types/flow'

function issue(createdAt: Date, state: 'started' | 'completed', index = 0): Issue {
  const timestamp = createdAt.toISOString()
  return {
    id: `${state}-${timestamp}-${index}`,
    title: 'Synthetic progress issue',
    identifier: 'SYN-1',
    createdAt: timestamp,
    updatedAt: timestamp,
    completedAt: state === 'completed' ? timestamp : undefined,
    state: { id: state, name: state, type: state, color: '#5e6ad2' },
  } as Issue
}

describe('project progress data', () => {
  it('creates a forecast window when the target date is still ahead', () => {
    const today = startOfDay(new Date())
    const start = addDays(today, -14)
    const target = addDays(today, 7)
    const result = buildProgressData([
      issue(addDays(today, -6), 'started', 0),
      ...Array.from({ length: 5 }, (_, index) => issue(addDays(today, -10), 'completed', index + 1)),
    ], format(start, 'yyyy-MM-dd'), format(target, 'yyyy-MM-dd'))

    expect(result.targetDate.getTime()).toBeGreaterThan(result.currentDate.getTime())
    expect(result.forecast.completed).toBe(5)
    expect(result.forecast.optimisticDate).toBeDefined()
    expect(result.forecast.pessimisticDate).toBeDefined()
    expect(result.series.map(item => item.id)).toEqual(['Scope', 'Started', 'Completed'])
  })

  it('keeps the target marker while projecting completion beyond an overdue target', () => {
    const today = startOfDay(new Date())
    const start = addDays(today, -14)
    const target = addDays(today, -2)
    const result = buildProgressData([
      issue(addDays(today, -10), 'started', 0),
      ...Array.from({ length: 5 }, (_, index) => issue(addDays(today, -3), 'completed', index + 1)),
    ], format(start, 'yyyy-MM-dd'), format(target, 'yyyy-MM-dd'))

    expect(result.targetDate.getTime()).toBeLessThan(result.currentDate.getTime())
    expect(result.endDate.getTime()).toBeGreaterThan(result.currentDate.getTime())
    expect(result.forecast.optimisticDate?.getTime()).toBeGreaterThan(result.currentDate.getTime())
    expect(result.forecast.pessimisticDate?.getTime()).toBeGreaterThan(result.forecast.optimisticDate?.getTime() ?? 0)
    expect(result.series.map(item => item.id)).toEqual(['Scope', 'Started', 'Completed'])
  })

  it('uses persisted weekly history for the rendered series', () => {
    const today = startOfDay(new Date())
    const start = addDays(today, -14)
    const target = addDays(today, 7)
    const history = [0, 3, 6].map((value, index) => ({ date: addDays(start, index * 7).toISOString(), value }))
    const result = buildProgressData([], format(start, 'yyyy-MM-dd'), format(target, 'yyyy-MM-dd'), {
      completedScopeHistory: history,
      inProgressScopeHistory: history,
      issueCountHistory: history,
      progressHistory: history,
      scopeHistory: history,
    })

    expect(result.series[0].data.map(point => point.y)).toEqual([0, 3, 6, 6])
    expect(result.series[2].data.map(point => point.y)).toEqual([0, 3, 6])
  })
})

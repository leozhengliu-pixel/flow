import { describe, expect, it, vi } from 'vitest'
import type { Cycle, Issue } from '@/types/flow'
import { cycleStats, cycleStatusLabel, formatCycleDay, formatCycleRange, weekdaysLeft } from './cycle-model'

const cycle = {
  id: 'cycle-1', name: 'Cycle 1', number: 1, capacity: 4, status: 'current',
  startsAt: '2026-08-24T00:00:00.000Z', endsAt: '2026-09-04T00:00:00.000Z',
} as Cycle

function issue(id: string, type: Issue['state']['type'], options: Partial<Issue> = {}) {
  return { id, cycleId: cycle.id, state: { id: `state-${type}`, name: type, color: '#000', type }, ...options } as Issue
}

describe('cycle model', () => {
  it('calculates active scope, capacity, and completion ratios', () => {
    const stats = cycleStats(cycle, [
      issue('started', 'started'),
      issue('completed', 'completed'),
      issue('todo', 'unstarted'),
      issue('archived', 'completed', { archivedAt: '2026-08-25T00:00:00.000Z' }),
      issue('other-cycle', 'completed', { cycleId: 'cycle-2' }),
    ])
    expect(stats).toEqual({
      scope: 3, started: 1, completed: 1, capacity: 4,
      capacityPercent: 75, successPercent: 42, startedPercent: 33, completedPercent: 33,
    })
  })

  it('formats dates and status labels consistently', () => {
    expect(formatCycleRange(cycle)).toContain('Aug 24, 2026')
    expect(formatCycleRange(cycle, true)).toContain('Sep 4')
    expect(formatCycleDay(cycle.startsAt)).toEqual({ month: 'Aug', day: 24 })
    expect(cycleStatusLabel('upcoming')).toBe('Upcoming')
  })

  it('counts weekdays remaining without returning negative values', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-31T08:00:00.000Z'))
    expect(weekdaysLeft(cycle)).toBe(5)
    vi.setSystemTime(new Date('2026-09-07T08:00:00.000Z'))
    expect(weekdaysLeft(cycle)).toBe(0)
    vi.useRealTimers()
  })
})

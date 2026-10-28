import { describe, expect, it, vi } from 'vitest'
import type { Cycle, Issue } from '@/types/flow'
import { cooldownWeeksBetween, cycleListOrder, cycleStats, cycleStatusLabel, formatCycleDay, formatCycleRange, weekdaysLeft } from './cycle-model'

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
      estimates: false, scope: 3, started: 1, completed: 1, capacity: 4, capacityRatio: .75,
      capacityPercent: 75, successPercent: 42, startedPercent: 33, completedPercent: 33,
    })
  })

  it('measures scope and capacity in estimate points when the team uses estimates', () => {
    const pointsCycle = { ...cycle, capacity: 30 }
    const issues = [issue('a', 'unstarted', { estimate: 3 }), issue('b', 'started', { estimate: 5 })]
    const stats = cycleStats(pointsCycle, issues, { estimateType: 'fibonacci' })
    expect(stats).toMatchObject({ estimates: true, scope: 8, started: 5, capacityPercent: 27 })
    expect(Math.floor(stats.capacityRatio * 100)).toBe(26)
  })

  it('counts unestimated issues as 1 point unless turned off, and zero estimates only when allowed', () => {
    const issues = [issue('a', 'unstarted', { estimate: 3 }), issue('none', 'unstarted'), issue('zero', 'unstarted', { estimate: 0 })]
    expect(cycleStats(cycle, issues, { estimateType: 'fibonacci' }).scope).toBe(5)
    expect(cycleStats(cycle, issues, { estimateType: 'fibonacci', estimateAllowZero: true }).scope).toBe(4)
    expect(cycleStats(cycle, issues, { estimateType: 'fibonacci', estimateAllowZero: true, estimateCountUnestimated: false }).scope).toBe(3)
    expect(cycleStats(cycle, issues, { estimateType: 'notUsed' }).scope).toBe(3)
  })

  it('measures the cooldown between consecutive cycles in weeks', () => {
    expect(cooldownWeeksBetween({ endsAt: '2026-11-09T00:00:00Z' }, { startsAt: '2026-11-24T00:00:00Z' })).toBe(2)
    expect(cooldownWeeksBetween({ endsAt: '2026-11-09T00:00:00Z' }, { startsAt: '2026-11-17T00:00:00Z' })).toBe(1)
    expect(cooldownWeeksBetween({ endsAt: '2026-11-09T00:00:00Z' }, { startsAt: '2026-11-10T00:00:00Z' })).toBe(0)
    // A cycle completed early ends the day the next one starts: no cooldown between them.
    expect(cooldownWeeksBetween({ endsAt: '2026-10-27T00:00:00Z' }, { startsAt: '2026-10-27T00:00:00Z' })).toBe(0)
  })

  it('lists the latest start first and a later cycle above one completed on its start day', () => {
    const order = cycleListOrder([
      { id: 'c1', number: 1, startsAt: '2026-10-27T00:00:00Z' },
      { id: 'c3', number: 3, startsAt: '2026-12-22T00:00:00Z' },
      { id: 'c2', number: 2, startsAt: '2026-10-27T00:00:00Z' },
    ])
    expect(order.map(item => item.id)).toEqual(['c3', 'c2', 'c1'])
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

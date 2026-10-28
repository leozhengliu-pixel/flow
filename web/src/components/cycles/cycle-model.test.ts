import { describe, expect, it, vi } from 'vitest'
import type { Cycle, Issue } from '@/types/flow'
import { canStartCycleToday, cooldownWeeksBetween, cycleDateBounds, cycleDateMoveAffects, cycleListOrder, cycleNeighbors, cycleStats, cycleStatusLabel, formatCycleDay, formatCycleRange, weekdaysLeft } from './cycle-model'

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

  it('counts cooldown weeks as Sunday-to-Saturday calendar weeks, like the reference cycles list', () => {
    const weeks = (lastDay: string, nextStart: string) => cooldownWeeksBetween({ endsAt: `${lastDay}T00:00:00Z` }, { startsAt: `${nextStart}T00:00:00Z` })
    expect(weeks('2026-11-15', '2026-11-30')).toBe(2) // 14 days, Monday to Monday
    expect(weeks('2026-10-29', '2026-11-30')).toBe(5) // 31 days, Friday to Monday
    expect(weeks('2026-11-29', '2026-12-07')).toBe(1) // 7 days
    expect(weeks('2026-11-29', '2026-12-09')).toBe(1) // 9 days, Monday to Wednesday
    expect(weeks('2026-11-29', '2026-12-06')).toBe(1) // 6 days, Monday to Sunday
    expect(weeks('2026-11-15', '2026-12-06')).toBe(3) // 20 days, Monday to Sunday
  })

  it('lists the latest start first and a later cycle above one completed on its start day', () => {
    const order = cycleListOrder([
      { id: 'c1', number: 1, startsAt: '2026-10-27T00:00:00Z' },
      { id: 'c3', number: 3, startsAt: '2026-12-22T00:00:00Z' },
      { id: 'c2', number: 2, startsAt: '2026-10-27T00:00:00Z' },
    ])
    expect(order.map(item => item.id)).toEqual(['c3', 'c2', 'c1'])
  })

  describe('date moves', () => {
    const scheduled = (id: string, number: number, status: Cycle['status'], start: string, end: string) => ({ id, number, status, startsAt: `${start}T00:00:00Z`, endsAt: `${end}T00:00:00Z` })
    const c1 = scheduled('c1', 1, 'current', '2026-10-28', '2026-11-15')
    const c2 = scheduled('c2', 2, 'upcoming', '2026-11-30', '2026-12-13')
    const c3 = scheduled('c3', 3, 'upcoming', '2026-12-28', '2027-01-10')
    const cycles = [c3, c1, c2]

    it('finds the neighbouring cycles', () => {
      expect(cycleNeighbors(cycles, c2)).toEqual({ previous: c1, next: c3 })
      expect(cycleNeighbors(cycles, c1)).toEqual({ previous: undefined, next: c2 })
    })

    it('offers "Start cycle today" on the next upcoming cycle only while no cycle is current', () => {
      expect(canStartCycleToday(cycles, c2)).toBe(false)
      const cooldown = [{ ...c1, status: 'completed' as const }, c2, c3]
      expect(canStartCycleToday(cooldown, c2)).toBe(true)
      expect(canStartCycleToday(cooldown, c3)).toBe(false)
    })

    it('limits a moved end to two days and to before the next cycle\'s last day', () => {
      expect(cycleDateBounds(cycles, c1, 'end', '2026-10-28')).toEqual({ min: '2026-10-29', max: '2026-12-12' })
      expect(cycleDateBounds(cycles, c3, 'end', '2026-10-28')).toEqual({ min: '2026-12-29', max: undefined })
    })

    it('limits a moved start to two days of the previous cycle and to today', () => {
      expect(cycleDateBounds(cycles, c2, 'start', '2026-10-28')).toEqual({ min: '2026-10-30', max: undefined })
      expect(cycleDateBounds([{ ...c1, status: 'completed' as const }, c2], c2, 'start', '2026-10-28')).toEqual({ min: '2026-11-16', max: undefined })
    })

    it('names the neighbour a move pushes', () => {
      expect(cycleDateMoveAffects(cycles, c1, 'end', '2026-11-29')).toBeUndefined()
      expect(cycleDateMoveAffects(cycles, c1, 'end', '2026-12-04')).toBe(c2)
      expect(cycleDateMoveAffects(cycles, c2, 'start', '2026-11-16')).toBeUndefined()
      expect(cycleDateMoveAffects(cycles, c2, 'start', '2026-11-15')).toBe(c1)
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

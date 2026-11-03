import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import type { CycleGraphData } from '@/lib/api'
import { CycleChart } from './cycle-chart'
import { cycleAxisTicks, cycleChartDays, cycleLegendMetrics, cycleWeekendBands } from './cycle-chart-model'

vi.mock('@/lib/theme', () => ({ THEME_CHANGE_EVENT: 'flow-theme-change' }))

// Oct 28 2026 is a Wednesday: Saturdays and Sundays are days 3-4, 10-11 and 17-18 of 19.
const days = cycleChartDays('2026-10-28T00:00:00Z', '2026-11-15T00:00:00Z')

describe('cycle chart geometry', () => {
  it('lists a cycle\'s calendar days inclusively', () => {
    expect(days).toHaveLength(19)
    expect(days[0]).toBe('2026-10-28')
    expect(days.at(-1)).toBe('2026-11-15')
    expect(cycleChartDays('2026-11-15', '2026-10-28')).toEqual([])
  })

  it('merges a Saturday and a Sunday into one band', () => {
    expect(cycleWeekendBands(days)).toEqual([[3, 5], [10, 12], [17, 19]])
    expect(cycleWeekendBands(cycleChartDays('2026-10-31', '2026-10-31'))).toEqual([[0, 1]])
  })

  it('ticks the start, each Monday and the day after the last day, dropping crowded weeks', () => {
    const wide = cycleAxisTicks(days, 399)
    expect(wide.map(tick => [tick.date, tick.kind])).toEqual([['2026-10-28', 'start'], ['2026-11-02', 'week'], ['2026-11-09', 'week'], ['2026-11-16', 'end']])
    expect(wide[1].fraction).toBeCloseTo(5 / 19)
    // At 274px the first Monday is only 72px from the start; at 259px the second is 95px from the end and stays.
    expect(cycleAxisTicks(days, 274).map(tick => tick.date)).toEqual(['2026-10-28', '2026-11-09', '2026-11-16'])
    expect(cycleAxisTicks(days, 259).map(tick => tick.date)).toEqual(['2026-10-28', '2026-11-09', '2026-11-16'])
    expect(cycleAxisTicks([], 400)).toEqual([])
  })
})

describe('cycle legend figures', () => {
  const graph = {
    cycleId: 'c', timezone: 'UTC', startDate: '2026-10-28', endDate: '2026-10-30', today: '2026-10-29', estimates: false,
    days: [
      { date: '2026-10-28', scope: 4, started: 1, completed: 0, scopePoints: 0, startedPoints: 0, completedPoints: 0 },
      { date: '2026-10-29', scope: 5, started: 2, completed: 1, scopePoints: 0, startedPoints: 0, completedPoints: 0 },
      { date: '2026-10-30', future: true, scope: 0, started: 0, completed: 0, scopePoints: 0, startedPoints: 0, completedPoints: 0 },
    ],
    summary: { scope: 5, started: 2, completed: 1, scopePoints: 0, startedPoints: 0, completedPoints: 0, added: 0, removed: 0, addedPoints: 0, removedPoints: 0 },
    breakdown: { assignees: [], labels: [], projects: [] },
  } as CycleGraphData
  const totals = { graph, measure: 'issues' as const, scope: 5, started: 2, startedPercent: 40, completed: 1, completedPercent: 20 }

  it('shows the totals, a hovered day\'s figures, and dashes for a day that has not happened', () => {
    expect(cycleLegendMetrics(totals).map(metric => [metric.key, metric.count, metric.percent])).toEqual([['scope', 5, undefined], ['started', 2, 40], ['completed', 1, 20]])
    expect(cycleLegendMetrics({ ...totals, hoveredDate: '2026-10-28' }).map(metric => [metric.count, metric.percent])).toEqual([[4, undefined], [1, 25], [0, 0]])
    expect(cycleLegendMetrics({ ...totals, hoveredDate: '2026-10-30' }).map(metric => metric.count)).toEqual([null, null, null])
  })
})

describe('cycle chart axis', () => {
  it('renders the week ticks and a hover date', () => {
    const { container, rerender } = render(<I18nProvider><CycleChart measure="issues" startsAt="2026-10-28T00:00:00Z" endsAt="2026-11-15T00:00:00Z" height={200} label="chart"/></I18nProvider>)
    // Without a measured width only the start and the end are kept.
    expect([...container.querySelectorAll('.cycle-chart__axis > span')].map(label => label.textContent)).toEqual(['Oct 28', 'Nov 16'])
    expect(container.querySelector('.cycle-chart__tooltip')).toBeNull()
    rerender(<I18nProvider><CycleChart measure="issues" startsAt="2026-10-28T00:00:00Z" endsAt="2026-11-15T00:00:00Z" height={200} label="chart" hover={9}/></I18nProvider>)
    expect(container.querySelector('.cycle-chart__tooltip')?.textContent).toBe('Nov 6')
  })
})

import { addDays, format, startOfDay } from 'date-fns'
import { describe, expect, it } from 'vitest'
import type { Issue } from '@/types/flow'
import { buildProgressData } from './project-progress-data'

const day = (offset: number) => format(addDays(startOfDay(new Date()), offset), 'yyyy-MM-dd')

describe('buildProgressData', () => {
  // Paged deployments don't load the project's issues on the detail page;
  // the chart must still fit the persisted history instead of a 0–1 axis.
  it('fits persisted history when no issues are loaded locally', () => {
    const chart = buildProgressData([], day(-21), day(14), {
      scopeHistory: [{ date: day(-21), value: 7 }, { date: day(-14), value: 41 }, { date: day(-7), value: 49 }, { date: day(0), value: 62 }],
      completedScopeHistory: [{ date: day(-21), value: 0 }, { date: day(-14), value: 20 }, { date: day(-7), value: 40 }, { date: day(0), value: 58 }],
      inProgressScopeHistory: [{ date: day(0), value: 1 }],
    })
    const plotted = chart.series.flatMap(series => series.data.map(point => point.y))
    expect(chart.totalEstimate).toBe(62)
    expect(chart.yMax).toBeGreaterThanOrEqual(Math.max(...plotted))
    // The target line sits at the current scope, not on the floor.
    expect(chart.series.find(series => series.id === 'Target')?.data.every(point => point.y === 62)).toBe(true)
    expect(chart.forecast.total).toBe(62)
  })

  it('keeps using local issues when they are loaded', () => {
    const issue = (id: string, estimate: number) => ({ id, estimate, createdAt: `${day(-3)}T10:00:00Z`, updatedAt: `${day(-3)}T10:00:00Z`, state: { type: 'unstarted' } }) as never
    const chart = buildProgressData([issue('a', 3), issue('b', 5)], day(-7), day(7))
    expect(chart.totalEstimate).toBe(8)
    expect(chart.yMax).toBe(8)
  })

  it('matches direct counts over many dates, including events before the chart start', () => {
    const date = (offset: number) => `${day(offset)}T12:00:00`
    const issues = Array.from({ length: 2_000 }, (_, index) => ({
      id: `issue-${index}`, title: `Issue ${index}`, estimate: index % 6 + 1,
      createdAt: date(-900 + index % 880), updatedAt: date(-891 + index % 880),
      state: { type: ['completed', 'started', 'canceled', 'unstarted'][index % 4] },
    }) as Issue)
    const chart = buildProgressData(issues, day(-700), day(30))
    const scoped = issues.filter(issue => issue.state.type !== 'canceled')
    for (const series of chart.series) {
      for (const point of series.data.filter((_, index) => index % 13 === 0)) {
        const key = format(point.x, 'yyyy-MM-dd')
        const matches = scoped.filter(issue => {
          if (series.id === 'Target') return true
          if (series.id === 'Completed') return issue.state.type === 'completed' && issue.updatedAt.slice(0, 10) <= key
          if (series.id === 'Started' && issue.state.type === 'unstarted') return false
          return issue.createdAt.slice(0, 10) <= key
        })
        expect(point.y, `${series.id} ${key}`).toBe(matches.reduce((sum, issue) => sum + issue.estimate!, 0))
      }
    }
  })
})

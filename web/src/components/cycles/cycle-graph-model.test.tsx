import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import type { CycleGraphData, CycleGraphDay } from '@/lib/api'
import type { Cycle } from '@/types/flow'
import { cycleStats } from './cycle-model'
import { CycleGraph } from './cycle-graph'
import { cycleGraphIndexAt, cycleGraphPath, cycleGraphSeries, PROJECTION_WINDOW } from './cycle-graph-model'
import { CycleProgress } from './cycle-progress'

function day(date: string, scope: number, started: number, completed: number, future = false): CycleGraphDay {
  return future
    ? { date, future, scope: 0, started: 0, completed: 0, scopePoints: 0, startedPoints: 0, completedPoints: 0 }
    : { date, scope, started, completed, scopePoints: scope * 2, startedPoints: started * 2, completedPoints: completed * 2 }
}

const activeGraph: CycleGraphData = {
  cycleId: 'cycle-1',
  timezone: 'UTC',
  startDate: '2026-09-01',
  endDate: '2026-09-05',
  today: '2026-09-03',
  estimates: true,
  days: [
    day('2026-09-01', 4, 1, 0),
    day('2026-09-02', 5, 2, 1),
    day('2026-09-03', 6, 4, 2),
    day('2026-09-04', 0, 0, 0, true),
    day('2026-09-05', 0, 0, 0, true),
  ],
  summary: { scope: 6, started: 4, completed: 2, scopePoints: 12, startedPoints: 8, completedPoints: 4, added: 2, removed: 1, addedPoints: 4, removedPoints: 2 },
  breakdown: {
    assignees: [{ id: '', scope: 6, started: 4, completed: 2, scopePoints: 12, startedPoints: 8, completedPoints: 4 }],
    labels: [],
    projects: [],
  },
}

describe('cycle graph model', () => {
  it('keeps actuals up to today and leaves future days empty', () => {
    const series = cycleGraphSeries(activeGraph)
    expect(series.lastActualIndex).toBe(2)
    expect(series.todayIndex).toBe(2)
    expect(series.points.map(point => point.scope)).toEqual([4, 5, 6, null, null])
    expect(series.points.map(point => point.completed)).toEqual([0, 1, 2, null, null])
    expect(series).toMatchObject({ scope: 6, started: 4, completed: 2, added: 2, removed: 1, startedPercent: 67, completedPercent: 33 })
  })

  it('draws the target from zero to the current scope by the cycle end', () => {
    const series = cycleGraphSeries(activeGraph)
    expect(series.points.map(point => point.target)).toEqual([0, 1.5, 3, 4.5, 6])
  })

  it('projects completion from recent velocity for the active cycle only', () => {
    const series = cycleGraphSeries(activeGraph)
    // 2 completed over 2 elapsed days → 1 per day from today, capped at scope.
    expect(series.points.map(point => point.projected)).toEqual([null, null, 2, 3, 4])
    expect(PROJECTION_WINDOW).toBeGreaterThan(0)
    const finished = cycleGraphSeries({ ...activeGraph, today: undefined })
    expect(finished.points.every(point => point.projected === null)).toBe(true)
  })

  it('switches to estimate points', () => {
    const series = cycleGraphSeries(activeGraph, 'points')
    expect(series.points[2]).toMatchObject({ scope: 12, started: 8, completed: 4 })
    expect(series).toMatchObject({ scope: 12, completed: 4, added: 4, removed: 2 })
  })

  it('breaks paths on missing values and maps pointer positions to days', () => {
    expect(cycleGraphPath([0, 1, null, 2], 2, 30, 10)).toBe('M0.00 10.00 L10.00 5.00 M30.00 0.00')
    expect(cycleGraphIndexAt(0, 5, 100)).toBe(0)
    expect(cycleGraphIndexAt(60, 5, 100)).toBe(2)
    expect(cycleGraphIndexAt(500, 5, 100)).toBe(4)
  })
})

describe('cycle graph rendering', () => {
  it('shows the hovered day values and toggles issues vs points', () => {
    const onMeasureChange = vi.fn()
    render(<I18nProvider><CycleGraph graph={activeGraph} measure="issues" onMeasureChange={onMeasureChange}/></I18nProvider>)
    const plot = screen.getByRole('img')
    const legend = () => screen.getAllByRole('listitem').map(item => item.textContent)
    expect(legend()).toContain('Scope6')
    expect(legend()).toContain('Completed233%')
    vi.spyOn(plot, 'getBoundingClientRect').mockReturnValue({ left: 0, width: 400, top: 0, height: 150, right: 400, bottom: 150, x: 0, y: 0, toJSON: () => ({}) })
    fireEvent.pointerMove(plot, { clientX: 0 })
    expect(legend()).toContain('Scope4')
    fireEvent.pointerMove(plot, { clientX: 400 })
    expect(legend()).toContain('Scope–')
    expect(legend().some(text => text?.startsWith('Projected'))).toBe(true)
    fireEvent.click(screen.getByRole('radio', { name: 'Points' }))
    expect(onMeasureChange).toHaveBeenCalledWith('points')
  })

  it('hides the points toggle when the team does not use estimates', () => {
    render(<I18nProvider><CycleGraph graph={{ ...activeGraph, estimates: false }} measure="points" onMeasureChange={vi.fn()}/></I18nProvider>)
    expect(screen.queryByRole('radio', { name: 'Points' })).toBeNull()
    expect(screen.getAllByRole('listitem').map(item => item.textContent)).toContain('Scope6')
  })

  it('shows the weekend as one band per weekend, not one per day', () => {
    const weekendGraph: CycleGraphData = { ...activeGraph, startDate: '2026-09-04', endDate: '2026-09-08', today: '2026-09-04', days: [day('2026-09-04', 1, 0, 0), day('2026-09-05', 1, 0, 0), day('2026-09-06', 1, 0, 0), day('2026-09-07', 1, 0, 0), day('2026-09-08', 1, 0, 0)] }
    const { container } = render(<I18nProvider><CycleGraph graph={weekendGraph} measure="issues"/></I18nProvider>)
    const bands = container.querySelectorAll('.flow-cycle-graph__weekend')
    expect(bands).toHaveLength(1)
    // Saturday and Sunday (indexes 1 and 2 of 5): from half a day before the first to half a day after the last.
    expect((bands[0] as HTMLElement).style.left).toBe('12.5%')
    expect((bands[0] as HTMLElement).style.width).toBe('50%')
  })

  it('reads Scope, Started and Completed in the details card and breaks them down by tab', () => {
    const cycle = { id: 'cycle-1', number: 1, name: 'Cycle 1', status: 'current', startsAt: '2026-09-01', endsAt: '2026-09-05', capacity: 30 } as Cycle
    const stats = cycleStats(cycle, [])
    render(<I18nProvider><CycleProgress cycle={cycle} graph={activeGraph} issues={[]} stats={stats} settingsFor={() => undefined}/></I18nProvider>)
    expect(screen.getAllByRole('listitem').map(item => item.textContent)).toEqual(['Scope12', 'Started8•67%', 'Completed4•33%'])
    expect(screen.getAllByRole('tab').map(tab => tab.textContent)).toEqual(['Assignees', 'Labels', 'Priority', 'Projects', 'Teams'])
    fireEvent.click(screen.getByRole('button', { name: 'Collapse progress section' }))
    expect(screen.queryByRole('tab')).toBeNull()
  })
})

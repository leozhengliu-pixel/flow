import { render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { CycleGraphData } from '@/lib/api'
import type { Cycle } from '@/types/flow'
import { cycleStats } from './cycle-model'
import { CycleTimelineCard } from './cycle-timeline'

const graph: CycleGraphData = {
  cycleId: 'c1', timezone: 'UTC', startDate: '2026-11-01', endDate: '2026-11-02', today: '2026-11-02', estimates: false,
  days: [{ date: '2026-11-01', scope: 2, started: 1, completed: 0, scopePoints: 2, startedPoints: 1, completedPoints: 0 }],
  summary: { scope: 2, started: 1, completed: 0, scopePoints: 2, startedPoints: 1, completedPoints: 0, added: 0, removed: 0, addedPoints: 0, removedPoints: 0 },
  breakdown: { assignees: [], labels: [], projects: [] },
}
vi.mock('./use-cycle-graph', () => ({ useCycleGraph: () => ({ graph }) }))
vi.mock('@/lib/theme', () => ({ THEME_CHANGE_EVENT: 'flow-theme-change' }))
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('timeline canvas theme changes', () => {
  it.each([true, false])('repaints mounted pixels with the new palette and cleans up (ResizeObserver: %s)', observerAvailable => {
    vi.stubGlobal('ResizeObserver', observerAvailable ? class { observe() {} disconnect() {} } : undefined)
    vi.spyOn(HTMLCanvasElement.prototype, 'clientWidth', 'get').mockReturnValue(278)
    const strokes: string[] = []
    const context = {
      strokeStyle: '', fillStyle: '', lineWidth: 1,
      scale: vi.fn(), save: vi.fn(), beginPath: vi.fn(), rect: vi.fn(), clip: vi.fn(), restore: vi.fn(), fillRect: vi.fn(),
      setLineDash: vi.fn(), moveTo: vi.fn(), lineTo: vi.fn(), arc: vi.fn(), fill: vi.fn(),
      stroke() { strokes.push(this.strokeStyle) },
    }
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(context as unknown as CanvasRenderingContext2D)
    const cycle = { id: 'c1', number: 1, name: 'Cycle 1', status: 'current', capacity: 30, startsAt: '2026-11-01', endsAt: '2026-11-02' } as Cycle
    const view = render(<I18nProvider><CycleTimelineCard cycle={cycle} issues={[]} stats={cycleStats(cycle, [])} onOpen={vi.fn()}/></I18nProvider>)
    const canvas = view.container.querySelector('canvas')!
    canvas.style.setProperty('--issue-dim', 'dark-dim')
    window.dispatchEvent(new Event('flow-theme-change'))
    expect(strokes).toContain('dark-dim')
    strokes.length = 0
    canvas.style.setProperty('--issue-dim', 'light-dim')
    window.dispatchEvent(new Event('flow-theme-change'))
    expect(strokes).toContain('light-dim')
    expect(strokes).not.toContain('dark-dim')
    view.unmount()
    strokes.length = 0
    window.dispatchEvent(new Event('flow-theme-change'))
    expect(strokes).toEqual([])
  })
})

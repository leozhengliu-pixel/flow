import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeIssue, teammate } from '@/test/fixtures'
import type { Cycle, Issue, Team, TeamSettings } from '@/types/flow'
import { cycleStats } from './cycle-model'
import { CyclePlanning } from './cycle-planning'
import { cyclePlanningDays, cyclePlanningRows } from './cycle-planning-model'
import { CyclesPage } from './cycles-page'

const graphState = vi.hoisted(() => ({ graph: undefined as unknown }))
vi.mock('./use-cycle-graph', () => ({ useCycleGraph: () => ({ graph: graphState.graph }) }))

const team = { id: 'team-1', key: 'TST', name: 'Test team', color: '#5e6ad2' } as Team
const fibonacci = { teamId: 'team-1', estimateType: 'fibonacci' } as TeamSettings
const cycle = (id: string, number: number, startsAt: string, endsAt: string, over: Partial<Cycle> = {}) => ({ id, number, name: `Cycle ${number}`, teamId: team.id, status: 'upcoming', capacity: 30, startsAt, endsAt, ...over }) as Cycle
const cycle1 = cycle('c1', 1, '2026-11-02T00:00:00Z', '2026-11-15T00:00:00Z')
const cycle2 = cycle('c2', 2, '2026-11-30T00:00:00Z', '2026-12-13T00:00:00Z')
const issues = [makeIssue({ id: 'a', estimate: 3, cycleId: 'c1', assignee: undefined }), makeIssue({ id: 'b', estimate: 5, cycleId: 'c1', assignee: undefined })]

function page(estimateSettings?: TeamSettings) {
  return render(<I18nProvider><CyclesPage cycles={[cycle1, cycle2]} issues={issues} team={team} estimateSettings={estimateSettings} settings={{ enabled: true, durationWeeks: 2, cooldownWeeks: 2, upcomingCount: 2 }} onOpen={vi.fn()} onUpdateCycle={vi.fn()} onStartCycle={vi.fn()} onCompleteCycle={vi.fn()} onUpdateSettings={vi.fn()} onReload={vi.fn()} onOpenSidebar={vi.fn()}/></I18nProvider>)
}

describe('cycle capacity and scope', () => {
  it('lists "26% of capacity" and "△ 8 scope" for 8 of 30 points, "0%" for an empty cycle, and the cooldown between cycles', () => {
    const { container } = page(fibonacci)
    const rows = [...container.querySelectorAll('.flow-cycle-row')] as HTMLElement[]
    expect(rows.map(row => row.querySelector('.flow-cycle-name')?.textContent)).toEqual(['Cycle 2', 'Cycle 1'])
    expect(rows[1].querySelector('.flow-cycle-capacity')).toHaveTextContent('26%of capacity')
    expect(rows[1].querySelector('.flow-cycle-scope')).toHaveTextContent('8 scope')
    expect(rows[1].querySelector('.flow-cycle-scope .estimate-icon')).not.toBeNull()
    expect(rows[0].querySelector('.flow-cycle-capacity')).toHaveTextContent('0%of capacity')
    expect(rows[0].querySelector('.flow-cycle-scope')).toHaveTextContent('0 scope')
    expect(screen.getByText('2 weeks cooldown')).toBeInTheDocument()
  })

  it('counts issues instead of points for teams without estimates', () => {
    const { container } = page({ ...fibonacci, estimateType: 'notUsed' })
    const row = [...container.querySelectorAll('.flow-cycle-row')].at(-1) as HTMLElement
    expect(row.querySelector('.flow-cycle-scope')).toHaveTextContent('2 scope')
    expect(row.querySelector('.flow-cycle-scope .estimate-icon')).toBeNull()
  })

  it('shows Planning with Capacity, Days to start, Scope and per-tab estimate sums', async () => {
    const user = userEvent.setup()
    const all = [...issues, makeIssue({ id: 'c', estimate: undefined, cycleId: 'c1', assignee: teammate, priority: 1, labels: [] })] as Issue[]
    const stats = cycleStats(cycle1, all, fibonacci)
    render(<I18nProvider><CyclePlanning cycle={cycle1} issues={all} stats={stats} settingsFor={() => fibonacci}/></I18nProvider>)
    const values = [...document.querySelectorAll('.cycle-planning dl > div')].map(item => `${item.querySelector('dt')?.textContent}=${item.querySelector('dd')?.textContent}`)
    expect(values[0]).toBe('Capacity=30%')
    expect(values[1]).toMatch(/^Days to start=\d+$/)
    expect(values[2]).toBe('Scope=9')
    expect(document.querySelector('.cycle-planning-scope .estimate-icon')).not.toBeNull()
    expect(screen.getAllByRole('tab').map(tab => tab.textContent)).toEqual(['Assignees', 'Labels', 'Priority', 'Projects', 'Teams'])
    const breakdown = () => [...screen.getByRole('tabpanel').querySelectorAll('.cycle-planning-row')].map(row => `${row.querySelector(':scope > span:first-child > span:last-of-type')?.textContent} △ ${row.querySelector('.cycle-planning-value')?.textContent}`)
    expect(breakdown()).toEqual(['No assignee △ 8', 'Teammate △ 1'])
    await user.click(screen.getByRole('tab', { name: 'Priority' }))
    expect(breakdown()).toEqual(['Urgent △ 1', 'High △ 8'])
    await user.click(screen.getByRole('tab', { name: 'Teams' }))
    expect(breakdown()).toEqual(['Test team △ 9'])
    expect(within(screen.getByRole('tabpanel')).getByText('Test team').closest('.cycle-planning-row')?.querySelector('.estimate-icon')).not.toBeNull()
  })

  it('draws the current cycle summary in estimate points, or issue counts without estimates', () => {
    const totals = (scope: number, started: number, completed: number, points: number) => ({ scope, started, completed, scopePoints: scope * points, startedPoints: started * points, completedPoints: completed * points })
    graphState.graph = {
      cycleId: 'c1', timezone: 'UTC', startDate: '2026-11-02', endDate: '2026-11-15', today: '2026-11-03', estimates: true,
      days: [{ date: '2026-11-02', ...totals(2, 0, 0, 4) }, { date: '2026-11-03', ...totals(2, 1, 1, 4) }, { date: '2026-11-04', future: true, ...totals(0, 0, 0, 0) }],
      summary: { ...totals(2, 1, 1, 4), added: 1, removed: 0, addedPoints: 5, removedPoints: 0 },
      breakdown: { assignees: [], labels: [], projects: [] },
    }
    try {
      const current = { ...cycle1, status: 'current' as const }
      const metrics = (settings: TeamSettings) => {
        const { container, unmount } = render(<I18nProvider><CyclesPage cycles={[current]} issues={issues} team={team} estimateSettings={settings} settings={{ enabled: true, durationWeeks: 2, cooldownWeeks: 0, upcomingCount: 2 }} onOpen={vi.fn()} onUpdateCycle={vi.fn()} onStartCycle={vi.fn()} onCompleteCycle={vi.fn()} onUpdateSettings={vi.fn()} onReload={vi.fn()} onOpenSidebar={vi.fn()}/></I18nProvider>)
        const values = [...container.querySelectorAll('.flow-cycle-current-metrics > span')].map(item => `${item.querySelector('small')?.textContent}=${item.querySelector('strong')?.textContent}${item.querySelector('em')?.textContent ?? ''}`)
        unmount()
        return values
      }
      expect(metrics(fibonacci)).toEqual(['Scope=8+5', 'Started=4• 50%', 'Completed=4• 50%'])
      expect(metrics({ ...fibonacci, estimateType: 'notUsed' })).toEqual(['Scope=2+1', 'Started=1• 50%', 'Completed=1• 50%'])
    } finally {
      graphState.graph = undefined
    }
  })

  it('builds breakdown rows and the days count', () => {
    const rows = cyclePlanningRows(issues, 'priority', () => ({ ...fibonacci, estimateCountUnestimated: false }))
    expect(rows.map(row => [row.name, row.value])).toEqual([['High', 8]])
    expect(cyclePlanningDays(cycle1, new Date(2026, 9, 26, 18).getTime())).toBe(7)
    expect(cyclePlanningDays({ ...cycle1, status: 'current' }, new Date(2026, 10, 14, 12).getTime())).toBe(1)
    expect(cyclePlanningDays(cycle1, new Date(2026, 10, 20).getTime())).toBe(0)
  })
})

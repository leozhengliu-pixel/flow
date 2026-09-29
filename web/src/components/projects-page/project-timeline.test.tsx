import { act, fireEvent, render, renderHook, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { ProjectTimeline, TIMELINE_LABEL_WIDTH } from './project-timeline'
import { parseTimelineDay, timelineRange, timelineX } from './project-timeline-model'
import type { ProjectDataGroup, ProjectPageItem } from './projects-data-view'
import { useProjectsViewState } from './use-projects-view-state'

const today = parseTimelineDay('2026-09-29')!

function item(overrides: Partial<ProjectPageItem>): ProjectPageItem {
  return { id: 'p', name: 'Project', health: 'on-track', priority: 'none', issueCount: 0, progress: 0, status: 'In Progress', ...overrides }
}

const blocker = item({ id: 'a', name: 'Blocker', rawStartDate: '2026-09-01', rawTargetDate: '2026-09-20', milestones: [
  { id: 'm1', name: 'Alpha', targetDate: '2026-09-10', progress: 100 },
  { id: 'm2', name: 'Beta', targetDate: '2026-09-18', progress: 25 },
] })
const blocked = item({ id: 'b', name: 'Blocked', rawStartDate: '2026-09-15', rawTargetDate: '2026-10-05', blockedByIds: ['a'] })
const later = item({ id: 'c', name: 'Later', rawStartDate: '2026-10-10', rawTargetDate: '2026-10-30', blockedByIds: ['b'] })
const undated = item({ id: 'd', name: 'Undated' })
const groups: ProjectDataGroup[] = [{ id: 'g', name: 'In Progress', projects: [blocker, blocked, later, undated] }]

function renderTimeline(props: Partial<Parameters<typeof ProjectTimeline>[0]> = {}) {
  return render(<I18nProvider><ProjectTimeline groups={groups} today={today} {...props}/></I18nProvider>)
}

afterEach(() => { localStorage.clear() })

describe('ProjectTimeline zoom', () => {
  it('switches Week / Month / Quarter / Year and reports the change', () => {
    const onZoomChange = vi.fn()
    const { container } = renderTimeline({ zoom: 'quarter', onZoomChange })
    expect(screen.getByRole('radio', { name: /Quarter/ }).getAttribute('aria-checked')).toBe('true')
    fireEvent.click(screen.getByRole('radio', { name: /Week/ }))
    expect(onZoomChange).toHaveBeenCalledWith('week')
    fireEvent.keyDown(container.querySelector('.lp-project-timeline')!, { key: '-' })
    expect(onZoomChange).toHaveBeenLastCalledWith('year')
    fireEvent.keyDown(container.querySelector('.lp-project-timeline')!, { key: '+' })
    expect(onZoomChange).toHaveBeenLastCalledWith('month')
  })

  it('rescales bars and the today line when uncontrolled zoom changes', () => {
    const { container } = renderTimeline()
    const width = () => parseFloat((screen.getByRole('button', { name: 'Blocker timeline bar' }).parentElement as HTMLElement).style.width)
    const quarterWidth = width()
    fireEvent.click(screen.getByRole('radio', { name: /Week/ }))
    expect(width()).toBeGreaterThan(quarterWidth * 5)
    const line = container.querySelector<HTMLElement>('.lp-project-timeline__today-line')!
    expect(parseFloat(line.style.left)).toBeGreaterThan(TIMELINE_LABEL_WIDTH)
    expect(container.querySelector('.lp-project-timeline__minor')!.children.length).toBeGreaterThan(50)
  })

  it('persists the chosen zoom with the display options', () => {
    const first = renderHook(() => useProjectsViewState([], { storageKey: 'zoom-test' }))
    expect(first.result.current.dataViewProps.timelineZoom).toBe('quarter')
    act(() => first.result.current.dataViewProps.onTimelineZoomChange?.('week'))
    expect(first.result.current.state.display.timelineZoom).toBe('week')
    first.unmount()
    const second = renderHook(() => useProjectsViewState([], { storageKey: 'zoom-test' }))
    expect(second.result.current.dataViewProps.timelineZoom).toBe('week')
    second.unmount()
    const other = renderHook(() => useProjectsViewState([], { storageKey: 'other-view' }))
    expect(other.result.current.dataViewProps.timelineZoom).toBe('quarter')
  })
})

describe('ProjectTimeline milestones', () => {
  it('renders filled and outlined diamonds at their target dates and opens the milestone', () => {
    const onOpenMilestone = vi.fn()
    renderTimeline({ zoom: 'month', onOpenMilestone })
    const alpha = screen.getByRole('button', { name: 'Milestone Alpha' })
    const beta = screen.getByRole('button', { name: 'Milestone Beta' })
    expect(alpha.hasAttribute('data-completed')).toBe(true)
    expect(beta.hasAttribute('data-completed')).toBe(false)
    expect(parseFloat(beta.style.left) - parseFloat(alpha.style.left)).toBe(8 * 12)
    fireEvent.click(alpha)
    expect(onOpenMilestone).toHaveBeenCalledWith(blocker, 'm1')
  })

  it('drags a milestone to a new target date', () => {
    const onUpdateMilestone = vi.fn().mockResolvedValue(undefined)
    renderTimeline({ zoom: 'month', onUpdateMilestone })
    const beta = screen.getByRole('button', { name: 'Milestone Beta' })
    fireEvent.pointerDown(beta, { button: 0, clientX: 100, pointerId: 1 })
    fireEvent.pointerMove(beta, { clientX: 100 + 12 * 3, pointerId: 1 })
    fireEvent.pointerUp(beta, { clientX: 100 + 12 * 3, pointerId: 1 })
    expect(onUpdateMilestone).toHaveBeenCalledWith('a', 'm2', { targetDate: '2026-09-21' })
  })
})

describe('ProjectTimeline dependencies', () => {
  it('draws blocker → blocked connectors and highlights overlaps in red', () => {
    const { container } = renderTimeline({ zoom: 'month' })
    const links = [...container.querySelectorAll<SVGPathElement>('path.lp-project-timeline__link')]
    expect(links.map(link => `${link.dataset.blocker}>${link.dataset.blocked}`)).toEqual(['a>b', 'b>c'])
    expect(links[0].classList.contains('is-conflict')).toBe(true)
    expect(links[1].classList.contains('is-conflict')).toBe(false)
    const range = timelineRange([parseTimelineDay('2026-09-01')!, parseTimelineDay('2026-10-30')!], today, 'month')
    expect(links[1].getAttribute('d')!.startsWith(`M${timelineX(range, parseTimelineDay('2026-10-06')!)},`)).toBe(true)
  })

  it('creates a dependency by dragging from a bar end handle onto another bar', () => {
    const onCreateDependency = vi.fn().mockResolvedValue(undefined)
    const { container } = renderTimeline({ zoom: 'month', onCreateDependency })
    const handle = container.querySelector('[data-timeline-project="c"] .lp-project-timeline__link-handle.is-end')!
    const target = container.querySelector('[data-timeline-project="a"]')!
    const original = document.elementsFromPoint
    document.elementsFromPoint = () => [target]
    try {
      fireEvent.pointerDown(handle, { button: 0, clientX: 10, clientY: 10, pointerId: 1 })
      fireEvent.pointerMove(handle, { clientX: 20, clientY: 20, pointerId: 1 })
      fireEvent.pointerUp(handle, { clientX: 20, clientY: 20, pointerId: 1 })
    } finally {
      document.elementsFromPoint = original
    }
    expect(onCreateDependency).toHaveBeenCalledWith('c', 'a')
  })
})

describe('ProjectTimeline undated rows', () => {
  it('drags across an undated row to set start and target dates', () => {
    const onUpdateProject = vi.fn().mockResolvedValue(undefined)
    const onOpenProject = vi.fn()
    renderTimeline({ zoom: 'week', onUpdateProject, onOpenProject })
    const row = screen.getByRole('button', { name: 'Add dates for Undated' })
    // The canvas spans every dated project (Sep 1 – Oct 30) plus today.
    const range = timelineRange([parseTimelineDay('2026-09-01')!, parseTimelineDay('2026-10-30')!], today, 'week')
    const x = (date: string) => TIMELINE_LABEL_WIDTH + timelineX(range, parseTimelineDay(date)!) + 5
    fireEvent.pointerDown(row, { button: 0, clientX: x('2026-10-01'), pointerId: 1 })
    fireEvent.pointerMove(row, { clientX: x('2026-10-07'), pointerId: 1 })
    fireEvent.pointerUp(row, { clientX: x('2026-10-07'), pointerId: 1 })
    expect(onUpdateProject).toHaveBeenCalledWith('d', { startDate: '2026-10-01', targetDate: '2026-10-07' })
    fireEvent.click(row)
    expect(onOpenProject).not.toHaveBeenCalled()
    fireEvent.click(row)
    expect(onOpenProject).toHaveBeenCalledWith(undated)
  })
})

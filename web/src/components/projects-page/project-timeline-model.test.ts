import { describe, expect, it } from 'vitest'
import {
  daysBetween,
  dependencyPath,
  dependencyRelationsWithBlocker,
  desktopTimelineHeader,
  indexTimelineDependencies,
  isDependencyConflict,
  normalizeTimelineZoom,
  parseTimelineDay,
  projectDependencyEdges,
  projectTimelineSpan,
  stepTimelineZoom,
  TIMELINE_PX_PER_DAY,
  timelineBarBox,
  timelineDaysForPixels,
  timelineDependencyPaths,
  timelineHeader,
  timelineMilestoneMarkers,
  timelineRange,
  timelineWindowDependencies,
  timelineX,
  type TimelineRowBox,
} from './project-timeline-model'

const day = (value: string) => parseTimelineDay(value)!

describe('timeline zoom scale', () => {
  it('orders zoom levels from Week (most detail) to Year and clamps steps', () => {
    expect(TIMELINE_PX_PER_DAY.week).toBeGreaterThan(TIMELINE_PX_PER_DAY.month)
    expect(TIMELINE_PX_PER_DAY.month).toBeGreaterThan(TIMELINE_PX_PER_DAY.quarter)
    expect(TIMELINE_PX_PER_DAY.quarter).toBeGreaterThan(TIMELINE_PX_PER_DAY.year)
    expect(stepTimelineZoom('month', -1)).toBe('week')
    expect(stepTimelineZoom('week', -1)).toBe('week')
    expect(stepTimelineZoom('quarter', 1)).toBe('year')
    expect(stepTimelineZoom('year', 1)).toBe('year')
    expect(normalizeTimelineZoom('bogus')).toBe('year')
    expect(normalizeTimelineZoom('week')).toBe('week')
  })

  it('snaps the range to the zoom unit and covers today plus every date', () => {
    const today = day('2026-09-29')
    const month = timelineRange([day('2026-11-15')], today, 'month')
    expect(month.start).toEqual(day('2026-08-01'))
    expect(month.end >= day('2026-12-01')).toBe(true)
    expect(month.width).toBe(Math.round(month.days * TIMELINE_PX_PER_DAY.month))

    const quarter = timelineRange([day('2025-02-10')], today, 'quarter')
    expect(quarter.start).toEqual(day('2024-10-01'))

    const week = timelineRange([], today, 'week')
    expect(week.start.getDay()).toBe(1) // Monday
    expect(week.start <= today && week.end > today).toBe(true)

    const year = timelineRange([], today, 'year')
    expect(year.start).toEqual(day('2025-01-01'))
  })

  it('converts between pixels and days at each zoom', () => {
    const range = timelineRange([], day('2026-09-29'), 'week')
    expect(timelineX(range, range.start)).toBe(0)
    expect(timelineX(range, day('2026-09-29'))).toBe(daysBetween(range.start, day('2026-09-29')) * 36)
    expect(timelineDaysForPixels(36 * 3 + 10, 36)).toBe(3)
    expect(timelineDaysForPixels(-20, 36)).toBe(-1)
    expect(timelineDaysForPixels(-5, 36)).toBe(0)
    expect(Object.is(timelineDaysForPixels(-5, 36), -0)).toBe(false)
  })

  it('counts calendar days across DST changes', () => {
    expect(daysBetween(day('2026-03-01'), day('2026-04-01'))).toBe(31)
    expect(daysBetween(day('2026-10-20'), day('2026-11-10'))).toBe(21)
  })

  it('adapts header tiers to the zoom', () => {
    const today = day('2026-09-29')
    const week = timelineHeader(timelineRange([], today, 'week'))
    expect(week.major[0].label).toMatch(/^[A-Z][a-z]{2} \d{4}$/)
    expect(week.minor.every(segment => segment.width === 36)).toBe(true)

    const month = timelineHeader(timelineRange([], today, 'month'))
    expect(month.minor[1].width).toBe(7 * 12)

    const quarter = timelineHeader(timelineRange([], today, 'quarter'))
    expect(quarter.major[0].label).toBe('Q2 2026')
    expect(quarter.minor.map(segment => segment.label).slice(0, 3)).toEqual(['Apr', 'May', 'Jun'])

    const year = timelineHeader(timelineRange([], today, 'year'))
    expect(year.major.map(segment => segment.label).slice(0, 2)).toEqual(['2025', '2026'])
    // Segments tile the canvas without gaps.
    for (const tier of [year.major, year.minor, quarter.minor]) {
      for (let index = 1; index < tier.length; index += 1) expect(tier[index].x).toBeCloseTo(tier[index - 1].x + tier[index - 1].width)
    }
  })

  it('jumps directly to a late week window without truncating or shifting the ticks', () => {
    const today = day('2026-09-29')
    const far = day('2071-01-01')
    const range = timelineRange(Array.from({ length: 150_000 }, () => far), today, 'week')
    const left = timelineX(range, day('2070-01-08'))
    const visible = { left, right: left + 720 }
    for (const header of [timelineHeader(range, visible), desktopTimelineHeader(range, 'en-US', visible)]) {
      expect(header.minor[0]).toMatchObject({ key: '2070-01-08', x: left })
      expect(header.minor).toHaveLength(21)
      expect(header.major.length).toBeLessThanOrEqual(2)
    }
  })

  it('preserves calendar-aligned weeks when jumping deep into a year view', () => {
    const range = timelineRange([day('2031-01-01')], day('2024-01-01'), 'year')
    const window = { left: timelineX(range, day('2029-07-03')), right: timelineX(range, day('2030-01-05')) }
    const indexed = desktopTimelineHeader(range, 'zh-CN', window)
    const original = desktopTimelineHeader(range, 'zh-CN')
    expect(indexed.minor.length).toBeLessThan(30)
    expect(indexed.minor).toEqual(original.minor.filter(tick => tick.x + tick.width > window.left && tick.x <= window.right))
    expect(indexed.major).toEqual(original.major.filter(tick => tick.x + tick.width > window.left && tick.x <= window.right))
  })
})

describe('project bars', () => {
  it('builds spans for partial dates and skips undated projects', () => {
    expect(projectTimelineSpan()).toBeUndefined()
    expect(projectTimelineSpan('2026-09-01', '2026-09-20')).toMatchObject({ start: day('2026-09-01'), end: day('2026-09-20'), hasStart: true, hasTarget: true })
    const openEnded = projectTimelineSpan('2026-09-01')!
    expect(daysBetween(openEnded.start, openEnded.end)).toBe(21)
    expect(openEnded.hasTarget).toBe(false)
    expect(projectTimelineSpan(undefined, '2026-09-20')).toMatchObject({ start: day('2026-09-20'), end: day('2026-09-20'), hasStart: false })
  })

  it('includes the target day in the bar width', () => {
    const range = { start: day('2026-09-01'), pxPerDay: 10 }
    expect(timelineBarBox(range, { start: day('2026-09-02'), end: day('2026-09-04') })).toEqual({ left: 10, width: 30 })
    expect(timelineBarBox(range, { start: day('2026-09-02'), end: day('2026-09-02') }, 24).width).toBe(24)
  })
})

describe('milestone markers', () => {
  it('centres diamonds on their target day and marks completed ones', () => {
    const range = { start: day('2026-09-01'), pxPerDay: 12 }
    const markers = timelineMilestoneMarkers(range, [
      { id: 'b', name: 'Beta', targetDate: '2026-09-11', progress: 100 },
      { id: 'a', name: 'Alpha', targetDate: '2026-09-03', progress: 40 },
      { id: 'c', name: 'Undated', progress: 0 },
    ])
    expect(markers.map(marker => marker.id)).toEqual(['a', 'b'])
    expect(markers[0]).toMatchObject({ x: 2 * 12 + 6, completed: false })
    expect(markers[1]).toMatchObject({ x: 10 * 12 + 6, completed: true })
  })
})

describe('dependencies', () => {
  const box = (left: number, width: number, y: number, start: string, end: string): TimelineRowBox => ({ left, width, y, start: day(start), end: day(end) })

  it('flags a conflict when the blocked project starts before the blocker ends', () => {
    expect(isDependencyConflict({ end: day('2026-09-10') }, { start: day('2026-09-11') })).toBe(false)
    expect(isDependencyConflict({ end: day('2026-09-10') }, { start: day('2026-09-10') })).toBe(true)
    expect(isDependencyConflict({ end: day('2026-09-10') }, { start: day('2026-09-01') })).toBe(true)
  })

  it('draws from the blocker end to the blocked start', () => {
    const rows = new Map([
      ['a', box(0, 100, 18, '2026-09-01', '2026-09-10')],
      ['b', box(200, 50, 54, '2026-09-20', '2026-09-25')],
      ['c', box(40, 50, 90, '2026-09-05', '2026-09-09')],
    ])
    const paths = timelineDependencyPaths([
      { blockerId: 'a', blockedId: 'b' },
      { blockerId: 'a', blockedId: 'c' },
      { blockerId: 'a', blockedId: 'b' },
      { blockerId: 'a', blockedId: 'missing' },
    ], rows)
    expect(paths).toHaveLength(2)
    expect(paths[0]).toMatchObject({ from: { x: 100, y: 18 }, to: { x: 200, y: 54 }, conflict: false })
    expect(paths[0].d.startsWith('M100,18 C150,18 150,54 200,54')).toBe(true)
    expect(paths[1]).toMatchObject({ conflict: true })
    // Backwards links route with an elbow that still enters from the left.
    expect(paths[1].d).toBe(dependencyPath({ x: 100, y: 18 }, { x: 40, y: 90 }))
    expect(paths[1].d.endsWith('H40')).toBe(true)
  })

  it('keeps crossings with neither endpoint on-screen and replaces only the moved project box', () => {
    const rows = new Map([
      ['a', box(0, 10, 0, '2026-09-01', '2026-09-10')],
      ['b', box(100, 30, 200, '2026-09-20', '2026-09-30')],
      ['c', box(700, 30, 200, '2026-09-20', '2026-09-30')],
    ])
    const edges = [{ blockerId: 'a', blockedId: 'b' }, { blockerId: 'b', blockedId: 'c' }]
    for (let i = 0; i < 2_000; i++) {
      rows.set(`offscreen-${i}`, box(100, 10, 1_000 + i * 50, '2026-09-20', '2026-09-30'))
      if (i) edges.push({ blockerId: `offscreen-${i - 1}`, blockedId: `offscreen-${i}` })
    }
    const index = indexTimelineDependencies(edges, rows)
    const window = { left: 40, right: 80, top: 80, bottom: 120 }
    expect(timelineWindowDependencies(index, window)).toEqual(timelineDependencyPaths(edges.slice(0, 1), rows))
    const moved = box(10, 10, 200, '2026-09-04', '2026-09-14')
    expect(timelineWindowDependencies(index, window, { id: 'b', box: moved })).toEqual([])
    expect(timelineWindowDependencies(index, window)[0]?.to).toEqual({ x: 100, y: 200 })
  })

  it('normalises dependencyIds and relations into blocker → blocked edges', () => {
    const edges = projectDependencyEdges(
      [{ id: 'b', dependencyIds: ['a'] }, { id: 'c' }],
      [
        { projectId: 'c', relatedProjectId: 'd', type: 'blocks' },
        { projectId: 'e', relatedProjectId: 'c', type: 'blocked_by' },
        { projectId: 'x', relatedProjectId: 'y', type: 'related' },
      ],
    )
    expect(edges).toEqual([
      { blockerId: 'a', blockedId: 'b' },
      { blockerId: 'c', blockedId: 'd' },
      { blockerId: 'c', blockedId: 'e' },
    ])
  })

  it('adds a blocker while keeping existing owned relations', () => {
    expect(dependencyRelationsWithBlocker('b', 'new', [
      { projectId: 'b', relatedProjectId: 'a', type: 'blocked_by' },
      { projectId: 'b', relatedProjectId: 'z', type: 'blocks' },
      { projectId: 'q', relatedProjectId: 'b', type: 'blocks' },
    ], ['a', 'legacy'])).toEqual([
      { projectId: 'a', type: 'blocked_by' },
      { projectId: 'z', type: 'blocks' },
      { projectId: 'legacy', type: 'blocked_by' },
      { projectId: 'new', type: 'blocked_by' },
    ])
  })
})

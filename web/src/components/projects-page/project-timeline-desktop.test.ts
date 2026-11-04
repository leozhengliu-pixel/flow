import { describe, expect, it } from 'vitest'
import { DESKTOP_AXIS_LABEL_PITCH, desktopTimelineHeader, timelineRange } from './project-timeline-model'

describe('desktop timeline axis', () => {
  it('keeps month names, January year context and a weekly tick at year zoom', () => {
    const range = { ...timelineRange([], new Date(2026, 9, 30), 'year'), pxPerDay: 2.5 }
    const header = desktopTimelineHeader(range, 'en-US')
    expect(header.major.some(segment => segment.label === 'Jan 2027')).toBe(true)
    expect(header.major.some(segment => segment.label === 'Apr')).toBe(true)
    expect(header.minor.every(segment => /^(\d{1,2})?$/.test(segment.label))).toBe(true)
    expect(header.minor[2].x - header.minor[1].x).toBeCloseTo(7 * range.pxPerDay)
  })

  it('numbers every other week once weekly ticks sit closer than the label pitch', () => {
    const range = { ...timelineRange([], new Date(2026, 9, 30), 'year'), pxPerDay: 2.5 }
    const header = desktopTimelineHeader(range, 'en-US')
    const labelled = header.minor.filter(segment => segment.label)
    expect(header.minor.length).toBeGreaterThan(labelled.length)
    expect(labelled.length).toBeGreaterThan(30)
    for (let index = 1; index < labelled.length; index += 1) expect(labelled[index].x - labelled[index - 1].x).toBeCloseTo(14 * range.pxPerDay)
  })

  it('never packs numbered ticks closer than the label pitch at any zoom', () => {
    for (const zoom of ['week', 'month', 'quarter', 'year'] as const) {
      const range = timelineRange([], new Date(2026, 9, 30), zoom)
      const labelled = desktopTimelineHeader(range, 'en-US').minor.filter(segment => segment.label)
      expect(labelled.length, zoom).toBeGreaterThan(5)
      for (let index = 1; index < labelled.length; index += 1) expect(labelled[index].x - labelled[index - 1].x, zoom).toBeGreaterThanOrEqual(DESKTOP_AXIS_LABEL_PITCH - 0.001)
    }
  })

  it('numbers the same weeks whatever the range start', () => {
    const early = desktopTimelineHeader({ ...timelineRange([new Date(2025, 2, 1)], new Date(2026, 9, 30), 'quarter') }, 'en-US')
    const late = desktopTimelineHeader({ ...timelineRange([], new Date(2026, 9, 30), 'quarter') }, 'en-US')
    const labelledDays = (header: typeof early) => new Set(header.minor.filter(segment => segment.label).map(segment => segment.key))
    const shared = [...labelledDays(late)].filter(key => early.minor.some(segment => segment.key === key))
    expect(shared.length).toBeGreaterThan(10)
    for (const key of shared) expect(labelledDays(early).has(key), key).toBe(true)
    for (const segment of late.minor.filter(item => !item.label)) expect(labelledDays(early).has(segment.key), segment.key).toBe(false)
  })

  it('localizes month labels without changing date geometry', () => {
    const range = timelineRange([], new Date(2026, 9, 30), 'quarter')
    const english = desktopTimelineHeader(range, 'en-US')
    const chinese = desktopTimelineHeader(range, 'zh-CN')
    expect(chinese.major[0].label).not.toBe(english.major[0].label)
    expect(chinese.major.map(segment => [segment.x, segment.width])).toEqual(english.major.map(segment => [segment.x, segment.width]))
  })
})

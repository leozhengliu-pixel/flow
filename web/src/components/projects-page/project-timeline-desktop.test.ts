import { describe, expect, it } from 'vitest'
import { desktopTimelineHeader, timelineRange } from './project-timeline-model'

describe('desktop timeline axis', () => {
  it('keeps month names, January year context and fortnight day ticks at year zoom', () => {
    const range = timelineRange([], new Date(2026, 9, 30), 'year')
    const header = desktopTimelineHeader(range, 'en-US')
    expect(header.major.some(segment => segment.label === 'Jan 2027')).toBe(true)
    expect(header.major.some(segment => segment.label === 'Apr')).toBe(true)
    expect(header.minor.every(segment => /^\d{1,2}$/.test(segment.label))).toBe(true)
    expect(header.minor[2].x - header.minor[1].x).toBeCloseTo(14 * range.pxPerDay)
  })

  it('localizes month labels without changing date geometry', () => {
    const range = timelineRange([], new Date(2026, 9, 30), 'quarter')
    const english = desktopTimelineHeader(range, 'en-US')
    const chinese = desktopTimelineHeader(range, 'zh-CN')
    expect(chinese.major[0].label).not.toBe(english.major[0].label)
    expect(chinese.major.map(segment => [segment.x, segment.width])).toEqual(english.major.map(segment => [segment.x, segment.width]))
  })
})

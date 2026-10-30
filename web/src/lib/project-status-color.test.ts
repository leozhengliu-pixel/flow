import { describe, expect, it } from 'vitest'
import { PROJECT_STATUS_DEFAULT_COLORS, hexToLch, normalizeHexColor, projectStatusOptionColor, projectStatusStrokeColor } from './project-status-color'

describe('projectStatusOptionColor', () => {
  const current = { id: 'started', name: 'In progress', color: '#ffcc00' }

  it('uses the current status color for matching identities or names', () => {
    expect(projectStatusOptionColor({ id: 'started', name: 'Started', color: '#111' }, current)).toBe('#ffcc00')
    expect(projectStatusOptionColor({ id: 'other', name: 'In progress', color: '#222' }, current)).toBe('#ffcc00')
  })

  it('keeps the option color for unrelated statuses', () => {
    expect(projectStatusOptionColor({ id: 'done', name: 'Done', color: '#00aa66' }, current)).toBe('#00aa66')
  })
})

describe('PROJECT_STATUS_DEFAULT_COLORS', () => {
  it('uses the reference app project status palette', () => {
    expect(PROJECT_STATUS_DEFAULT_COLORS).toEqual({
      backlog: '#f2994a',
      planned: '#d7d8db',
      started: 'lch(80% 90 85)',
      completed: '#5e6ad2',
      canceled: '#8a8f98',
    })
  })
})

describe('projectStatusStrokeColor', () => {
  it('keeps hex colours on dark surfaces and maps the brand yellow and purple to the reference app LCH', () => {
    expect(projectStatusStrokeColor('#F2994A', 'dark')).toBe('#F2994A')
    expect(projectStatusStrokeColor('#f2c94c', 'dark')).toBe('lch(80% 90 85)')
    expect(projectStatusStrokeColor('#5E6AD2', 'light')).toBe('lch(48% 59.31 288.43)')
  })

  it('darkens bright hex colours on light surfaces', () => {
    expect(projectStatusStrokeColor('#F2994A', 'light')).toMatch(/^lch\(70\.313% 62\.0\d+ 61\.6\d+\)$/)
    expect(projectStatusStrokeColor('#d7d8db', 'light')).toMatch(/^lch\(70\.313% 1\.\d+ 27\d\.\d+\)$/)
    expect(projectStatusStrokeColor('#8A8F98', 'light')).toBe('#8A8F98')
    expect(projectStatusStrokeColor('lch(60% 40 30)', 'light')).toBe('lch(60% 40 30)')
  })

  it('converts hex to the LCH values the reference app writes', () => {
    const [l, c, h] = hexToLch('#f2994a')!
    expect(l).toBeCloseTo(71.56, 1)
    expect(c).toBeCloseTo(62.08, 1)
    expect(h).toBeCloseTo(61.65, 1)
    expect(normalizeHexColor('#ABC')).toBe('#aabbcc')
    expect(normalizeHexColor('nope')).toBeNull()
  })
})

import { describe, expect, it, vi } from 'vitest'
import { DEFAULT_PROJECT_ISSUE_DISPLAY } from './project-issue-display'
import { formatProjectPropertyDate, initiativeStatusLabel } from './project-detail-helpers'

describe('project detail helpers', () => {
  it('formats every supported project date resolution', () => {
    const localized = vi.fn(() => 'localized')
    expect(formatProjectPropertyDate(undefined, undefined, 'Target date', 'en-US', localized)).toBe('Target date')
    expect(formatProjectPropertyDate('2026-08-15', 'month', '', 'en-US', localized)).toBe('Aug 2026')
    expect(formatProjectPropertyDate('2026-08-15', 'month', '', 'en-US', localized, 'short')).toBe("Aug '26")
    expect(formatProjectPropertyDate('2026-08-15', 'quarter', '', 'en-US', localized)).toBe('Q3 2026')
    expect(formatProjectPropertyDate('2026-08-15', 'halfYear', '', 'en-US', localized)).toBe('H2 2026')
    expect(formatProjectPropertyDate('2026-08-15', 'year', '', 'en-US', localized)).toBe('2026')
    expect(formatProjectPropertyDate('2026-08-15', undefined, '', 'zh-CN', localized)).toBe('localized')
    expect(localized).toHaveBeenCalled()
  })

  it('normalizes initiative labels and project issue display defaults', () => {
    expect(initiativeStatusLabel('active')).toBe('Active')
    expect(initiativeStatusLabel('canceled')).toBe('Canceled')
    expect(initiativeStatusLabel('planned')).toBe('Planned')
    expect(DEFAULT_PROJECT_ISSUE_DISPLAY).toMatchObject({ grouping: 'status', ordering: 'priority', completedWindow: 'all' })
    expect(DEFAULT_PROJECT_ISSUE_DISPLAY.properties.has('dueDate')).toBe(true)
  })
})

import { describe, expect, it } from 'vitest'
import { formatRelativeTime } from './relative-time'

const now = Date.parse('2026-10-09T12:00:00Z')
const ago = (ms: number) => new Date(now - ms).toISOString()

describe('formatRelativeTime', () => {
  it('formats compact English relative times', () => {
    expect(formatRelativeTime(ago(5_000), 'en-US', now)).toBe('just now')
    expect(formatRelativeTime(ago(31 * 60_000), 'en-US', now)).toBe('31min ago')
    expect(formatRelativeTime(ago(3 * 3_600_000), 'en-US', now)).toBe('3h ago')
    expect(formatRelativeTime(ago(9 * 86_400_000), 'en-US', now)).toBe('9d ago')
    expect(formatRelativeTime(ago(70 * 86_400_000), 'en-US', now)).toBe('2mo ago')
    expect(formatRelativeTime(ago(800 * 86_400_000), 'en-US', now)).toBe('2y ago')
  })
  it('formats Chinese relative times without mixing English units', () => {
    expect(formatRelativeTime(ago(5_000), 'zh-CN', now)).toBe('刚刚')
    expect(formatRelativeTime(ago(31 * 60_000), 'zh-CN', now)).toBe('31分钟前')
    expect(formatRelativeTime(ago(3 * 3_600_000), 'zh-CN', now)).toBe('3小时前')
    expect(formatRelativeTime(ago(10 * 86_400_000), 'zh-CN', now)).toBe('10天前')
    expect(formatRelativeTime(ago(70 * 86_400_000), 'zh-CN', now)).toBe('2个月前')
  })
  it('returns an empty string for invalid dates and clamps future dates', () => {
    expect(formatRelativeTime('nope', 'en-US', now)).toBe('')
    expect(formatRelativeTime(new Date(now + 60_000), 'en-US', now)).toBe('just now')
  })
})

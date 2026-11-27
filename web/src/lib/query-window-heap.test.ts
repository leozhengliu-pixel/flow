import { describe, expect, it } from 'vitest'
import { checkQueryWindowRetention, queryWindowHeapTrend, type QueryWindowRetention } from '../../e2e/query-window-heap'

const samples = (bytes: number[]) => bytes.map((usedSize, index) => ({ navigation: (index + 1) * 20, usedSize }))
const retained: QueryWindowRetention = { windows: 1, pages: 4, rows: 400, maxIndex: 100, cursors: 102, offsets: 102, records: 400, pins: 400, listeners: 2, selected: ['p19-project-0'], directoryBytes: 1_500_000, summaryBytes: 1_700_000 }

describe('browser retention guards', () => {
  it('allows a decaying warmup followed by a plateau', () => {
    const trend = queryWindowHeapTrend(samples([76e6, 78e6, 79e6, 79.2e6, 79.4e6]))
    expect(trend.lateBytesPerNavigation).toBeLessThanOrEqual(trend.allowedBytesPerNavigation)
  })
  it('rejects accelerating retention in the second half', () => {
    const trend = queryWindowHeapTrend(samples([76e6, 76.1e6, 76.2e6, 78.2e6, 80.2e6]))
    expect(trend.lateBytesPerNavigation).toBeGreaterThan(trend.allowedBytesPerNavigation)
  })
  it('does not turn an isolated development-stack valley into sustained growth', () => {
    const trend = queryWindowHeapTrend(samples([79473008, 80011268, 76392140, 76885432, 80775872]))
    expect(trend.lateBytesPerNavigation).toBeLessThanOrEqual(trend.allowedBytesPerNavigation)
  })
  it('does not accept missing or nonfinite measurements', () => {
    expect(() => queryWindowHeapTrend(samples([1, 2, 3]))).toThrow()
    expect(() => queryWindowHeapTrend(samples([1, 2, NaN, 4, 5]))).toThrow()
  })
  it('accepts bounded windows and cursor bookkeeping', () => {
    expect(() => checkQueryWindowRetention(retained, 100, 2)).not.toThrow()
  })
  it.each(['windows', 'pages', 'rows', 'records', 'pins', 'listeners', 'directoryBytes', 'summaryBytes'] as const)('rejects a growing %s independently of aggregate heap noise', field => {
    const maximum = { windows: 1, pages: 4, rows: 400, records: 400, pins: 400, listeners: 2, directoryBytes: 8 * 1024 * 1024, summaryBytes: 16 * 1024 * 1024 }
    expect(() => checkQueryWindowRetention({ ...retained, [field]: maximum[field] + 1 }, 100, 2)).toThrow()
  })
})

import { afterEach, expect, it, vi } from 'vitest'
import { clearScaleP19ProjectHandoff, readScaleP19ProjectHandoff, writeScaleP19ProjectHandoff } from './scale-p19-project-handoff'

afterEach(() => vi.useRealTimers())
it('scale-p19 preserves transient view state across legacy fallback without sharing it with another scope', () => {
  vi.useFakeTimers()
  const state = { filters: [], extraFilters: [], selectedIds: ['p-100'], sidebarOpen: true, insightMode: 'health' as const, insightFilter: null }
  writeScaleP19ProjectHandoff('workspace:member:view', state)
  expect(readScaleP19ProjectHandoff('other:member:view')).toBeUndefined()
  expect(readScaleP19ProjectHandoff('workspace:other:view')).toBeUndefined()
  expect(readScaleP19ProjectHandoff('workspace:member:view')).toBe(state)
  clearScaleP19ProjectHandoff('workspace:member:view')
  expect(readScaleP19ProjectHandoff('workspace:member:view')).toBeUndefined()
  writeScaleP19ProjectHandoff('workspace:member:view', state)
  vi.advanceTimersByTime(30_001)
  expect(readScaleP19ProjectHandoff('workspace:member:view')).toBeUndefined()
})

import { afterEach, expect, it, vi } from 'vitest'
import { clearProjectWindowHandoff, readProjectWindowHandoff, writeProjectWindowHandoff } from './project-window-handoff'

afterEach(() => vi.useRealTimers())
it('preserves transient view state across legacy fallback without sharing it with another scope', () => {
  vi.useFakeTimers()
  const state = { filters: [], extraFilters: [], selectedIds: ['p-100'], sidebarOpen: true, insightMode: 'health' as const, insightFilter: null }
  writeProjectWindowHandoff('workspace:member:view', state)
  expect(readProjectWindowHandoff('other:member:view')).toBeUndefined()
  expect(readProjectWindowHandoff('workspace:other:view')).toBeUndefined()
  expect(readProjectWindowHandoff('workspace:member:view')).toBe(state)
  clearProjectWindowHandoff('workspace:member:view')
  expect(readProjectWindowHandoff('workspace:member:view')).toBeUndefined()
  writeProjectWindowHandoff('workspace:member:view', state)
  vi.advanceTimersByTime(30_001)
  expect(readProjectWindowHandoff('workspace:member:view')).toBeUndefined()
})

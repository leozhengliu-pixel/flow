import { act, fireEvent, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useSidebarLayout } from './use-sidebar-layout'

vi.mock('motion/react', async importOriginal => ({
  ...await importOriginal<typeof import('motion/react')>(),
  useReducedMotion: () => false,
  animate: (value: { set: (value: number) => void }, next: number) => { value.set(next); return { stop: vi.fn() } },
}))

let compact = false
let media: EventTarget
beforeEach(() => {
  localStorage.clear()
  compact = false
  media = Object.assign(new EventTarget(), { media: '(max-width: 1024px)' })
  Object.defineProperty(media, 'matches', { get: () => compact })
  vi.stubGlobal('matchMedia', () => media)
})
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })

it('opens a fresh installation at 244px and discards the old accidental icon rail', () => {
  const first = renderHook(() => useSidebarLayout(false))
  expect(first.result.current.width).toBe(244)
  expect(first.result.current.collapsed).toBe(false)
  first.unmount()
  localStorage.setItem('flow.sidebar.width', '52')
  const migrated = renderHook(() => useSidebarLayout(false))
  expect(migrated.result.current.width).toBe(244)
  expect(migrated.result.current.collapsed).toBe(false)
})

it('keeps resized width when collapsing, reopening and remounting', () => {
  localStorage.setItem('flow.sidebar.width', '300')
  const hook = renderHook(() => useSidebarLayout(false))
  act(() => hook.result.current.toggle())
  expect(document.documentElement.style.getPropertyValue('--sidebar')).toBe('8px')
  expect(hook.result.current.visible).toBe(false)
  expect(localStorage.getItem('flow.sidebar.width')).toBe('300')
  hook.unmount()
  const remounted = renderHook(() => useSidebarLayout(false))
  expect(remounted.result.current.collapsed).toBe(true)
  act(() => remounted.result.current.toggle())
  expect(remounted.result.current.width).toBe(300)
  expect(document.documentElement.style.getPropertyValue('--sidebar')).toBe('300px')
})

it('opens an edge preview after 250ms and closes it when the pointer leaves', () => {
  vi.useFakeTimers()
  localStorage.setItem('flow.sidebar.collapsed', 'true')
  const hook = renderHook(() => useSidebarLayout(false))
  act(() => hook.result.current.onEdgeEnter())
  act(() => vi.advanceTimersByTime(249))
  expect(hook.result.current.visible).toBe(false)
  act(() => vi.advanceTimersByTime(1))
  expect(hook.result.current.visible).toBe(true)
  expect(hook.result.current.collapsed).toBe(true)
  expect(document.documentElement.style.getPropertyValue('--sidebar')).toBe('8px')
  fireEvent.pointerMove(document.body)
  expect(hook.result.current.visible).toBe(false)
})

it('auto-hides at the compact breakpoint without overwriting the desktop preference', () => {
  const hook = renderHook(() => useSidebarLayout(false))
  act(() => { compact = true; media.dispatchEvent(new Event('change')) })
  expect(hook.result.current.compact).toBe(true)
  expect(hook.result.current.visible).toBe(false)
  expect(hook.result.current.surfaceWidth).toBe(330)
  act(() => hook.result.current.toggle())
  expect(hook.result.current.visible).toBe(true)
  expect(hook.result.current.collapsed).toBe(false)
  fireEvent.pointerMove(document.body)
  expect(hook.result.current.visible).toBe(true)
  act(() => hook.result.current.onNavigate())
  expect(hook.result.current.visible).toBe(false)
  act(() => { compact = false; media.dispatchEvent(new Event('change')) })
  expect(hook.result.current.visible).toBe(true)
  expect(hook.result.current.width).toBe(244)
})

it('ignores the collapse shortcut while entering text', () => {
  const hook = renderHook(() => useSidebarLayout(false))
  const input = document.createElement('input')
  document.body.append(input)
  fireEvent.keyDown(input, { key: '[' })
  expect(hook.result.current.collapsed).toBe(false)
  input.remove()
  fireEvent.keyDown(window, { key: '[' })
  expect(hook.result.current.collapsed).toBe(true)
})

it('toggles a desktop preview from the header button without changing the pinned preference', () => {
  localStorage.setItem('flow.sidebar.collapsed', 'true')
  const hook = renderHook(() => useSidebarLayout(false))
  const trigger = document.createElement('button')
  trigger.dataset.sidebarTrigger = 'true'
  document.body.append(trigger)
  fireEvent.pointerOver(trigger, { pointerType: 'mouse' })
  expect(hook.result.current.floatingOpen).toBe(true)
  fireEvent.click(trigger)
  expect(hook.result.current.floatingOpen).toBe(false)
  fireEvent.pointerOver(trigger, { pointerType: 'mouse' })
  expect(hook.result.current.floatingOpen).toBe(false)
  fireEvent.click(trigger)
  expect(hook.result.current.floatingOpen).toBe(true)
  expect(hook.result.current.collapsed).toBe(true)
  trigger.remove()
})

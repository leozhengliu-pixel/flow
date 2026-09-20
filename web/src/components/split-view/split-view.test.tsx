import type { ReactNode } from 'react'
import { act, fireEvent, render, screen, renderHook } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  LEGACY_INBOX_LIST_WIDTH_KEY,
  SPLIT_VIEW_LIST_WIDTH_KEYS,
  persistSplitViewListWidth,
  readSplitViewListWidth,
  resolveSplitViewOrigin,
  SplitView,
  SplitViewList,
  useIsSplitView,
  useSplitViewListActivation,
} from './index'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
;(globalThis as unknown as { ResizeObserver: typeof ResizeObserverStub }).ResizeObserver = ResizeObserverStub

afterEach(() => {
  window.localStorage.clear()
})

describe('SplitView widths', () => {
  it('reads and persists shared list-width keys including inbox legacy fallback', () => {
    window.localStorage.setItem(LEGACY_INBOX_LIST_WIDTH_KEY, '333')
    expect(readSplitViewListWidth('inbox')).toBe(333)
    persistSplitViewListWidth('inbox', 340)
    expect(window.localStorage.getItem(SPLIT_VIEW_LIST_WIDTH_KEYS.inbox)).toBe('340')
    expect(window.localStorage.getItem(LEGACY_INBOX_LIST_WIDTH_KEY)).toBe('340')
  })
})

describe('SplitViewOriginHelper', () => {
  it('resolves my-issues / team / triage / inbox origins', () => {
    expect(resolveSplitViewOrigin('acme', { type: 'myIssues' })?.breadcrumb.label).toBe('My issues')
    expect(resolveSplitViewOrigin('acme', { type: 'teamView', teamKey: 'FLOW', viewKind: 'active' })?.rootPath).toContain('/team/FLOW/')
    expect(resolveSplitViewOrigin('acme', { type: 'triage', teamKey: 'FLOW' })?.rootPath).toContain('/triage')
    expect(resolveSplitViewOrigin('acme', { type: 'inbox' })?.viewKind).toBe('inbox')
  })
})

describe('SplitView chrome', () => {
  it('renders list and detail with a resize separator', () => {
    render(
      <SplitView surface="inbox" list={<SplitViewList title="Inbox">rows</SplitViewList>} detail={<div>Detail</div>} />,
    )
    expect(screen.getByRole('heading', { name: 'Inbox' })).toBeInTheDocument()
    expect(screen.getByText('Detail')).toBeInTheDocument()
    expect(screen.getByRole('separator', { name: 'Resize list' })).toBeInTheDocument()
  })

  it('resizes the list with keyboard', () => {
    persistSplitViewListWidth('inbox', 300)
    const { container } = render(
      <SplitView surface="inbox" list={<div>List</div>} detail={<div>Detail</div>} />,
    )
    const root = container.querySelector('[data-split-view]') as HTMLElement
    Object.defineProperty(root, 'getBoundingClientRect', {
      value: () => ({ left: 0, width: 1200, top: 0, height: 800, right: 1200, bottom: 800, x: 0, y: 0, toJSON() {} }),
    })
    fireEvent.keyDown(screen.getByRole('separator', { name: 'Resize list' }), { key: 'ArrowRight' })
    expect(Number(window.localStorage.getItem(SPLIT_VIEW_LIST_WIDTH_KEYS.inbox))).toBeGreaterThanOrEqual(300)
  })
})

describe('useIsSplitView', () => {
  it('tracks the desktop breakpoint', () => {
    let matches = true
    const listeners = new Set<() => void>()
    const media = {
      get matches() {
        return matches
      },
      addEventListener: (_event: string, cb: () => void) => {
        listeners.add(cb)
      },
      removeEventListener: (_event: string, cb: () => void) => {
        listeners.delete(cb)
      },
    }
    vi.stubGlobal('matchMedia', vi.fn(() => media))
    const { result } = renderHook(() => useIsSplitView(true))
    expect(result.current).toBe(true)
    matches = false
    act(() => {
      listeners.forEach(listener => listener())
    })
    expect(result.current).toBe(false)
    vi.unstubAllGlobals()
  })
})

describe('useSplitViewListActivation', () => {
  it('activates the current item key while split is enabled', () => {
    const onActivate = vi.fn()
    const wrapper = ({ children }: { children: ReactNode }) => (
      <MemoryRouter initialEntries={['/inbox/1']}>{children}</MemoryRouter>
    )
    renderHook(
      () =>
        useSplitViewListActivation({
          isSplitView: true,
          currentItemKey: '1',
          onActivate,
        }),
      { wrapper },
    )
    expect(onActivate).toHaveBeenCalledWith('1')
  })
})

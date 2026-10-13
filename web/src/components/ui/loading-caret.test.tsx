import { readFileSync } from 'node:fs'
import { act, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { LoadingCaret, LoadingContainer, LoadingListFooter } from './loading-caret'
import { useLoadingState } from './loading-state'

const css = readFileSync('src/components/ui/loading-caret.css', 'utf8')

function Probe({ loading }: { loading: boolean }) {
  return <output>{useLoadingState(loading) ? 'loading' : 'ready'}</output>
}

describe('loading caret', () => {
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date('2026-10-13T00:00:00Z')) })
  afterEach(() => { vi.useRealTimers() })

  it('is a status with an accessible name and no visible text', () => {
    render(<LoadingCaret label="Loading issues"/>)
    const caret = screen.getByRole('status', { name: 'Loading issues' })
    expect(caret).toHaveClass('flow-loading-caret')
    expect(caret).toHaveTextContent('')
    expect(caret).not.toHaveAttribute('style')
  })

  it('appears after 350ms in a list footer', () => {
    render(<LoadingListFooter label="Loading issues"/>)
    const caret = screen.getByRole('status', { name: 'Loading issues' })
    expect(caret.parentElement).toHaveClass('flow-loading-footer')
    expect(caret).toHaveStyle({ animationDelay: '350ms' })
  })

  it('keeps the measured geometry, colour token and blink timing', () => {
    expect(css).toContain('.flow-loading-caret{width:8px;height:16px;flex:0 0 auto;border-radius:2px;background:var(--loading-caret);opacity:0;animation:flow-loading-caret 1s var(--ease) 1s infinite')
    expect(css).toContain('.flow-loading-caret.is-positioned{position:absolute;top:14px;left:10px')
    expect(css).toContain('@keyframes flow-loading-caret{0%,40%,100%{opacity:1}50%,90%{opacity:.2}}')
    expect(css).toContain('.flow-loading-content.is-loading{opacity:0;will-change:opacity;backface-visibility:hidden;transition-duration:.1s;transition-delay:.5s}')
    expect(css).toContain('.flow-loading-footer{height:50px')
    expect(css).toContain('.flow-loading-footer>.flow-loading-caret{margin:14px 0 0 24px}')
    const tokens = readFileSync('src/styles/tokens.css', 'utf8')
    expect(tokens).toContain(':root{--loading-caret:lch(90.451% 1.2 272)}')
    expect(tokens).toContain(':root[data-theme="light"]{--loading-caret:lch(19.588% 1.25 282)}')
  })

  it('hides a load that finishes within 900ms immediately', () => {
    const { rerender } = render(<Probe loading/>)
    expect(screen.getByText('loading')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(500) })
    rerender(<Probe loading={false}/>)
    expect(screen.getByText('ready')).toBeInTheDocument()
  })

  it('keeps a load that finishes between 900ms and 1500ms until 1500ms', () => {
    const { rerender } = render(<Probe loading/>)
    act(() => { vi.advanceTimersByTime(1000) })
    rerender(<Probe loading={false}/>)
    expect(screen.getByText('loading')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(499) })
    expect(screen.getByText('loading')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(1) })
    expect(screen.getByText('ready')).toBeInTheDocument()
  })

  it('hides a slow load as soon as it finishes and restarts the clock for the next load', () => {
    const { rerender } = render(<Probe loading/>)
    act(() => { vi.advanceTimersByTime(2000) })
    rerender(<Probe loading={false}/>)
    expect(screen.getByText('ready')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(5000) })
    rerender(<Probe loading/>)
    act(() => { vi.advanceTimersByTime(300) })
    rerender(<Probe loading={false}/>)
    expect(screen.getByText('ready')).toBeInTheDocument()
  })

  it('makes loading content inert and fades it while the caret shows', () => {
    const { rerender, container } = render(<LoadingContainer loading label="Loading issues"><button>Previous result</button></LoadingContainer>)
    const frame = container.firstElementChild as HTMLElement
    expect(frame).toHaveClass('flow-loading-frame')
    expect(frame).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByRole('status', { name: 'Loading issues' })).toHaveClass('is-positioned')
    const content = screen.getByText('Previous result').parentElement as HTMLElement
    expect(content).toHaveClass('flow-loading-content', 'is-loading')
    expect(content).toHaveAttribute('inert')
    rerender(<LoadingContainer loading={false} label="Loading issues"><button>Previous result</button></LoadingContainer>)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(content).not.toHaveClass('is-loading')
    expect(content).not.toHaveAttribute('inert')
    expect(frame).not.toHaveAttribute('aria-busy')
  })

  it('can keep the content visible while loading', () => {
    render(<LoadingContainer loading keepContentVisible><span>Kept</span></LoadingContainer>)
    expect(screen.getByText('Kept').parentElement).not.toHaveClass('is-loading')
    expect(screen.getByRole('status', { name: 'Loading…' })).toBeInTheDocument()
  })
})

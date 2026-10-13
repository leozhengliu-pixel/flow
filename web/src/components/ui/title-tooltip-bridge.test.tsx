import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { TitleTooltipBridge } from './title-tooltip-bridge'

describe('TitleTooltipBridge', () => {
  afterEach(() => vi.useRealTimers())

  it('shows native titles as Flow tooltips after the delay and restores the attribute', () => {
    vi.useFakeTimers()
    render(<><TitleTooltipBridge/><button type="button" title="Copy link"><svg/></button><span title="Plain text">Label</span></>)
    const button = screen.getByRole('button')
    fireEvent.pointerOver(button, { pointerType: 'mouse' })
    expect(button).not.toHaveAttribute('title')
    expect(button).toHaveAccessibleName('Copy link')
    expect(screen.queryByRole('tooltip')).toBeNull()
    act(() => vi.advanceTimersByTime(450))
    expect(screen.getByRole('tooltip')).toHaveTextContent('Copy link')
    expect(screen.getByRole('tooltip')).toHaveClass('flow-tooltip-content')

    // Moving straight to another titled element skips the delay.
    fireEvent.pointerOut(button, { relatedTarget: screen.getByText('Label') })
    expect(button).toHaveAttribute('title', 'Copy link')
    expect(button).not.toHaveAttribute('aria-label')
    fireEvent.pointerOver(screen.getByText('Label'), { pointerType: 'mouse' })
    expect(screen.getByRole('tooltip')).toHaveTextContent('Plain text')
    fireEvent.pointerDown(screen.getByText('Label'))
    expect(screen.queryByRole('tooltip')).toBeNull()
  })

  it('uses the lighter palette for titles over elevated and raised surfaces', () => {
    vi.useFakeTimers()
    render(<><TitleTooltipBridge/><div data-tooltip-elevated><button type="button" title="Attach"><svg/></button></div><div data-tooltip-raised><button type="button" title="Close"><svg/></button></div></>)
    fireEvent.pointerOver(screen.getByRole('button', { name: 'Attach' }), { pointerType: 'mouse' })
    act(() => vi.advanceTimersByTime(450))
    expect(screen.getByRole('tooltip')).toHaveClass('flow-tooltip-content--elevated')
    fireEvent.pointerOut(screen.getByRole('button', { name: 'Attach' }), { relatedTarget: document.body })
    fireEvent.pointerOver(screen.getByRole('button', { name: 'Close' }), { pointerType: 'mouse' })
    act(() => vi.advanceTimersByTime(450))
    expect(screen.getByRole('tooltip')).toHaveClass('flow-tooltip-content--raised')
  })

  it('leaves opted-out elements alone', () => {
    render(<><TitleTooltipBridge/><div data-native-title><button type="button" title="Native">x</button></div></>)
    fireEvent.pointerOver(screen.getByRole('button'), { pointerType: 'mouse' })
    expect(screen.getByRole('button')).toHaveAttribute('title', 'Native')
  })
})

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

  it('shows data-tooltip-text without rewriting the element', () => {
    vi.useFakeTimers()
    render(<><TitleTooltipBridge/><a href="https://example.com" data-tooltip-text="example.com">site</a></>)
    const link = screen.getByRole('link')
    fireEvent.pointerOver(link, { pointerType: 'mouse' })
    expect(link.getAttributeNames().sort()).toEqual(['data-tooltip-text', 'href'])
    act(() => vi.advanceTimersByTime(450))
    expect(screen.getByRole('tooltip')).toHaveTextContent('example.com')
    fireEvent.pointerOut(link, { relatedTarget: document.body })
    expect(link.getAttributeNames().sort()).toEqual(['data-tooltip-text', 'href'])
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

  it('renders a trailing key combination in a title as keycaps', () => {
    vi.useFakeTimers()
    render(<><TitleTooltipBridge/><button type="button" title="Open details (⌘I)"><svg/></button><button type="button" title="Go to inbox (G then I)"><svg/></button><button type="button" title="Copy (1)"><svg/></button></>)
    const [details, inbox, plain] = screen.getAllByRole('button')
    fireEvent.pointerOver(details, { pointerType: 'mouse' })
    act(() => vi.advanceTimersByTime(450))
    const tooltip = screen.getByRole('tooltip')
    expect(tooltip.querySelector('.flow-tooltip-copy')).toHaveTextContent(/^Open details$/)
    expect([...tooltip.querySelectorAll('kbd.flow-tooltip-key')].map(key => key.textContent)).toEqual(['⌘', 'I'])
    fireEvent.pointerOut(details, { relatedTarget: inbox })
    fireEvent.pointerOver(inbox, { pointerType: 'mouse' })
    expect([...screen.getByRole('tooltip').querySelectorAll('.flow-tooltip-shortcut > *')].map(node => node.textContent)).toEqual(['G', 'then', 'I'])
    fireEvent.pointerOut(inbox, { relatedTarget: plain })
    fireEvent.pointerOver(plain, { pointerType: 'mouse' })
    expect(screen.getByRole('tooltip')).toHaveTextContent('Copy (1)')
    expect(screen.getByRole('tooltip').querySelector('kbd')).toBeNull()
  })

  it('opens beside an element marked data-tooltip-side="right"', () => {
    vi.useFakeTimers()
    render(<><TitleTooltipBridge/><a href="#home" title="Go to pulse (G then F)" data-tooltip-side="right">Pulse</a></>)
    const link = screen.getByRole('link')
    link.getBoundingClientRect = () => ({ x: 12, y: 60, left: 12, top: 60, right: 232, bottom: 88, width: 220, height: 28, toJSON() {} }) as DOMRect
    fireEvent.pointerOver(link, { pointerType: 'mouse' })
    act(() => vi.advanceTimersByTime(450))
    expect(screen.getByRole('tooltip')).toHaveAttribute('data-side', 'right')
    expect(screen.getByRole('tooltip').style.left).toBe('240px')
  })
})

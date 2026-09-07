import { act, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Globals } from '@react-spring/web'
import { AnimatedMilestones, useExitPresence } from './motion'
import { motionPresets } from '@/lib/motion-presets'

const preference = vi.hoisted(() => ({ reduced: false }))
vi.mock('motion/react', async importOriginal => ({ ...await importOriginal<typeof import('motion/react')>(), useReducedMotion: () => preference.reduced }))

function Presence({ open }: { open: boolean }) {
  const present = useExitPresence(open)
  return present ? <div data-testid="surface" aria-hidden={!open}>Content</div> : null
}

afterEach(() => { preference.reduced = false; Globals.assign({ skipAnimation: false }); vi.useRealTimers() })

describe('motion lifecycle', () => {
  it('retains the surface until exit finishes', () => {
    vi.useFakeTimers()
    const { rerender } = render(<Presence open/>)
    rerender(<Presence open={false}/>)
    expect(screen.getByTestId('surface')).toHaveAttribute('aria-hidden', 'true')
    act(() => vi.advanceTimersByTime(419))
    expect(screen.getByTestId('surface')).toBeInTheDocument()
    act(() => vi.advanceTimersByTime(1))
    expect(screen.queryByTestId('surface')).not.toBeInTheDocument()
  })

  it('cancels a pending exit on a rapid reopen', () => {
    vi.useFakeTimers()
    const { rerender } = render(<Presence open/>)
    rerender(<Presence open={false}/>)
    act(() => vi.advanceTimersByTime(100))
    rerender(<Presence open/>)
    act(() => vi.advanceTimersByTime(500))
    expect(screen.getByTestId('surface')).toHaveAttribute('aria-hidden', 'false')
  })

  it('does not retain closed surfaces with reduced motion', () => {
    preference.reduced = true
    const { rerender } = render(<Presence open/>)
    rerender(<Presence open={false}/>)
    expect(screen.queryByTestId('surface')).not.toBeInTheDocument()
  })

  it('keeps milestone identity through reordering and removes reduced-motion exits', async () => {
    preference.reduced = true
    Globals.assign({ skipAnimation: true })
    const a = { id: 'a', title: 'Alpha' }, b = { id: 'b', title: 'Beta' }
    const renderItems = (items: typeof a[]) => <AnimatedMilestones items={items}>{item => <span>{item.title}</span>}</AnimatedMilestones>
    const { rerender } = render(renderItems([a,b]))
    const beta = screen.getByText('Beta')
    rerender(renderItems([b,a]))
    expect(screen.getByText('Beta')).toBe(beta)
    rerender(renderItems([b]))
    await waitFor(() => expect(screen.queryByText('Alpha')).not.toBeInTheDocument())
  })

  it('uses separate spring profiles for enter, exit and milestones', () => {
    expect(motionPresets.popoverEnter).toEqual({ tension: 1500, friction: 100, precision: 0.01 })
    expect(motionPresets.popoverExit.tension).toBe(2000)
    expect(motionPresets.milestone).toEqual(motionPresets.popoverExit)
    expect(motionPresets.createEnter.duration).toBe(0.35)
    expect(motionPresets.createExit.duration).toBe(0.4)
  })
})

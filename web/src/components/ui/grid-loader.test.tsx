import { render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GridLoader } from './grid-loader'
import { GRID_LOADER_FRAMES, type GridLoaderVariant } from './grid-loader-frames'

const counts: Record<GridLoaderVariant, number> = { scope: 12, upDown: 14, pong: 8, blowOut: 9, ufo: 5, down: 7, zap: 1, hourglass: 5, stats: 9, cat: 11, agent: 16, read: 1, unread: 1, outlines: 8 }

afterEach(() => vi.restoreAllMocks())

describe('GridLoader', () => {
  it('has the expected frame count for every variant', () => {
    for (const [variant, count] of Object.entries(counts)) expect(GRID_LOADER_FRAMES[variant as GridLoaderVariant]).toHaveLength(count)
  })

  it('renders a 16x16 grid of 25 dots in the static fallback', () => {
    const { container } = render(<GridLoader />)
    const svg = container.querySelector('svg')!
    expect(svg).toHaveAttribute('viewBox', '0 0 16 16')
    expect(svg).toHaveAttribute('width', '16')
    expect(svg).toHaveAttribute('data-variant', 'scope')
    const dots = svg.querySelectorAll('circle')
    expect(dots).toHaveLength(25)
    expect(dots[0]).toHaveAttribute('cx', '1')
    expect(dots[0]).toHaveAttribute('cy', '1')
    expect(dots[6]).toHaveAttribute('cx', '4.5')
    expect(dots[6]).toHaveAttribute('cy', '4.5')
    expect(dots[24]).toHaveAttribute('cx', '15')
    expect(dots[24]).toHaveAttribute('r', '1')
  })

  it('lights the dots in the initial frame mask, row-major from the top left', () => {
    // agent frame 3 is 39 = 0b100111 -> dots 0, 1, 2 and 5.
    const { container } = render(<GridLoader variant="agent" initialFrame={3} />)
    const lit = [...container.querySelectorAll('circle')].flatMap((dot, index) => dot.getAttribute('data-lit') === 'true' ? [index] : [])
    expect(lit).toEqual([0, 1, 2, 5])
  })

  it('lights bits above 2**24 -free masks correctly and wraps the initial frame', () => {
    const { container } = render(<GridLoader variant="stats" initialFrame={-1} />)
    const lit = [...container.querySelectorAll('circle')].filter(dot => dot.getAttribute('data-lit') === 'true')
    expect(lit).toHaveLength(0)
  })

  it('is decorative without a label and a named status with one', () => {
    const { container, rerender } = render(<GridLoader />)
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
    expect(container.querySelector('svg')).not.toHaveAttribute('role')
    rerender(<GridLoader label="Saving" />)
    const svg = container.querySelector('svg')!
    expect(svg).toHaveAttribute('role', 'status')
    expect(svg).toHaveAttribute('aria-label', 'Saving')
    expect(svg).not.toHaveAttribute('aria-hidden')
  })

  it('scales the svg with size and forwards className and colour', () => {
    const { container } = render(<GridLoader size={32} className="x" color="red" />)
    const svg = container.querySelector('svg')!
    expect(svg).toHaveAttribute('width', '32')
    expect(svg).toHaveAttribute('height', '32')
    expect(svg).toHaveClass('gli', 'x')
    expect(svg.style.color).toBe('red')
  })

  it('draws a sprite strip animated with the frame interval when a canvas is available', () => {
    const calls: unknown[][] = []
    const context = { scale: vi.fn(), beginPath: vi.fn(), arc: vi.fn((...args: unknown[]) => calls.push(args)), fill: vi.fn(), globalAlpha: 1, fillStyle: '' }
    vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue('Mozilla/5.0 Chrome/140')
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(context as unknown as CanvasRenderingContext2D)
    const toDataURL = vi.spyOn(HTMLCanvasElement.prototype, 'toDataURL').mockReturnValue('data:image/png;base64,AAAA')
    const { container } = render(<GridLoader variant="pong" interval={100} initialFrame={2} />)
    const image = container.querySelector('image')!
    expect(toDataURL).toHaveBeenCalled()
    expect(image.getAttribute('href')).toBe('data:image/png;base64,AAAA')
    expect(image).toHaveAttribute('width', String(8 * 16))
    expect(image.style.animationDuration).toBe('800ms')
    expect(image.style.animationTimingFunction).toBe('steps(8)')
    expect(image.style.animationDelay).toBe('-200ms')
    expect(container.querySelector('svg')).toHaveAttribute('data-sprite')
    // 25 dim dots plus the lit dots of every frame were drawn.
    const litTotal = GRID_LOADER_FRAMES.pong.reduce((sum, mask) => sum + mask.toString(2).replace(/0/g, '').length, 0)
    expect(calls).toHaveLength(8 * 25 + litTotal)
  })
})

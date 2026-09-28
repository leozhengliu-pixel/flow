import { describe, expect, it, vi } from 'vitest'
import { ChartHover } from './chart-hover'

describe('ChartHover (LS-0105)', () => {
  it('prefers hover over drill for active highlight', () => {
    const hover = new ChartHover('table')
    hover.setDrill({ slice: 'todo' })
    expect(hover.active).toEqual({ slice: 'todo' })
    hover.setHover({ slice: 'todo', segment: 'high' })
    expect(hover.active).toEqual({ slice: 'todo', segment: 'high' })
    expect(hover.highlight({ slice: 'todo', segment: 'high' })).toBe('strong')
    expect(hover.highlight({ slice: 'todo', segment: 'low' })).toBe('weak')
    hover.clearHover()
    expect(hover.active).toEqual({ slice: 'todo' })
  })

  it('tracks origin and notifies subscribers', () => {
    const hover = new ChartHover('graph')
    const spy = vi.fn()
    hover.subscribe(spy)
    hover.setHover({ segment: 'a' }, 'widget')
    expect(hover.state.origin).toBe('widget')
    expect(spy).toHaveBeenCalledWith(expect.objectContaining({ origin: 'widget', hover: { segment: 'a' } }))
  })

  it('toggles drill and exposes filter helpers', () => {
    const hover = new ChartHover('distribution')
    hover.toggleDrill({ slice: 's', segment: 'seg' })
    expect(hover.isFilteringDimension('s')).toBe(true)
    expect(hover.isFilteringSegment('seg')).toBe(true)
    expect(hover.isFilteringCell({ slice: 's', segment: 'seg' })).toBe(true)
    hover.toggleDrill({ slice: 's', segment: 'seg' })
    expect(hover.active).toBeUndefined()
  })
})

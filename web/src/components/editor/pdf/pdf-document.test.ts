import { afterEach, describe, expect, it, vi } from 'vitest'
import { canvasDensity, MAX_CANVAS_PIXELS, needsTiles, planTileRegion, regionCovers } from './pdf-document'

afterEach(() => { vi.unstubAllGlobals() })
const density = (value: number) => vi.stubGlobal('devicePixelRatio', value)

describe('needsTiles', () => {
  it('only splits a page whose full bitmap at screen density passes the cap', () => {
    density(2)
    expect(needsTiles(816, 1056)).toBe(false)
    expect(needsTiles(1632, 2112)).toBe(false)
    expect(needsTiles(3264, 4224)).toBe(true)
    density(1)
    expect(needsTiles(3264, 4224)).toBe(false)
  })
})

describe('canvasDensity', () => {
  it('is the screen density (capped at 3) and drops only to respect the pixel cap', () => {
    density(2)
    expect(canvasDensity(816, 1056)).toBe(2)
    density(5)
    expect(canvasDensity(816, 1056)).toBe(3)
    density(2)
    const reduced = canvasDensity(3264, 4224)
    expect(reduced).toBeLessThan(2)
    expect(Math.floor(3264 * reduced) * Math.floor(4224 * reduced)).toBeLessThanOrEqual(MAX_CANVAS_PIXELS)
  })
})

describe('planTileRegion', () => {
  it('covers the view with half a view of margin, on a 64px grid, inside the page', () => {
    density(2)
    const region = planTileRegion({ x: 1000, y: 2000, width: 1400, height: 700 }, 3264, 4224)
    expect(region).toEqual({ x: 256, y: 1600, width: 2880, height: 1472 })
    expect(region.x % 64).toBe(0)
    expect(regionCovers(region, { x: 1000, y: 2000, width: 1400, height: 700 })).toBe(true)
    expect(region.width * region.height * 4).toBeLessThanOrEqual(MAX_CANVAS_PIXELS)
  })

  it('is clipped at the page edges', () => {
    density(2)
    const top = planTileRegion({ x: 0, y: 0, width: 1400, height: 700 }, 3264, 4224)
    expect(top.x).toBe(0)
    expect(top.y).toBe(0)
    const bottom = planTileRegion({ x: 1864, y: 3524, width: 1400, height: 700 }, 3264, 4224)
    expect(bottom.x + bottom.width).toBe(3264)
    expect(bottom.y + bottom.height).toBe(4224)
  })

  it('shrinks the margin so a big window still renders at full density under the cap', () => {
    density(2)
    const view = { x: 0, y: 1000, width: 2400, height: 1500 }
    const region = planTileRegion(view, 6528, 8448)
    expect(region.width * region.height * 4).toBeLessThanOrEqual(MAX_CANVAS_PIXELS)
    expect(regionCovers(region, view)).toBe(true)
  })

  it('keeps an empty strip at an edge renderable', () => {
    density(2)
    const region = planTileRegion({ x: 0, y: 4224, width: 1, height: 1 }, 3264, 4224)
    expect(region.width).toBeGreaterThan(0)
    expect(region.height).toBeGreaterThan(0)
  })
})

describe('regionCovers', () => {
  it('is true only for a rectangle fully inside', () => {
    const outer = { x: 0, y: 0, width: 100, height: 100 }
    expect(regionCovers(outer, { x: 10, y: 10, width: 90, height: 90 })).toBe(true)
    expect(regionCovers(outer, { x: 10, y: 10, width: 91, height: 90 })).toBe(false)
    expect(regionCovers(outer, { x: -1, y: 0, width: 5, height: 5 })).toBe(false)
  })
})

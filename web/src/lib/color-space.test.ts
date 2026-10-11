import { describe, expect, it } from 'vitest'
import { hexToDisplayP3, hexToLch, lchToHex } from './color-space'

describe('color space (the reference app colour pad)', () => {
  it('converts hex to CIE LCH with the CSS Color 4 matrices, greys landing on hue 139.09', () => {
    const [l, c, h] = hexToLch('#bb87fc')!
    expect(l).toBeCloseTo(65.22, 1)
    expect(c).toBeCloseTo(63.79, 1)
    expect(h).toBeCloseTo(306.05, 1)
    expect(hexToLch('#e2e2e2')![2]).toBeCloseTo(139.09, 1)
    expect(hexToLch('nope')).toBeNull()
  })

  it('paints pad pixels: clipped to display-p3, then to sRGB', () => {
    // Pixels read from the reference app's 350×100 pad for #bb87fc (hue 306.05): x → chroma 0–132, y → lightness 100–0.
    const pad = (x: number, y: number) => lchToHex(100 - y, (x / 350) * 132, 306.049)
    expect(pad(0, 10)).toBe('#e2e2e2')
    expect(pad(120, 50)).toBe('#8b68b6')
    expect(pad(349, 50)).toBe('#9732ff')
    expect(pad(200, 70)).toBe('#5b28a8')
    // Hue strip: lightness 75, chroma 132, one hue per row.
    expect(lchToHex(75, 132, 0)).toBe('#ff00c3')
    expect(lchToHex(75, 132, 288)).toBe('#3aa8ff')
  })

  it('writes the pad thumb colour as display-p3', () => {
    expect(hexToDisplayP3('#bb87fc')).toBe('color(display-p3 0.702616 0.537823 0.960447)')
    expect(hexToDisplayP3('#f85911')).toBe('color(display-p3 0.90119 0.392601 0.184122)')
  })
})

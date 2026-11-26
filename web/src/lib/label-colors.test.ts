import { describe, expect, it } from 'vitest'
import { hexToLch, labelDisplayColor, labelHeaderTint } from './label-colors'

// Values read from the design reference's computed styles for the same label colours.
describe('label colours', () => {
  it('tints label group headers like the reference', () => {
    expect(labelHeaderTint('#EB5757', 'light')).toBe('lch(94.557% 2.396 8.46)')
    expect(labelHeaderTint('#F7C8C1', 'light')).toBe('lch(94.557% 2.335 9.897)')
    expect(labelHeaderTint('#26B5CE', 'light')).toBe('lch(94.557% 2.635 238.223)')
    expect(labelHeaderTint('#BB87FC', 'light')).toBe('lch(94.557% 3.402 297.116)')
    expect(labelHeaderTint('#4EA7FC', 'light')).toBe('lch(94.557% 3.849 267.34)')
    expect(labelHeaderTint('#BEC2C8', 'light')?.endsWith(' 279.067)')).toBe(true)
    expect(labelHeaderTint('#F7C8C1', 'dark')).toBe('lch(10.756% 3.755 11.454)')
    expect(labelHeaderTint('not a colour', 'light')).toBeUndefined()
    // Board rows tint against the page: the reference's row carets read lch(39.176 12 9.116) / lch(61.803 7 17.441).
    expect(labelHeaderTint('#F7C8C1', 'light', 'page')?.endsWith(' 9.116)')).toBe(true)
    expect(labelHeaderTint('#F7C8C1', 'dark', 'page')?.endsWith(' 17.441)')).toBe(true)
    // Board row headers: lch(94.557 2.335 9.897) in light (the header surface) and lch(9.064 4.272 12.928) in dark.
    expect(labelHeaderTint('#F7C8C1', 'light', 'row')).toBe('lch(94.557% 2.335 9.897)')
    expect(labelHeaderTint('#F7C8C1', 'dark', 'row')).toBe('lch(9.064% 4.272 12.928)')
    expect(labelHeaderTint('#EB5757', 'dark', 'row')).toBe('lch(9.064% 4.375 11.297)')
  })

  it('darkens low-contrast label dots in light and keeps them in dark', () => {
    expect(labelDisplayColor('#F7C8C1', 'light')).toBe('lch(67.969% 19.321 31.72)')
    expect(labelDisplayColor('#BEC2C8', 'light')).toBe('lch(67.969% 3.577 260.65)')
    expect(labelDisplayColor('#EB5757', 'light')).toBe('#EB5757')
    expect(labelDisplayColor('#F7C8C1', 'dark')).toBe('#F7C8C1')
    expect(labelDisplayColor('#F2C94C', 'light')).toBe('lch(80% 90 85)')
  })

  it('reads hex colours as CIE LCH (D50)', () => {
    expect(hexToLch('#F7C8C1')?.map(value => +value.toFixed(3))).toEqual([84.655, 19.321, 31.72])
    expect(hexToLch('#fff')?.[0]).toBeCloseTo(100, 3)
  })
})

import { describe, expect, it } from 'vitest'
import { estimateLabel, estimatePickerOptions, estimateScaleDetails, estimateValues, unestimatedValue } from './estimates'

describe('estimates', () => {
  it('builds each scale with optional zero and extended values', () => {
    expect(estimateValues({ estimateType: 'notUsed' })).toEqual([])
    expect(estimateValues({ estimateType: 'exponential' })).toEqual([1, 2, 4, 8, 16])
    expect(estimateValues({ estimateType: 'fibonacci', estimateAllowZero: true, estimateExtended: true })).toEqual([0, 1, 2, 3, 5, 8, 13, 21])
    expect(estimateValues({ estimateType: 'flow', estimateExtended: true })).toEqual([1, 2, 3, 4, 5, 6, 7])
  })

  it('labels t-shirt sizes and points', () => {
    expect(estimateLabel(5, 'tShirt')).toBe('L')
    expect(estimateLabel(0, 'tShirt')).toBe('-')
    expect(estimateLabel(1, 'fibonacci')).toBe('1 Point')
    expect(estimateScaleDetails('tShirt', true, true)).toBe('(-, XS, S, M, L, XL, XXL, XXXL)')
    expect(estimateScaleDetails('exponential')).toBe('(1, 2, 4, 8, 16 Points)')
  })

  it('offers a distinct "No estimate" option and counts unestimated as 1 by default', () => {
    expect(estimatePickerOptions({ estimateType: 'fibonacci', estimateAllowZero: true }).map(option => option.id).slice(0, 3)).toEqual(['-1', '0', '1'])
    expect(unestimatedValue(undefined)).toBe(1)
    expect(unestimatedValue({ estimateCountUnestimated: false })).toBe(0)
  })
})

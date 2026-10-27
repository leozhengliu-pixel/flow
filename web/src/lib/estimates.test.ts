import { describe, expect, it } from 'vitest'
import { estimateIconLevel, estimateLabel, estimatePickerOptions, estimatePoints, estimateScaleDetails, estimateShortLabel, estimateValues, formatEstimatePoints, sumEstimatePoints, teamUsesEstimates, unestimatedValue } from './estimates'

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

  it('labels the picker rows "No estimate", "1 Point" and "N Points"', () => {
    expect(estimatePickerOptions({ estimateType: 'fibonacci' }).map(option => option.label)).toEqual(['No estimate', '1 Point', '2 Points', '3 Points', '5 Points', '8 Points'])
    expect(estimateShortLabel(3, 'fibonacci')).toBe('3')
    expect(estimateShortLabel(3, 'tShirt')).toBe('M')
  })

  it('weights issues for sums: unestimated per the team option, zero only when allowed', () => {
    expect(estimatePoints(undefined, { estimateType: 'fibonacci' })).toBe(1)
    expect(estimatePoints(-1, { estimateType: 'fibonacci', estimateCountUnestimated: false })).toBe(0)
    expect(estimatePoints(0, { estimateType: 'fibonacci' })).toBe(1)
    expect(estimatePoints(0, { estimateType: 'fibonacci', estimateAllowZero: true })).toBe(0)
    expect(estimatePoints(5, { estimateType: 'fibonacci' })).toBe(5)
    const settings = { team: { estimateType: 'fibonacci' as const }, off: { estimateType: 'notUsed' as const } }
    const issues = [{ teamId: 'team', estimate: 3 }, { teamId: 'team', estimate: 5 }, { teamId: 'team' }, { teamId: 'off', estimate: 8 }]
    expect(sumEstimatePoints(issues, id => settings[id as keyof typeof settings])).toBe(9)
    expect(sumEstimatePoints([{ teamId: 'off', estimate: 8 }], id => settings[id as keyof typeof settings])).toBeUndefined()
    expect(teamUsesEstimates({ estimateType: 'notUsed' })).toBe(false)
    expect(formatEstimatePoints(2.25)).toBe('2.3')
  })

  it('fills the estimate icon in thirds of the scale', () => {
    const fibonacci = { estimateType: 'fibonacci' as const }
    expect([undefined, 0, 1, 2, 3, 5, 8].map(value => estimateIconLevel(value, fibonacci))).toEqual([0, 0, 1, 2, 2, 3, 3])
    expect(estimateIconLevel(16, { estimateType: 'exponential' })).toBe(3)
  })

  it('offers a distinct "No estimate" option and counts unestimated as 1 by default', () => {
    expect(estimatePickerOptions({ estimateType: 'fibonacci', estimateAllowZero: true }).map(option => option.id).slice(0, 3)).toEqual(['-1', '0', '1'])
    expect(unestimatedValue(undefined)).toBe(1)
    expect(unestimatedValue({ estimateCountUnestimated: false })).toBe(0)
  })
})

import { describe, expect, it } from 'vitest'
import {
  CombinedFilterMap,
  combineModelFilters,
  compareOptionsForValueType,
  findBlocks,
  fiscalYearBounds,
  getItemsFingerprint,
  normalizeFiscalYearStartMonth,
  rewriteWithinToIn,
  scoreFreeFormOptions,
  shouldSkipFilterScoring,
} from './filter-block-helper'
import type { FilterBlockDefinition } from './filter-block-types'

describe('combineModelFilters', () => {
  it('returns empty object for no filters', () => {
    expect(combineModelFilters('and', [])).toEqual({})
  })

  it('returns the single child unchanged', () => {
    const leaf = { field: 'status', operator: 'is', values: ['started'] }
    expect(combineModelFilters('and', [leaf])).toEqual(leaf)
  })

  it('flattens nested same-operator groups', () => {
    const a = { field: 'status', operator: 'is', values: ['a'] }
    const b = { field: 'priority', operator: 'is', values: ['1'] }
    const c = { field: 'assigneeId', operator: 'is', values: ['u1'] }
    expect(combineModelFilters('and', [{ and: [a, b] }, c])).toEqual({ and: [a, b, c] })
    expect(combineModelFilters('or', [{ or: [a, b] }, c])).toEqual({ or: [a, b, c] })
  })
})

describe('CombinedFilterMap', () => {
  it('OR-combines named subfilters', () => {
    const map = new CombinedFilterMap()
    map.update('freeform', { field: 'content', operator: 'contains', values: ['auth'] })
    const combined = map.update('status', { field: 'status', operator: 'is', values: ['started'] })
    expect(combined).toEqual({
      or: [
        { field: 'content', operator: 'contains', values: ['auth'] },
        { field: 'status', operator: 'is', values: ['started'] },
      ],
    })
    expect(map.remove('freeform')).toEqual({ field: 'status', operator: 'is', values: ['started'] })
    expect(map.remove('status')).toBeUndefined()
  })
})

describe('fiscal + freeform helpers', () => {
  it('normalizes fiscal year start month and bounds', () => {
    expect(normalizeFiscalYearStartMonth(15)).toBe(3)
    expect(normalizeFiscalYearStartMonth(-1)).toBe(11)
    const { start, end } = fiscalYearBounds(new Date(2026, 1, 15), 3) // Feb → previous FY starting Apr
    expect(start.getFullYear()).toBe(2025)
    expect(start.getMonth()).toBe(3)
    expect(end.getFullYear()).toBe(2026)
    expect(end.getMonth()).toBe(2)
  })

  it('rewrites within→in and scores freeform options', () => {
    expect(rewriteWithinToIn('within')).toBe('in')
    expect(rewriteWithinToIn('before')).toBe('before')
    expect(shouldSkipFilterScoring({ inputType: 'freeForm' })).toBe(true)
    expect(shouldSkipFilterScoring({ inputType: 'options' })).toBe(false)
    const ranked = scoreFreeFormOptions('auth', [
      { id: '1', label: 'Authentication' },
      { id: '2', label: 'Billing' },
      { id: '3', label: 'OAuth flow', keywords: 'auth login' },
    ])
    expect(ranked.map(item => item.value)).toEqual(['1', '3'])
  })

  it('exposes compare option catalogs and fingerprints', () => {
    expect(compareOptionsForValueType('equalValue', 2)[0]).toMatchObject({ label: 'is any of', value: 'is' })
    expect(compareOptionsForValueType('number').some(item => item.value === 'gt')).toBe(true)
    expect(getItemsFingerprint([])).toBe('empty')
    expect(getItemsFingerprint([{ id: 'a' }, { id: 'b' }, { id: 'c' }])).toBe('3:a:c')
  })
})

describe('findBlocks', () => {
  const blocks: FilterBlockDefinition[] = [
    { id: 'status', key: 'status', name: 'Status', valueType: 'equalValue', entityType: 'issue' },
    { id: 'assignee', key: 'assigneeId', name: 'Assignee', valueType: 'equalValue', entityType: 'issue' },
  ]

  it('walks and/or trees', () => {
    const found = findBlocks({
      and: [
        { field: 'status', operator: 'is', values: ['started'] },
        { or: [{ field: 'assigneeId', operator: 'not', values: ['u1'] }] },
      ],
    }, blocks)
    expect(found).toHaveLength(2)
    expect(found[0][0].id).toBe('status')
    expect(found[1][1].compareOption).toBe('not')
  })
})

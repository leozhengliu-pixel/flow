import { describe, expect, it } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useFilterValidation, validateFilterModel } from './use-filter-validation'
import type { FilterBlockDefinition } from './filter-block-types'

const blocks: FilterBlockDefinition[] = [
  {
    id: 'status',
    key: 'status',
    name: 'Status',
    valueType: 'equalValue',
    entityType: 'issue',
    isValueValid: value => value === 'started' || value === 'completed',
  },
  {
    id: 'labels',
    key: 'labelId',
    name: 'Labels',
    valueType: 'equalValue',
    entityType: 'issue',
    isValueValid: value => value.startsWith('l'),
  },
]

describe('useFilterValidation', () => {
  it('marks incomplete / invalid blocks', () => {
    const { result } = renderHook(() => useFilterValidation({
      filter: {
        and: [
          { field: 'status', operator: 'is', values: ['nope'] },
          { field: 'labelId', operator: 'all', values: ['l1', 'bad'] },
        ],
      },
      blocks,
    }))
    expect(result.current.isValid).toBe(false)
    expect(result.current.highlightInvalidBlocks).toBe(true)
  })

  it('treats not/neither as valid without values', () => {
    expect(validateFilterModel({ field: 'status', operator: 'not', values: [] }, blocks)).toBe(true)
  })

  it('accepts valid trees', () => {
    expect(validateFilterModel({
      and: [{ field: 'status', operator: 'is', values: ['started'] }],
    }, blocks)).toBe(true)
  })
})

import { beforeEach, describe, expect, it } from 'vitest'
import {
  clearFilterValueRegistry,
  getRegisteredFilterBlocks,
  hasRegisteredFilterValues,
  listRegisteredFilterEntityTypes,
  registerFilterValues,
} from './register-filter-values'
import { ensureFilterValuesRegistered, preloadFilterValuePacks } from './register-filter-values-lazy'

describe('RegisterFilterValues', () => {
  beforeEach(() => clearFilterValueRegistry())

  it('registers and lists packs', () => {
    registerFilterValues('issue', [
      { id: 'status', key: 'status', name: 'Status', valueType: 'equalValue', entityType: 'issue' },
      { id: 'status', key: 'status', name: 'Dup', valueType: 'equalValue', entityType: 'issue' },
    ])
    expect(hasRegisteredFilterValues('issue')).toBe(true)
    expect(getRegisteredFilterBlocks('issue')).toHaveLength(1)
    expect(listRegisteredFilterEntityTypes()).toEqual(['issue'])
  })
})

describe('RegisterFilterValuesShouldBeLazyLoaded', () => {
  beforeEach(() => clearFilterValueRegistry())

  it('lazy-loads entity packs', async () => {
    await ensureFilterValuesRegistered('project')
    expect(getRegisteredFilterBlocks('project').some(block => block.id === 'status')).toBe(true)
    await preloadFilterValuePacks(['initiative', 'notification', 'team'])
    expect(hasRegisteredFilterValues('initiative')).toBe(true)
    expect(hasRegisteredFilterValues('notification')).toBe(true)
    expect(getRegisteredFilterBlocks('team').length).toBeGreaterThan(0)
  })
})

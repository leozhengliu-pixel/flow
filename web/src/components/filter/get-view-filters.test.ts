import { describe, expect, it } from 'vitest'
import {
  encodeFilterForSearch,
  getViewFilters,
  readFilterFromSearch,
} from './get-view-filters'

describe('getViewFilters (LS-0701)', () => {
  it('reads and encodes URL filters', () => {
    const filter = { field: 'status', operator: 'is', values: ['started'] }
    const search = encodeFilterForSearch(filter)
    expect(search).toContain('filters=')
    expect(readFilterFromSearch(`?${search}`)).toEqual(filter)
  })

  it('supports emptyFilterWhenUrlParamMissing + identifier root path', () => {
    const views = getViewFilters(
      { urlKey: 'acme', pathname: 'team/ENG/all', search: '' },
      { emptyFilterWhenUrlParamMissing: true, type: 'issue' },
    )
    expect(views.universal.persistenceKey).toBe('/acme/team/ENG/all')
    expect(views.universal.isFiltering).toBe(false)
    expect(views.isFiltering('all')).toBe(false)
  })
})

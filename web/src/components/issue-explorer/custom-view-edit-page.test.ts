import { describe, expect, it } from 'vitest'
import type { SavedView } from '@/types/flow'
import { cloneCustomViewForEdit } from './custom-view-edit-page'

describe('CustomViewEditPage clone (LS-0156)', () => {
  it('clones view + filters for local edit transaction', () => {
    const view = {
      id: 'v1',
      name: 'Active',
      description: 'desc',
      resource: 'issues',
      scope: 'workspace',
      view: 'active',
      filters: [{ id: 'f1', field: 'status' }],
      display: { layout: 'board' },
      insights: { measure: 'count' },
    } as unknown as SavedView
    const clone = cloneCustomViewForEdit(view)
    expect(clone.view).not.toBe(view)
    expect(clone.filters).not.toBe(view.filters)
    expect(clone.filters).toEqual(view.filters)
    expect(clone.display.layout).toBe('board')
    expect(clone.insights).toEqual({ measure: 'count' })
  })
})

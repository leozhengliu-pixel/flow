import { describe, expect, it, vi } from 'vitest'
import { createProjectFilter, isProjectFilter } from './projects-filter-model'

describe('project filter model', () => {
  it('creates stable-shaped filters', () => {
    vi.spyOn(Date, 'now').mockReturnValue(123)
    vi.spyOn(Math, 'random').mockReturnValue(0.5)
    const filter = createProjectFilter('status', 'Status', { id: 'started', label: 'Started' })
    expect(filter).toMatchObject({ field: 'status', fieldLabel: 'Status', operator: 'is', values: [{ id: 'started', label: 'Started' }] })
    expect(filter.id).toMatch(/^status-123-/)
    vi.restoreAllMocks()
  })

  it('rejects malformed persisted values', () => {
    expect(isProjectFilter({ id: 'a', field: 'status', operator: 'is', values: [] })).toBe(true)
    expect(isProjectFilter({ id: 'a', field: 'status', operator: 'contains', values: [] })).toBe(false)
    expect(isProjectFilter({ id: 'a', field: 'status', operator: 'is', values: null })).toBe(false)
    expect(isProjectFilter(null)).toBe(false)
  })
})

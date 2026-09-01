import { describe, expect, it, vi } from 'vitest'
import { normalizeInboxFilters, removeInboxFilter, toggleInboxFilterValue, updateInboxFilterOperator, type InboxFilterCondition } from './inbox-filter-types'

const base: InboxFilterCondition = {
  id: 'from-filter', property: 'from', operator: 'is',
  values: [{ value: 'user-1', valueLabel: 'Viewer' }],
}

describe('inbox filters', () => {
  it('normalizes duplicate properties and values', () => {
    const normalized = normalizeInboxFilters([
      base,
      { ...base, id: 'duplicate', operator: 'isNot', values: [{ value: 'user-1', valueLabel: 'Viewer' }, { value: 'user-2', valueLabel: 'Teammate' }, { value: '', valueLabel: '' }] },
    ])
    expect(normalized).toEqual([{ ...base, values: [{ value: 'user-1', valueLabel: 'Viewer' }, { value: 'user-2', valueLabel: 'Teammate' }] }])
  })

  it('adds, toggles, removes, and changes operators immutably', () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000001')
    const added = toggleInboxFilterValue([], 'project', { id: 'project-1', label: 'Project' })
    expect(added[0]).toMatchObject({ id: 'project-00000000-0000-4000-8000-000000000001', property: 'project', operator: 'is' })
    const expanded = toggleInboxFilterValue(added, 'project', { id: 'project-2', label: 'Second project' })
    expect(expanded[0].values.map(value => value.value)).toEqual(['project-1', 'project-2'])
    const toggled = toggleInboxFilterValue(expanded, 'project', { id: 'project-1', label: 'Project' })
    expect(toggled[0].values.map(value => value.value)).toEqual(['project-2'])
    const changed = updateInboxFilterOperator(toggled, toggled[0].id, 'isNot')
    expect(changed[0].operator).toBe('isNot')
    expect(removeInboxFilter(changed, changed[0].id)).toEqual([])
    expect(added[0].operator).toBe('is')
  })

  it('supports review status as a first-class Inbox property', () => {
    const filters = toggleInboxFilterValue([], 'reviewStatus', { id: 'approved', label: 'Approved' })
    expect(filters).toEqual([expect.objectContaining({ property: 'reviewStatus', operator: 'is', values: [{ value: 'approved', valueLabel: 'Approved' }] })])
    expect(updateInboxFilterOperator(filters, filters[0].id, 'isNot')[0].operator).toBe('isNot')
  })
})

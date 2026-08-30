import { describe, expect, it } from 'vitest'
import { groupOptionSections } from './group-options'

describe('groupOptionSections', () => {
  it('preserves first-seen group ordering and keeps ungrouped options together', () => {
    const sections = groupOptionSections([
      { id: 'a', groupId: 'one', groupLabel: 'One' },
      { id: 'b' },
      { id: 'c', groupId: 'one', groupLabel: 'One' },
      { id: 'd', groupLabel: 'Two' },
    ])
    expect(sections.map(section => section.id)).toEqual(['one', 'ungrouped', 'Two'])
    expect(sections[0].options.map(option => option.id)).toEqual(['a', 'c'])
    expect(sections[1].label).toBeUndefined()
    expect(sections[2].label).toBe('Two')
  })
})

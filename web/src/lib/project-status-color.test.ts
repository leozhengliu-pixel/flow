import { describe, expect, it } from 'vitest'
import { projectStatusOptionColor } from './project-status-color'

describe('projectStatusOptionColor', () => {
  const current = { id: 'started', name: 'In progress', color: '#ffcc00' }

  it('uses the current status color for matching identities or names', () => {
    expect(projectStatusOptionColor({ id: 'started', name: 'Started', color: '#111' }, current)).toBe('#ffcc00')
    expect(projectStatusOptionColor({ id: 'other', name: 'In progress', color: '#222' }, current)).toBe('#ffcc00')
  })

  it('keeps the option color for unrelated statuses', () => {
    expect(projectStatusOptionColor({ id: 'done', name: 'Done', color: '#00aa66' }, current)).toBe('#00aa66')
  })
})

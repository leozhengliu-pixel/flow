import { describe, expect, it } from 'vitest'
import { CURRENT_USER_SENTINEL } from '../filter/block-utils'
import { getAssigneeFilterOptionsForContext } from './get-assignee-filter-options-for-context'

describe('getAssigneeFilterOptionsForContext (LS-0700)', () => {
  const me = { id: 'u1', displayName: 'Ada' }
  const users = [me, { id: 'u2', displayName: 'Bai' }, { id: 'u3', displayName: 'Cai' }]

  it('prefers persisted view assignee allow-list', () => {
    const options = getAssigneeFilterOptionsForContext({
      currentUser: me,
      users,
      view: { persisted: true, assigneeIds: ['u2'], includeNoAssigneeOption: true },
    })
    expect(options.map(option => option.id)).toEqual(['', 'u2'])
  })

  it('scopes to team members otherwise', () => {
    const options = getAssigneeFilterOptionsForContext({
      currentUser: me,
      users,
      teams: [{ id: 't1', memberIds: ['u3'] }],
    })
    const ids = options.map(option => option.id)
    expect(ids).toContain(CURRENT_USER_SENTINEL)
    expect(ids).toContain('u3')
    expect(ids).not.toContain('u2')
  })
})

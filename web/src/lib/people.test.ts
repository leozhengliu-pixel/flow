import { describe, expect, it } from 'vitest'

import type { User } from '@/types/flow'
import { markSuspendedMembersInactive, personMatchesQuery, personUsername } from './people'

describe('markSuspendedMembersInactive', () => {
  const person = (id: string): User => ({ id, name: id, displayName: id, email: `${id}@example.com`, active: true } as User)
  it('marks suspended members inactive so pickers leave them out', () => {
    const users = [person('stays'), person('paused')]
    const marked = markSuspendedMembersInactive(users, [{ user: users[0], status: 'active' }, { user: users[1], status: 'suspended' }])
    expect(marked.map(user => [user.id, user.active])).toEqual([['stays', true], ['paused', false]])
    expect(marked[0]).toBe(users[0])
  })
  it('keeps the list as it is without suspended members', () => {
    const users = [person('stays')]
    expect(markSuspendedMembersInactive(users, undefined)).toBe(users)
  })
})

describe('personUsername', () => {
  it('prefers the API username and falls back to a sanitized email local part', () => {
    expect(personUsername({ id: 'u1', username: 'bcgroupdev', email: 'other@example.test' })).toBe('bcgroupdev')
    expect(personUsername({ id: 'u2', email: 'Skyler.Anderson+work@example.test' })).toBe('skyler.andersonwork')
    expect(personUsername({ id: 'u3', email: '_odd_@example.test' })).toBe('odd')
    expect(personUsername({ id: 'u4', name: 'No Email' })).toBe('')
  })

  it('matches people by username', () => {
    expect(personMatchesQuery({ id: 'u1', displayName: 'Skyler Anderson', username: 'bcgroupdev' }, 'bcgroup')).toBe(true)
  })
})

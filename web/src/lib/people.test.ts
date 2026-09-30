import { describe, expect, it } from 'vitest'

import { personMatchesQuery, personUsername } from './people'

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

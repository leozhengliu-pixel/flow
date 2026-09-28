import { describe, expect, it } from 'vitest'

import {
  getIssueSharingAudience,
  isLessRestrictiveMembership,
  isLessRestrictivePermission,
} from './team-security-confirms'

describe('team security confirms', () => {
  it('detects less restrictive permission and membership changes', () => {
    expect(isLessRestrictivePermission('owners', 'allMembers')).toBe(true)
    expect(isLessRestrictivePermission('allMembers', 'owners')).toBe(false)
    expect(isLessRestrictiveMembership('owners', 'open')).toBe(true)
    expect(isLessRestrictiveMembership('open', 'members')).toBe(false)
  })

  it('returns issue sharing audience copy', () => {
    expect(getIssueSharingAudience()).toContain('link')
  })
})

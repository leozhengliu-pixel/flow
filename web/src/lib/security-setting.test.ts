import { describe, expect, it } from 'vitest'
import {
  isRestrictingSecurityPermission,
  normalizeSecurityPermission,
  roleSatisfiesSecurityPermission,
  securityPermissionLabel,
} from './security-setting'

describe('security-setting', () => {
  it('normalizes legacy and linear-like values', () => {
    expect(normalizeSecurityPermission('everyone')).toBe('members')
    expect(normalizeSecurityPermission('admins')).toBe('admins')
    expect(normalizeSecurityPermission('owners')).toBe('owners')
    expect(normalizeSecurityPermission('owners_and_admins')).toBe('owners_and_admins')
  })

  it('labels the four-role matrix', () => {
    expect(securityPermissionLabel('members')).toBe('All members')
    expect(securityPermissionLabel('owners_and_admins')).toBe('Only owners and admins')
    expect(securityPermissionLabel('owners')).toBe('Only owners')
    expect(securityPermissionLabel('admins')).toBe('Only admins')
  })

  it('detects API-key restriction promotions', () => {
    expect(isRestrictingSecurityPermission('members', 'admins')).toBe(true)
    expect(isRestrictingSecurityPermission('admins', 'owners')).toBe(true)
    expect(isRestrictingSecurityPermission('owners', 'members')).toBe(false)
  })

  it('enforces role gates', () => {
    expect(roleSatisfiesSecurityPermission('member', 'members')).toBe(true)
    expect(roleSatisfiesSecurityPermission('member', 'admins')).toBe(false)
    expect(roleSatisfiesSecurityPermission('admin', 'owners')).toBe(false)
    expect(roleSatisfiesSecurityPermission('owner', 'owners')).toBe(true)
    expect(roleSatisfiesSecurityPermission('admin', 'owners_and_admins')).toBe(true)
    expect(roleSatisfiesSecurityPermission('guest', 'members')).toBe(false)
  })
})

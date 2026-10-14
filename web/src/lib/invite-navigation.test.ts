import { describe, expect, it } from 'vitest'

import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData } from '@/types/flow'
import { canInviteMembers } from './invite-navigation'

function data(role: BootstrapData['viewerRole'], invitePermission?: string) {
  const base = makeBootstrap({ viewerRole: role })
  return { ...base, workspaceSettings: { ...base.workspaceSettings, invitePermission } } as BootstrapData
}

describe('canInviteMembers (Settings › Security "New user invitations")', () => {
  it('lets only admins invite by default and when set to admins', () => {
    for (const value of [undefined, '', 'admins']) {
      expect(canInviteMembers(data('member', value))).toBe(false)
      expect(canInviteMembers(data('admin', value))).toBe(true)
      expect(canInviteMembers(data('owner', value))).toBe(true)
    }
  })

  it('lets members invite when all members may', () => {
    expect(canInviteMembers(data('member', 'members'))).toBe(true)
    expect(canInviteMembers(data('guest', 'members'))).toBe(false)
  })
})

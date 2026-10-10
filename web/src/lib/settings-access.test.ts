import { describe, expect, it } from 'vitest'

import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, TeamSettings } from '@/types/flow'

import type { SettingsPageId } from './app-routes'
import {
  canAdministerReleasePipeline,
  canCreateReleasePipeline,
  roleAllowed,
  teamSettingsAccess,
  workspaceSettingsAccess,
  type SettingsAccess,
} from './settings-access'

type Role = BootstrapData['viewerRole']

function data(role: Role, settings: Partial<BootstrapData['workspaceSettings']> = {}): BootstrapData {
  const base = makeBootstrap({ viewerRole: role })
  return { ...base, workspaceSettings: { ...base.workspaceSettings, ...settings } }
}

// Linear's settings tree with default security settings (labels/templates by
// all members, import by admins). Columns: owner, admin, member, guest.
const MATRIX: [SettingsPageId, SettingsAccess, SettingsAccess][] = [
  // page, member, guest (owners and admins can always edit)
  ['preferences', 'edit', 'edit'],
  ['profile', 'edit', 'edit'],
  ['notifications', 'edit', 'edit'],
  ['code-and-reviews', 'edit', 'edit'],
  ['account-security', 'edit', 'edit'],
  ['connections', 'edit', 'edit'],
  ['agents', 'edit', 'edit'],
  ['issue-labels', 'edit', 'hidden'],
  ['issue-templates', 'edit', 'hidden'],
  ['sla', 'read', 'hidden'],
  ['project-labels', 'edit', 'hidden'],
  ['project-templates', 'edit', 'hidden'],
  ['project-statuses', 'read', 'hidden'],
  ['project-updates', 'read', 'hidden'],
  ['ai', 'edit', 'hidden'],
  ['loops', 'edit', 'hidden'],
  ['initiatives', 'read', 'hidden'],
  ['initiative-labels', 'edit', 'hidden'],
  ['documents', 'edit', 'hidden'],
  ['customer-requests', 'read', 'hidden'],
  ['releases', 'edit', 'hidden'],
  ['pulse', 'edit', 'hidden'],
  ['asks', 'edit', 'hidden'],
  ['emojis', 'edit', 'hidden'],
  ['integrations', 'edit', 'hidden'],
  ['workspace', 'hidden', 'hidden'],
  ['teams', 'hidden', 'hidden'],
  ['members', 'hidden', 'hidden'],
  ['security', 'hidden', 'hidden'],
  ['authentication', 'hidden', 'hidden'],
  ['audit-log', 'hidden', 'hidden'],
  ['api', 'hidden', 'hidden'],
  ['applications', 'hidden', 'hidden'],
  ['import-export', 'hidden', 'hidden'],
  ['workflows', 'hidden', 'hidden'],
]

describe('workspaceSettingsAccess', () => {
  const defaults = { labelPermission: 'members', templatePermission: 'members', importPermission: 'admins' } as const
  it.each(MATRIX)('%s: member=%s guest=%s, owners and admins edit', (page, member, guest) => {
    expect(workspaceSettingsAccess(data('owner', defaults), page)).toBe('edit')
    expect(workspaceSettingsAccess(data('admin', defaults), page)).toBe('edit')
    expect(workspaceSettingsAccess(data('member', defaults), page)).toBe(member)
    expect(workspaceSettingsAccess(data('guest', defaults), page)).toBe(guest)
  })

  it('applies the Security "who can" settings to members', () => {
    const restricted = data('member', { labelPermission: 'admins', templatePermission: 'admins', importPermission: 'members' })
    expect(workspaceSettingsAccess(restricted, 'issue-labels')).toBe('read')
    expect(workspaceSettingsAccess(restricted, 'project-labels')).toBe('read')
    expect(workspaceSettingsAccess(restricted, 'issue-templates')).toBe('hidden')
    expect(workspaceSettingsAccess(restricted, 'project-templates')).toBe('hidden')
    expect(workspaceSettingsAccess(restricted, 'documents')).toBe('hidden')
    expect(workspaceSettingsAccess(restricted, 'import-export')).toBe('edit')
    // Admins are bound by an owners-only label setting like the API.
    expect(workspaceSettingsAccess(data('admin', { labelPermission: 'owners' }), 'issue-labels')).toBe('read')
    expect(workspaceSettingsAccess(data('owner', { labelPermission: 'owners' }), 'issue-labels')).toBe('edit')
  })

  it('treats unset permissions like the API (admins only)', () => {
    expect(roleAllowed('member', undefined)).toBe(false)
    expect(roleAllowed('admin', undefined)).toBe(true)
    expect(roleAllowed('member', 'members')).toBe(true)
    expect(roleAllowed('guest', 'members')).toBe(false)
    expect(roleAllowed('admin', 'owners')).toBe(false)
    expect(roleAllowed('owner', 'admins_only')).toBe(false)
  })
})

describe('teamSettingsAccess', () => {
  function teamData(role: Role, teamRole: 'owner' | 'member' | undefined, permission: TeamSettings['settingsPermission']) {
    const base = data(role)
    const teamId = base.teams[0].id
    return {
      teamId,
      value: {
        ...base,
        teamMembers: teamRole ? [{ teamId, userId: base.viewer.id, role: teamRole, joinedAt: '' }] : [],
        teamSettings: { [teamId]: { teamId, settingsPermission: permission, labelPermission: permission, templatePermission: permission, memberPermission: permission } as TeamSettings },
      } as BootstrapData,
    }
  }

  it.each([
    // workspace role, team role, team permission, expected
    ['owner', undefined, 'owners', 'edit'],
    ['admin', undefined, 'owners', 'edit'],
    ['member', 'owner', 'owners', 'edit'],
    ['member', 'member', 'allMembers', 'edit'],
    ['member', 'member', 'owners', 'read'],
    ['member', undefined, 'allMembers', 'hidden'],
    ['guest', 'member', 'allMembers', 'hidden'],
  ] as const)('%s with team role %s and %s permission → %s', (role, teamRole, permission, expected) => {
    const { teamId, value } = teamData(role, teamRole, permission)
    expect(teamSettingsAccess(value, teamId, 'workflow')).toBe(expected)
    expect(teamSettingsAccess(value, teamId, 'issue-labels')).toBe(expected)
  })

  it('keeps Access and permissions owner-only for team members', () => {
    const { teamId, value } = teamData('member', 'member', 'allMembers')
    expect(teamSettingsAccess(value, teamId, 'security')).toBe('read')
  })
})

describe('release pipelines', () => {
  function pipelineData(role: Role, teamRole?: 'owner' | 'member') {
    const base = data(role)
    const teamId = base.teams[0].id
    return { teamId, value: { ...base, teamMembers: teamRole ? [{ teamId, userId: base.viewer.id, role: teamRole, joinedAt: '' }] : [], teamSettings: {} } as BootstrapData }
  }

  it('lets every non-guest create pipelines', () => {
    expect(canCreateReleasePipeline({ viewerRole: 'owner' })).toBe(true)
    expect(canCreateReleasePipeline({ viewerRole: 'admin' })).toBe(true)
    expect(canCreateReleasePipeline({ viewerRole: 'member' })).toBe(true)
    expect(canCreateReleasePipeline({ viewerRole: 'guest' })).toBe(false)
  })

  it('lets admins and owners of every pipeline team administer a pipeline', () => {
    const owner = pipelineData('member', 'owner')
    expect(canAdministerReleasePipeline(owner.value, { teamIds: [owner.teamId] })).toBe(true)
    expect(canAdministerReleasePipeline(owner.value, { teamIds: [owner.teamId, 'other-team'] })).toBe(false)
    expect(canAdministerReleasePipeline(owner.value, { teamIds: [] })).toBe(false)
    const member = pipelineData('member', 'member')
    expect(canAdministerReleasePipeline(member.value, { teamIds: [member.teamId] })).toBe(false)
    expect(canAdministerReleasePipeline(pipelineData('admin').value, { teamIds: [] })).toBe(true)
    expect(canAdministerReleasePipeline(pipelineData('guest', 'owner').value, { teamIds: [owner.teamId] })).toBe(false)
  })
})

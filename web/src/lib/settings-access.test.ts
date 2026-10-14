import { describe, expect, it } from 'vitest'

import { zhCN } from '@/i18n/translations'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, TeamSettings } from '@/types/flow'

import type { SettingsPageId } from './app-routes'
import {
  adminHintsApply,
  adminOnlyHint,
  hiddenSettingsMessage,
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

// The reference app's settings tree with default security settings (labels/templates by
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
  ['coding-sessions', 'edit', 'hidden'],
  ['coding-environments', 'edit', 'hidden'],
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

  it('keeps Access and permissions owner-only for team members (the reference app sends them to the overview)', () => {
    const { teamId, value } = teamData('member', 'member', 'allMembers')
    expect(teamSettingsAccess(value, teamId, 'security')).toBe('hidden')
    const owner = teamData('member', 'owner', 'owners')
    expect(teamSettingsAccess(owner.value, owner.teamId, 'security')).toBe('edit')
  })
})

describe('member-facing copy (measured in the reference app as a member)', () => {
  const member = data('member', { labelPermission: 'members', templatePermission: 'members' })

  it('answers administration URLs with "Not found" and names the API page reason', () => {
    expect(hiddenSettingsMessage('api')).toBe('Only workspace administrators can manage API settings')
    for (const page of ['workspace', 'members', 'security', 'applications', 'import-export'] as const)
      expect(hiddenSettingsMessage(page)).toBe('We could not find the page you were looking for')
  })

  it('explains disabled controls on hover only for members on admin-configured pages', () => {
    expect(adminHintsApply(member, 'sla')).toBe(true)
    expect(adminHintsApply(member, 'pulse')).toBe(true)
    expect(adminHintsApply(member, 'releases')).toBe(false)
    expect(adminHintsApply(member, 'issue-labels')).toBe(false)
    expect(adminHintsApply(member, 'preferences')).toBe(false)
    expect(adminHintsApply(data('admin'), 'sla')).toBe(false)
    expect(adminHintsApply(data('guest'), 'sla')).toBe(false)
  })

  it.each([
    ['sla', 'toggle', 'Enable SLAs', true, 'Only admins can modify'],
    ['sla', 'button', 'Add rule', true, 'Only admins can add a rule'],
    ['sla', 'button', 'Edit configuration', true, 'Only admins can edit SLAs'],
    ['sla', 'button', 'Delete configuration', true, 'Only admins can delete SLAs'],
    ['project-statuses', 'button', 'Create new project status', true, 'Only workspace admins can edit project statuses'],
    ['project-updates', 'button', 'Connect', true, 'Only workspace admins can configure this'],
    ['project-updates', 'button', 'Edit', true, 'Only admins can modify'],
    ['initiatives', 'button', 'Connect', true, 'Only workspace admins can configure this'],
    ['customer-requests', 'button', 'Create new customer status', true, 'Only workspace admins can edit customer statuses'],
    ['asks', 'button', 'Connect workspace', false, 'Only admins can connect a workspace'],
    ['asks', 'button', 'Add email', false, 'Only admins can add an email address'],
    ['loops', 'button', 'Allowlist', false, 'Only workspace security admins can change sources'],
    ['loops', 'toggle', 'Enable Loops', false, 'Only admins can modify'],
    ['pulse', 'select', 'Default workspace schedule', false, 'Only admins can modify'],
    ['pulse', 'select', 'Your personal schedule', false, null],
    ['integrations', 'button', 'Disable', true, 'Only admins can disable this integration'],
    ['ai', 'button', 'Save', false, null],
  ] as const)('%s › %s "%s" → %s', (page, kind, label, readOnly, expected) => {
    expect(adminOnlyHint(page, { kind, label }, readOnly)).toBe(expected)
    if (expected) expect(zhCN[expected], expected).toBeTruthy()
  })

  it('has Chinese copy for the hint link, Not found reasons and team danger zone', () => {
    for (const key of ['View admins →', 'Only workspace administrators can manage API settings', 'We could not find the page you were looking for', 'All integrations',
      'Only workspace owners, admins, and team owners can delete teams', 'Only workspace owners, admins, and team owners can retire teams',
      "Can't retire the last active team in the workspace", "Can't delete the last active team in the workspace", 'You should be a member of at least one active team'])
      expect(zhCN[key], key).toBeTruthy()
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

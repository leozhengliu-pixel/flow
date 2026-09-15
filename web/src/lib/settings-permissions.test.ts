import { describe, expect, it } from 'vitest'

import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData } from '@/types/flow'

import { canManageTeamSettings, viewerOwnsTeam } from './settings-permissions'

function permissionData(): BootstrapData {
  const data = makeBootstrap()
  const teamId = data.teams[0].id
  data.viewerRole = 'member'
  data.teamSettings = {
    [teamId]: {
      teamId,
      settingsPermission: 'owners',
      labelPermission: 'owners',
      templatePermission: 'owners',
      agentSkillPermission: 'owners',
      loopPermission: 'owners',
      membershipRestriction: 'open',
      access: 'public',
      memberPermission: 'allMembers',
    } as BootstrapData['teamSettings'][string],
  }
  data.teamSettings[teamId] = {
    ...data.teamSettings[teamId],
    access: 'public',
    memberPermission: 'allMembers',
  }
  data.teamMembers = []
  return data
}

describe('team settings permissions', () => {
  it('does not treat public team visibility as membership management permission', () => {
    const data = permissionData()
    expect(canManageTeamSettings(data, data.teams[0].id, 'members')).toBe(false)
  })

  it('allows direct members under allMembers while reserving owner role changes', () => {
    const data = permissionData()
    const teamId = data.teams[0].id
    data.teamMembers.push({ teamId, userId: data.viewer.id, role: 'member', joinedAt: new Date().toISOString() })
    expect(canManageTeamSettings(data, teamId, 'members')).toBe(true)
    expect(viewerOwnsTeam(data, teamId)).toBe(false)
  })

  it('inherits owner administration from a parent team', () => {
    const data = permissionData()
    const childId = 'child-team'
    data.teams.push({ ...data.teams[0], id: childId, key: 'CHILD', name: 'Child' })
    data.teamSettings[childId] = { ...data.teamSettings[data.teams[0].id], teamId: childId, parentTeamId: data.teams[0].id }
    data.teamMembers.push({ teamId: data.teams[0].id, userId: data.viewer.id, role: 'owner', joinedAt: new Date().toISOString() })
    expect(viewerOwnsTeam(data, childId)).toBe(true)
    expect(canManageTeamSettings(data, childId, 'members')).toBe(true)
  })
})

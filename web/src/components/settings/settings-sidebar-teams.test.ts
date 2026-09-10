import { describe, expect, it } from 'vitest'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, TeamSettings } from '@/types/flow'
import { settingsSidebarTeams } from './settings-sidebar-teams'

function fixture(role: BootstrapData['viewerRole'] = 'admin') {
  return makeBootstrap({ viewerRole: role, teams: Array.from({ length: 1000 }, (_, index) => ({ id: `team-${index}`, name: `Team ${index}`, key: `T${index}`, color: '#777777' })), teamMembers: [{ teamId: 'team-8', userId: 'user-1', role: 'member', joinedAt: '' }, { teamId: 'team-20', userId: 'user-1', role: 'owner', joinedAt: '' }, { teamId: 'team-3', userId: 'user-2', role: 'owner', joinedAt: '' }] })
}
describe('settings sidebar team scope', () => {
  it.each(['admin', 'owner', 'member'] as const)('shows only joined teams for %s, including ordinary memberships', role => {
    expect(settingsSidebarTeams(fixture(role)).map(item => item.team.id)).toEqual(['team-8', 'team-20'])
  })
  it('excludes retired teams and hides the section for guests', () => {
    const data = fixture()
    data.teams[8].retiredAt = '2026-09-01T00:00:00Z'
    expect(settingsSidebarTeams(data).map(item => item.team.id)).toEqual(['team-20'])
    expect(settingsSidebarTeams(fixture('guest'))).toEqual([])
  })
  it('searches joined team names and identifiers without expanding to all administrator teams', () => {
    expect(settingsSidebarTeams(fixture(), ' t20 ').map(item => item.team.id)).toEqual(['team-20'])
    expect(settingsSidebarTeams(fixture(), 'Team 8')).toHaveLength(1)
    expect(settingsSidebarTeams(fixture(), 'T999')).toEqual([])
  })
  it('orders joined parent teams before children and tolerates unavailable parents and cycles', () => {
    const data = fixture()
    data.teamSettings = { 'team-8': { parentTeamId: 'team-20' } as TeamSettings }
    expect(settingsSidebarTeams(data).map(item => [item.team.id, item.depth])).toEqual([['team-20', 0], ['team-8', 1]])
    data.teamSettings['team-20'] = { parentTeamId: 'team-8' } as TeamSettings
    expect(settingsSidebarTeams(data)).toHaveLength(2)
    data.teamSettings['team-20'].parentTeamId = 'unavailable'
    expect(settingsSidebarTeams(data)[0].team.id).toBe('team-20')
  })
  it('does not invent a display limit if the user really belongs to every team', () => {
    const data = fixture()
    data.teamMembers = data.teams.map(team => ({ teamId: team.id, userId: data.viewer.id, role: 'member', joinedAt: '' }))
    expect(settingsSidebarTeams(data)).toHaveLength(1000)
  })
})

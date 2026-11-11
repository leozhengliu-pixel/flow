import { describe, expect, it } from 'vitest'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, TeamSettings } from '@/types/flow'
import { settingsPhoneTeamGuideX, settingsPhoneTeamIndent, settingsSidebarTeams, settingsTeamGuideOffsets, settingsTeamGuideX, settingsTeamIndent } from './settings-sidebar-teams'

describe('settings sidebar team tree geometry', () => {
  it('insets a sub-team 22px, 18px more per level, and draws each level’s line under its parent’s icon', () => {
    expect([0, 1, 2, 3].map(settingsTeamIndent)).toEqual([0, 22, 40, 58])
    // 16px into a top-level row (x 28 for a row at x 12), 12px into a nested one.
    expect([1, 2, 3].map(settingsTeamGuideX)).toEqual([16, 34, 52])
  })
  it('gives the phone drawer 22px per level with the line under the parent’s icon (16px, then 12px into a nested row)', () => {
    expect([0, 1, 2, 3].map(settingsPhoneTeamIndent)).toEqual([0, 22, 44, 66])
    expect([1, 2, 3].map(settingsPhoneTeamGuideX)).toEqual([16, 34, 56])
    // Relative to the row of a team at that depth: a depth-1 team's own line is 6px left of its row, and so is every level's.
    expect(settingsTeamGuideOffsets(1, 1)).toEqual({ '--x': '-6px', '--x-phone': '-6px' })
    expect(settingsTeamGuideOffsets(2, 2)).toEqual({ '--x': '-6px', '--x-phone': '-10px' })
    expect(settingsTeamGuideOffsets(1, 3)['--x-phone']).toBe('-50px')
  })
})

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
  it('lists sibling sub-teams newest first at every level and keeps top-level teams in the given order', () => {
    const data = makeBootstrap({ teams: ['root', 'old', 'new', 'old-child', 'new-child'].map((id, index) => ({ id, name: id, key: id.toUpperCase().slice(0, 3) + index, color: '#777777', createdAt: `2026-11-0${index + 1}T00:00:00Z` })) })
    data.teamMembers = data.teams.map(team => ({ teamId: team.id, userId: data.viewer.id, role: 'member', joinedAt: '' }))
    data.teamSettings = Object.fromEntries([['old', 'root'], ['new', 'root'], ['old-child', 'old'], ['new-child', 'old']].map(([id, parent]) => [id, { parentTeamId: parent } as TeamSettings]))
    data.teams.push({ id: 'second-root', name: 'second-root', key: 'SR', color: '#777777', createdAt: '2026-11-09T00:00:00Z' })
    data.teamMembers.push({ teamId: 'second-root', userId: data.viewer.id, role: 'member', joinedAt: '' })
    expect(settingsSidebarTeams(data).map(item => `${item.team.id}:${item.depth}`)).toEqual(['root:0', 'new:1', 'old:1', 'new-child:2', 'old-child:2', 'second-root:0'])
  })
  it('does not invent a display limit if the user really belongs to every team', () => {
    const data = fixture()
    data.teamMembers = data.teams.map(team => ({ teamId: team.id, userId: data.viewer.id, role: 'member', joinedAt: '' }))
    expect(settingsSidebarTeams(data)).toHaveLength(1000)
  })
})

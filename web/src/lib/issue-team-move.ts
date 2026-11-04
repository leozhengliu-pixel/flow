import type { BootstrapData, Team } from '@/types/flow'
import { teamHierarchy } from './team-hierarchy'

export function issueTeamMoveReason(data: BootstrapData, team: Team): string | undefined {
  if (data.viewerRole === 'guest') return 'Guests cannot move issues to another team'
  if (team.archivedAt) return 'Team is archived'
  if (team.retiredAt) return 'Team is retired'
  const joined = new Set((data.teamMembers ?? []).filter(member => member.userId === data.viewer.id).map(member => member.teamId))
  if (joined.has(team.id)) return undefined
  const hierarchy = teamHierarchy(data.teams, data.teamSettings, data.teamParents)
  const boundary = (item: Team) => item.private || data.teamSettings?.[item.id]?.access === 'private' || data.teamSettings?.[item.id]?.access === 'restricted' && !data.teamSettings?.[item.id]?.parentTeamId
  if (boundary(team)) return 'Team is outside your teams'
  const ancestor = [...(hierarchy.ancestors.get(team.id) ?? [])].reverse().find(boundary)
  if (ancestor && !joined.has(ancestor.id)) return 'Team is outside your teams'
  return undefined
}

export function issueMoveTeams(data: BootstrapData) {
  const hierarchy = teamHierarchy(data.teams, data.teamSettings, data.teamParents)
  return hierarchy.rows(data.teams.filter(team => !issueTeamMoveReason(data, team))).map(({ team }) => ({
    team,
    ancestors: hierarchy.ancestors.get(team.id) ?? [],
    path: [...(hierarchy.ancestors.get(team.id) ?? []), team].map(item => item.name).join(' > '),
  }))
}

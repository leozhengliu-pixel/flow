/** One team a project can belong to. `icon` is the team's icon name (see ViewGlyph). */
export interface ProjectTeamChoice {
  id: string
  name: string
  key?: string
  color?: string
  icon?: string
}

/** A project's teams are ordered: the first is the lead team. */
export function toggleProjectTeam(teamIds: string[], teamId: string): string[] {
  if (!teamIds.includes(teamId)) return [...teamIds, teamId]
  // The lead team (so also a project's only team) cannot be unchecked; choose another lead team first.
  if (teamIds[0] === teamId) return teamIds
  return teamIds.filter(id => id !== teamId)
}

export function setProjectLeadTeam(teamIds: string[], teamId: string): string[] {
  if (!teamIds.includes(teamId) || teamIds[0] === teamId) return teamIds
  return [teamId, ...teamIds.filter(id => id !== teamId)]
}

/** "Design" for one team, "Design, ENG, OPS" (lead team name, then the other teams' keys) for several. */
export function projectTeamsLabel(teams: ProjectTeamChoice[], teamIds: string[], fallback: string): string {
  const selected = teamIds.map(id => teams.find(team => team.id === id)).filter((team): team is ProjectTeamChoice => Boolean(team))
  if (!selected.length) return fallback
  const [lead, ...others] = selected
  return [lead.name, ...others.map(team => team.key || team.name)].join(', ')
}

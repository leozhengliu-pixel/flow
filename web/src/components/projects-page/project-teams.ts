/** One team a project can belong to. `icon` is the team's icon name (see ViewGlyph). */
export interface ProjectTeamChoice {
  id: string
  name: string
  key?: string
  color?: string
  icon?: string
  /** The parent team when this team is a sub-team; the picker falls back to the team directory when a caller omits it. */
  parentId?: string
}

/** The icon name and colour a team shows, with the defaults for a team that has none set. */
export function teamGlyph(team: ProjectTeamChoice) { return { icon: team.icon ?? 'Team', color: team.color ?? '#5e6ad2' } }

export interface TeamTreeRow<T extends { id: string }> {
  team: T
  /** How many of the team's ancestors are in the same list: 0 for a top-level row. */
  depth: number
}

/**
 * Orders a flat team list as a tree: a team is followed by its sub-teams, which keep the list's order, and deeper
 * levels repeat. A sub-team whose ancestors are all missing from the list stays a top-level row, so a menu block
 * never indents a team under one it does not show. `parentOf` returns a team id's parent (for any team id).
 */
export function orderTeamTree<T extends { id: string }>(teams: T[], parentOf: (id: string) => string | undefined): TeamTreeRow<T>[] {
  const ids = new Set(teams.map(team => team.id))
  const nearestParent = (team: T): string | undefined => {
    const seen = new Set([team.id])
    for (let id = parentOf(team.id); id && !seen.has(id); id = parentOf(id)) {
      if (ids.has(id)) return id
      seen.add(id)
    }
    return undefined
  }
  const children = new Map<string, T[]>()
  const roots: T[] = []
  for (const team of teams) {
    const parent = nearestParent(team)
    if (parent) children.set(parent, [...(children.get(parent) ?? []), team])
    else roots.push(team)
  }
  const rows: TeamTreeRow<T>[] = []
  const visit = (team: T, depth: number) => {
    rows.push({ team, depth })
    for (const child of children.get(team.id) ?? []) visit(child, depth + 1)
  }
  for (const team of roots) visit(team, 0)
  // Teams in a parent cycle are unreachable from any root: keep them, flat.
  if (rows.length < teams.length) {
    const placed = new Set(rows.map(row => row.team.id))
    for (const team of teams) if (!placed.has(team.id)) rows.push({ team, depth: 0 })
  }
  return rows
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

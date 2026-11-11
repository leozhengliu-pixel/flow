import type { BootstrapData, Team } from '@/types/flow'

/** Left inset of a team row in the settings sidebar: 22px for a sub-team, 18px more per deeper level. */
export function settingsTeamIndent(depth: number): number {
  return depth <= 0 ? 0 : 22 + (depth - 1) * 18
}

/** x of the tree line joining level `level` teams to their parent, from the left of a top-level team row: under the
 *  parent's icon (16px into a top-level row, 12px into a nested one). */
export function settingsTeamGuideX(level: number): number {
  return settingsTeamIndent(level - 1) + (level > 1 ? 12 : 16)
}

export function settingsSidebarTeams(data: BootstrapData, query = ''): { team: Team; depth: number }[] {
  if (data.viewerRole === 'guest') return []
  const memberships = new Set((data.teamMembers ?? []).filter(member => member.userId === data.viewer.id).map(member => member.teamId))
  const teams = data.teams.filter(team => memberships.has(team.id) && !team.retiredAt)
  const needle = query.trim().toLocaleLowerCase()
  if (needle) return teams.filter(team => `${team.name} ${team.key}`.toLocaleLowerCase().includes(needle)).map(team => ({ team, depth: 0 }))

  const ids = new Set(teams.map(team => team.id))
  const children = new Map<string, Team[]>()
  const roots: Team[] = []
  for (const team of teams) {
    const parent = data.teamSettings?.[team.id]?.parentTeamId
    if (parent && parent !== team.id && ids.has(parent)) {
      const siblings = children.get(parent) ?? []
      siblings.push(team); children.set(parent, siblings)
    }
    else roots.push(team)
  }
  const result: { team: Team; depth: number }[] = []
  const visited = new Set<string>()
  const visit = (root: Team) => {
    const stack = [{ team: root, depth: 0 }]
    while (stack.length) {
      const item = stack.pop()!
      if (visited.has(item.team.id)) continue
      visited.add(item.team.id); result.push(item)
      const nested = children.get(item.team.id) ?? []
      for (let i = nested.length - 1; i >= 0; i--) stack.push({ team: nested[i], depth: item.depth + 1 })
    }
  }
  roots.forEach(visit)
  // Malformed legacy parent relationships must not hide a joined team.
  teams.forEach(team => { if (!visited.has(team.id)) visit(team) })
  return result
}

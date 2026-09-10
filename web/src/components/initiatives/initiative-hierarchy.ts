import type { Initiative, Project, Team, TeamSettings } from '@/types/flow'

export function initiativeGraph(initiatives: Initiative[]) {
  const byId = new Map(initiatives.map(item => [item.id, item]))
  const children = new Map<string, string[]>()
  for (const item of initiatives) for (const parent of new Set(item.parentInitiativeIds ?? [])) {
    if (parent === item.id || !byId.has(parent)) continue
    const ids = children.get(parent) ?? []; ids.push(item.id); children.set(parent, ids)
  }
  const walk = (id: string, direction: 'parents' | 'children') => {
    const seen = new Set([id]), stack = [id], result: string[] = []
    while (stack.length) {
      const current = stack.pop()!
      for (const next of direction === 'children' ? children.get(current) ?? [] : byId.get(current)?.parentInitiativeIds ?? []) {
        if (seen.has(next) || !byId.has(next)) continue
        seen.add(next); result.push(next); stack.push(next)
      }
    }
    return result
  }
  const projectCache = new Map<string, Set<string>>()
  const projectIds = (id: string) => {
    if (!projectCache.has(id)) projectCache.set(id, new Set([id, ...walk(id, 'children')].flatMap(key => byId.get(key)?.projectIds ?? [])))
    return projectCache.get(id)!
  }
  return { byId, children, ancestors: (id: string) => walk(id, 'parents'), descendants: (id: string) => walk(id, 'children'), projectIds,
    canParent: (child: string, parent: string) => child !== parent && byId.has(parent) && !walk(child, 'children').includes(parent) }
}

export function initiativeProjectIds(initiative: Initiative, initiatives: Initiative[]) { return initiativeGraph(initiatives).projectIds(initiative.id) }

export function initiativesForTeam(initiatives: Initiative[], projects: Project[], teams: Team[], settings: Record<string, Pick<TeamSettings, 'parentTeamId'>>, teamId: string, contributing = true) {
  const graph = initiativeGraph(initiatives), accessibleTeams = new Set(teams.map(team => team.id))
  const teamParents = (id: string) => {
    const ids = new Set<string>()
    while (id && accessibleTeams.has(id) && !ids.has(id)) { ids.add(id); id = settings[id]?.parentTeamId ?? '' }
    return ids
  }
  const scope = new Set(teams.filter(team => teamParents(team.id).has(teamId)).map(team => team.id))
  const projectTeams = new Map(projects.map(project => [project.id, project.teamIds]))
  return initiatives.filter(item => {
    const led = Boolean(item.leadTeamId && scope.has(item.leadTeamId))
    if (!contributing || led) return led
    const related = new Set(item.contributingTeamIds)
    for (const id of [item.id, ...graph.ancestors(item.id)]) {
      const lead = graph.byId.get(id)?.leadTeamId
      if (lead) for (const team of teamParents(lead)) related.add(team)
    }
    for (const id of graph.projectIds(item.id)) for (const team of projectTeams.get(id) ?? []) related.add(team)
    return [...related].some(id => scope.has(id))
  })
}

export function initiativeTreeRows(items: Initiative[], graph: ReturnType<typeof initiativeGraph>, collapsed: Set<string>) {
  const included = new Set(items.map(item => item.id)), visited = new Set<string>()
  const rank = new Map(items.map((item, index) => [item.id, index]))
  const result: { initiative: Initiative; depth: number; childCount: number }[] = []
  const visit = (root: Initiative) => {
    const stack = [{ initiative: root, depth: 0 }]
    while (stack.length) {
      const row = stack.pop()!
      if (visited.has(row.initiative.id)) continue
      visited.add(row.initiative.id)
      const childIds = (graph.children.get(row.initiative.id) ?? []).filter(id => included.has(id)).sort((a, b) => rank.get(a)! - rank.get(b)!)
      result.push({ ...row, childCount: childIds.length })
      if (collapsed.has(row.initiative.id)) continue
      for (let i = childIds.length - 1; i >= 0; i--) stack.push({ initiative: graph.byId.get(childIds[i])!, depth: row.depth + 1 })
    }
  }
  const roots = items.filter(item => !(item.parentInitiativeIds ?? []).some(id => included.has(id)))
  const covered = new Set(roots.flatMap(root => [root.id, ...graph.descendants(root.id)]))
  roots.forEach(visit)
  items.filter(item => !covered.has(item.id)).forEach(visit)
  return result
}

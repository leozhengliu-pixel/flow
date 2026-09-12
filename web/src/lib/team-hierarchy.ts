import type { Team, TeamSettings } from '@/types/flow'

export const MAX_TEAM_DEPTH = 4
export type TeamHierarchySettings = Record<string, Pick<TeamSettings, 'parentTeamId'> & Partial<Pick<TeamSettings, 'access'>>>

export function resolvedTeamSettings(settings: Record<string, TeamSettings>, teamId: string, seen = new Set<string>()): TeamSettings | undefined {
  const current = settings[teamId]
  if (!current || seen.has(teamId)) return current
  seen.add(teamId)
  if (!current.inheritIssueEstimation || !current.parentTeamId) return current
  const parent = resolvedTeamSettings(settings, current.parentTeamId, seen)
  return parent ? { ...current, estimateType: parent.estimateType } : current
}

export function teamHierarchy(teams: Team[], settings: TeamHierarchySettings = {}) {
  const byId = new Map(teams.map(team => [team.id, team]))
  const ancestors = new Map<string, Team[]>()
  const children = new Map<string, Team[]>()
  const heights = new Map<string, number>()
  for (const team of teams) {
    const chain: Team[] = [], seen = new Set([team.id])
    let parent = byId.get(settings[team.id]?.parentTeamId ?? '')
    while (parent && !seen.has(parent.id)) {
      seen.add(parent.id); chain.unshift(parent)
      parent = byId.get(settings[parent.id]?.parentTeamId ?? '')
    }
    ancestors.set(team.id, chain)
    chain.forEach((ancestor, index) => heights.set(ancestor.id, Math.max(heights.get(ancestor.id) ?? 0, chain.length - index)))
    const parentId = settings[team.id]?.parentTeamId
    if (parentId && byId.has(parentId) && parentId !== team.id) { const siblings = children.get(parentId) ?? []; siblings.push(team); children.set(parentId, siblings) }
  }
  const path = (id: string) => [...(ancestors.get(id) ?? []), ...(byId.has(id) ? [byId.get(id)!] : [])].map(team => team.name).join(' › ')
  const parentError = (teamId: string, parentId: string): string | undefined => {
    if (!parentId) return undefined
    const parent = byId.get(parentId)
    if (!parent || parent.retiredAt) return 'Parent team is unavailable'
    if (parentId === teamId || ancestors.get(parentId)?.some(team => team.id === teamId)) return 'A team cannot be its own ancestor'
    if ((ancestors.get(parentId)?.length ?? 0) + 1 + (heights.get(teamId) ?? 0) > MAX_TEAM_DEPTH) return 'Teams can only be nested five levels deep'
  }
  const subtree = (id: string) => new Set([id, ...teams.filter(team => !team.retiredAt && ancestors.get(team.id)?.some(parent => parent.id === id)).map(team => team.id)])
  const rows = (items = teams, collapsed = new Set<string>()) => {
    const ids = new Set(items.map(team => team.id)), nested = new Map<string, Team[]>()
    const roots: Team[] = []
    for (const team of items) {
      const parent = [...(ancestors.get(team.id) ?? [])].reverse().find(item => ids.has(item.id))
      if (parent) { const siblings = nested.get(parent.id) ?? []; siblings.push(team); nested.set(parent.id, siblings) }
      else roots.push(team)
    }
    const result: { team: Team; depth: number; hasChildren: boolean }[] = [], seen = new Set<string>()
    const visit = (root: Team) => {
      const stack = [{team: root, depth: 0}]
      while (stack.length) {
        const item = stack.pop()!
        if (seen.has(item.team.id)) continue
        seen.add(item.team.id)
        result.push({...item, hasChildren: Boolean(nested.get(item.team.id)?.length)})
        if (!collapsed.has(item.team.id)) for (const team of [...(nested.get(item.team.id) ?? [])].reverse()) stack.push({team, depth: item.depth + 1})
      }
    }
    roots.forEach(visit)
    // Preserve malformed legacy trees without reintroducing deliberately collapsed descendants.
    items.filter(team => !seen.has(team.id) && !(ancestors.get(team.id) ?? []).some(parent => collapsed.has(parent.id))).forEach(visit)
    return result
  }
  return { byId, ancestors, children, heights, path, parentError, subtree, rows }
}

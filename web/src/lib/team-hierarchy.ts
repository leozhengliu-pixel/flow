import type { Team, TeamSettings } from '@/types/flow'

export const MAX_TEAM_DEPTH = 4
export type TeamHierarchySettings = Record<string, Pick<TeamSettings, 'parentTeamId'> & Partial<Pick<TeamSettings, 'access' | 'defaultProjectTemplateId'>>>

export function resolvedTeamSettings(settings: Record<string, TeamSettings>, teamId: string, seen = new Set<string>()): TeamSettings | undefined {
  const current = settings[teamId]
  if (!current || seen.has(teamId)) return current
  seen.add(teamId)
  if (!current.inheritIssueEstimation || !current.parentTeamId) return current
  const parent = resolvedTeamSettings(settings, current.parentTeamId, seen)
  return parent
    ? {
        ...current,
        estimateType: parent.estimateType,
        estimateAllowZero: parent.estimateAllowZero,
        estimateExtended: parent.estimateExtended,
        estimateCountUnestimated: parent.estimateCountUnestimated,
      }
    : current
}

/**
 * How sibling teams are ordered at every nesting level. Menus, pickers, filters and Settings > Teams list siblings
 * alphabetically; the sidebars list the sub-teams of a team newest first (a new membership goes to the top of the list)
 * while top-level teams keep the given order ('sidebar'); 'newest' orders every level newest first; 'given' keeps the
 * caller's order (Settings > Teams, which sorts by the chosen column).
 */
export type TeamSiblingOrder = 'alphabetical' | 'newest' | 'sidebar' | 'given'

/** Sorts teams for the given sibling order without touching the input; ties keep the input order. */
export function sortTeamSiblings<T extends Pick<Team, 'name' | 'createdAt'>>(items: readonly T[], order: TeamSiblingOrder = 'alphabetical'): T[] {
  if (order === 'given') return [...items]
  const indexed = items.map((team, index) => ({ team, index }))
  if (order === 'newest' || order === 'sidebar') {
    indexed.sort((a, b) => {
      const left = a.team.createdAt ? Date.parse(a.team.createdAt) : Number.NaN, right = b.team.createdAt ? Date.parse(b.team.createdAt) : Number.NaN
      if (!Number.isNaN(left) && !Number.isNaN(right) && left !== right) return right - left
      return b.index - a.index
    })
  } else {
    indexed.sort((a, b) => a.team.name.localeCompare(b.team.name, undefined, { sensitivity: 'base' }) || a.index - b.index)
  }
  return indexed.map(item => item.team)
}

export function teamHierarchy(teams: Team[], settings: TeamHierarchySettings = {}, projectedParents: Record<string, string> = {}) {
  const byId = new Map(teams.map(team => [team.id, team]))
  const ancestors = new Map<string, Team[]>()
  const children = new Map<string, Team[]>()
  const heights = new Map<string, number>()
  for (const team of teams) {
    const chain: Team[] = [], seen = new Set([team.id])
    let parent = byId.get(settings[team.id]?.parentTeamId ?? projectedParents[team.id] ?? '')
    while (parent && !seen.has(parent.id)) {
      seen.add(parent.id); chain.unshift(parent)
      parent = byId.get(settings[parent.id]?.parentTeamId ?? projectedParents[parent.id] ?? '')
    }
    ancestors.set(team.id, chain)
    chain.forEach((ancestor, index) => heights.set(ancestor.id, Math.max(heights.get(ancestor.id) ?? 0, chain.length - index)))
    const parentId = settings[team.id]?.parentTeamId ?? projectedParents[team.id]
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
  const rows = (unsorted = teams, collapsed = new Set<string>(), order: TeamSiblingOrder = 'alphabetical') => {
    const items = sortTeamSiblings(unsorted, order)
    const givenIndex = new Map(unsorted.map((team, index) => [team.id, index]))
    const ids = new Set(items.map(team => team.id)), nested = new Map<string, Team[]>()
    const roots: Team[] = []
    for (const team of items) {
      const parent = [...(ancestors.get(team.id) ?? [])].reverse().find(item => ids.has(item.id))
      if (parent) { const siblings = nested.get(parent.id) ?? []; siblings.push(team); nested.set(parent.id, siblings) }
      else roots.push(team)
    }
    if (order === 'sidebar') roots.sort((a, b) => givenIndex.get(a.id)! - givenIndex.get(b.id)!)
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

export type TeamRowNode = { team: Team; children: TeamRowNode[] }
/** Folds the depth-first rows of `teamHierarchy().rows()` back into a tree, so a sub-team can render inside its parent. */
export function teamRowTree(rows: { team: Team; depth: number }[]): TeamRowNode[] {
  const roots: TeamRowNode[] = [], path: TeamRowNode[] = []
  for (const row of rows) {
    const node: TeamRowNode = { team: row.team, children: [] }
    path.length = Math.min(row.depth, path.length)
    const parent = path[path.length - 1]
    if (parent) parent.children.push(node); else roots.push(node)
    path.push(node)
  }
  return roots
}

/**
 * Tree guides for a flat list of rows in tree order (`depths[i]` is row i's nesting level, 0 for a root): for every row,
 * the levels (1…depth) whose vertical guide runs on past the row's bottom edge because a later sibling at that level
 * follows. A sub-item's own level is in the list unless it is its parent's last shown child; an ancestor's level is in
 * it while that ancestor still has a later sibling, so the guide passes the ancestor's whole subtree.
 */
export function treeGuideLevels(depths: number[]): number[][] {
  return depths.map((depth, index) => {
    const levels: number[] = []
    for (let level = 1; level <= depth; level++) {
      let next = index + 1
      while (next < depths.length && depths[next] > level) next++
      if (next < depths.length && depths[next] === level) levels.push(level)
    }
    return levels
  })
}

/** Left offset of a team's rows in the sidebar: the first sub-level steps in by 19px, every deeper level by 16px more (an elbow's left is this offset minus 4px). */
export function teamIndentPx(depth: number): number {
  return depth <= 0 ? 0 : 19 + (depth - 1) * 16
}

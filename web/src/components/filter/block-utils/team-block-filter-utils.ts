/**
 * LS-0719 teamBlockFilterUtils — __myTeams__ sentinel, tree sort, retired teams.
 */
import type { FilterBlockDefinition, FilterBlockOption, FilterEntityType } from '../filter-block-types'
import { MY_TEAMS_SENTINEL } from './user-filter-block-helper'

export { MY_TEAMS_SENTINEL }

export interface FilterTeamLike {
  id: string
  name: string
  key?: string
  parentId?: string | null
  retiredAt?: string | null
  private?: boolean
}

export function myTeamsFilterOption(): FilterBlockOption {
  return {
    id: MY_TEAMS_SENTINEL,
    label: 'My teams',
    keywords: 'mine my teams membership',
  }
}

/** Depth-first tree order with optional indentation metadata for menus. */
export function sortTeamsAsTree(
  teams: FilterTeamLike[],
  options: { includeRetired?: boolean } = {},
): Array<FilterTeamLike & { depth: number }> {
  const visible = teams.filter(team => options.includeRetired || !team.retiredAt)
  const byParent = new Map<string | null, FilterTeamLike[]>()
  for (const team of visible) {
    const parent = team.parentId ?? null
    const list = byParent.get(parent) ?? []
    list.push(team)
    byParent.set(parent, list)
  }
  for (const list of byParent.values()) {
    list.sort((a, b) => a.name.localeCompare(b.name))
  }
  const result: Array<FilterTeamLike & { depth: number }> = []
  const walk = (parentId: string | null, depth: number) => {
    for (const team of byParent.get(parentId) ?? []) {
      result.push({ ...team, depth })
      walk(team.id, depth + 1)
    }
  }
  walk(null, 0)
  // Orphans whose parent is missing from the set
  const seen = new Set(result.map(team => team.id))
  for (const team of visible) {
    if (!seen.has(team.id)) result.push({ ...team, depth: 0 })
  }
  return result
}

export function teamToFilterOption(
  team: FilterTeamLike & { depth?: number },
): FilterBlockOption {
  const indent = '· '.repeat(team.depth ?? 0)
  return {
    id: team.id,
    label: `${indent}${team.name}`,
    keywords: [team.name, team.key, team.retiredAt ? 'retired' : ''].filter(Boolean).join(' '),
    disabled: Boolean(team.retiredAt),
  }
}

export function buildTeamFilterOptions(args: {
  teams: FilterTeamLike[]
  includeMyTeams?: boolean
  includeRetired?: boolean
  selectedIds?: string[]
}): FilterBlockOption[] {
  const options: FilterBlockOption[] = []
  if (args.includeMyTeams) options.push(myTeamsFilterOption())
  const tree = sortTeamsAsTree(args.teams, { includeRetired: args.includeRetired })
  for (const team of tree) {
    options.push(teamToFilterOption(team))
  }
  // Keep selected retired teams visible even when includeRetired is false
  if (!args.includeRetired && args.selectedIds?.length) {
    const known = new Set(options.map(option => option.id))
    for (const id of args.selectedIds) {
      if (known.has(id) || id === MY_TEAMS_SENTINEL) continue
      const team = args.teams.find(item => item.id === id)
      if (team) options.push(teamToFilterOption({ ...team, depth: 0 }))
    }
  }
  return options
}

export function createTeamCollectionFilterBlock(args: {
  id?: string
  key?: string
  name?: string
  entityType: FilterEntityType
  sortPriority?: number
  includeMyTeams?: boolean
}): FilterBlockDefinition {
  return {
    id: args.id ?? 'team',
    key: args.key ?? 'teamId',
    name: args.name ?? 'Team',
    valueType: 'equalValue',
    inputType: 'options',
    entityType: args.entityType,
    sortPriority: args.sortPriority ?? 20,
    defaultCompareOption: 'is',
  }
}

export const teamBlockFilterUtils = {
  MY_TEAMS_SENTINEL,
  myTeamsFilterOption,
  sortTeamsAsTree,
  teamToFilterOption,
  buildTeamFilterOptions,
  createTeamCollectionFilterBlock,
}

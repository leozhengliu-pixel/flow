/**
 * LS-0703 initiativeBlockFilterUtils — lift initiatives FilterState onto BlockFilterUtils.
 */
import type { FilterBlockDefinition, FilterEntityType, FilterModelNode } from '../filter-block-types'
import { combineModelFilters } from '../filter-block-helper'
import { createDateFilterBlock } from './date-block-filter-utils'
import { createLabelFilterBlock } from './label-block-filter-utils'
import { createTeamCollectionFilterBlock } from './team-block-filter-utils'

export type InitiativeFilterField =
  | 'status'
  | 'priority'
  | 'ownerId'
  | 'creatorId'
  | 'leadTeamId'
  | 'teamId'
  | 'health'
  | 'labelId'
  | 'targetDate'
  | 'createdAt'
  | 'updatedAt'

/** Ad-hoc initiatives FilterState → REST FilterModelNode. */
export function initiativeFilterStateToModel(
  state: Partial<Record<InitiativeFilterField, string | string[] | undefined>>,
  combine: 'and' | 'or' = 'and',
): FilterModelNode {
  const nodes: FilterModelNode[] = []
  for (const [field, raw] of Object.entries(state)) {
    if (raw == null || raw === '') continue
    const values = Array.isArray(raw) ? raw.map(String) : [String(raw)]
    nodes.push({ field, operator: 'is', values })
  }
  return combineModelFilters(combine, nodes)
}

export function createInitiativeFilterBlocks(
  entityType: FilterEntityType = 'initiative',
): FilterBlockDefinition[] {
  const equal = (
    id: string,
    key: string,
    name: string,
    sortPriority: number,
  ): FilterBlockDefinition => ({
    id,
    key,
    name,
    valueType: 'equalValue',
    inputType: 'options',
    entityType,
    sortPriority,
    defaultCompareOption: 'is',
  })
  return [
    equal('status', 'status', 'Status', 10),
    equal('priority', 'priority', 'Priority', 20),
    equal('owner', 'ownerId', 'Owner', 30),
    equal('creator', 'creatorId', 'Creator', 40),
    equal('leadTeam', 'leadTeamId', 'Lead team', 50),
    createTeamCollectionFilterBlock({
      id: 'team',
      key: 'teamId',
      name: 'Team',
      entityType,
      sortPriority: 60,
      includeMyTeams: true,
    }),
    equal('health', 'health', 'Health', 70),
    createLabelFilterBlock({ entityType, sortPriority: 80 }),
    createDateFilterBlock({
      id: 'targetDate',
      key: 'targetDate',
      name: 'Target date',
      entityType,
      sortPriority: 90,
      defaultCompareOptionForDates: 'before',
    }),
    createDateFilterBlock({
      id: 'createdAt',
      key: 'createdAt',
      name: 'Created',
      entityType,
      sortPriority: 100,
      defaultCompareOptionForDates: 'after',
    }),
    createDateFilterBlock({
      id: 'updatedAt',
      key: 'updatedAt',
      name: 'Updated',
      entityType,
      sortPriority: 110,
      defaultCompareOptionForDates: 'after',
    }),
  ]
}

export const initiativeBlockFilterUtils = {
  initiativeFilterStateToModel,
  createInitiativeFilterBlocks,
}

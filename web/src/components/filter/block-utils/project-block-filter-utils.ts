/**
 * LS-0716 projectBlockFilterUtils — Status / StatusType helpers + collapsed statuses.
 */
import type { FilterBlockDefinition, FilterBlockOption, FilterEntityType } from '../filter-block-types'

export type ProjectStatusType = 'planned' | 'started' | 'paused' | 'completed' | 'canceled' | 'backlog'

export interface FilterProjectStatusLike {
  id: string
  name: string
  type: ProjectStatusType | string
  color?: string
  collapsed?: boolean
}

export const PROJECT_STATUS_TYPE_OPTIONS: FilterBlockOption[] = [
  { id: 'backlog', label: 'Backlog', keywords: 'status type' },
  { id: 'planned', label: 'Planned', keywords: 'status type' },
  { id: 'started', label: 'In progress', keywords: 'status type started active' },
  { id: 'paused', label: 'Paused', keywords: 'status type' },
  { id: 'completed', label: 'Completed', keywords: 'status type done' },
  { id: 'canceled', label: 'Canceled', keywords: 'status type cancelled' },
]

export function buildProjectStatusFilterOptions(
  statuses: FilterProjectStatusLike[],
  options: { includeCollapsed?: boolean } = {},
): FilterBlockOption[] {
  return statuses
    .filter(status => options.includeCollapsed || !status.collapsed)
    .map(status => ({
      id: status.id,
      label: status.name,
      color: status.color,
      keywords: `${status.name} ${status.type}`,
    }))
}

export function createProjectStatusFilterBlock(
  entityType: FilterEntityType = 'project',
): FilterBlockDefinition {
  return {
    id: 'status',
    key: 'statusId',
    name: 'Status',
    valueType: 'equalValue',
    inputType: 'options',
    entityType,
    sortPriority: 10,
    defaultCompareOption: 'is',
    options: () => [],
  }
}

export function createProjectStatusTypeFilterBlock(
  entityType: FilterEntityType = 'project',
): FilterBlockDefinition {
  return {
    id: 'statusType',
    key: 'statusType',
    name: 'Status type',
    valueType: 'equalValue',
    inputType: 'options',
    entityType,
    sortPriority: 15,
    defaultCompareOption: 'is',
    options: () => PROJECT_STATUS_TYPE_OPTIONS,
  }
}

export function collapsedStatusIds(statuses: FilterProjectStatusLike[]): string[] {
  return statuses.filter(status => status.collapsed).map(status => status.id)
}

export const projectBlockFilterUtils = {
  PROJECT_STATUS_TYPE_OPTIONS,
  buildProjectStatusFilterOptions,
  createProjectStatusFilterBlock,
  createProjectStatusTypeFilterBlock,
  collapsedStatusIds,
}

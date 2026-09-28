import type { FilterBlockDefinition } from '../filter-block-types'

export const initiativeFilterBlocks: FilterBlockDefinition[] = [
  { id: 'status', key: 'status', name: 'Status', valueType: 'equalValue', entityType: 'initiative', sortPriority: 10, defaultCompareOption: 'is' },
  { id: 'priority', key: 'priority', name: 'Priority', valueType: 'equalValue', entityType: 'initiative', sortPriority: 20, defaultCompareOption: 'is' },
  { id: 'ownerId', key: 'ownerId', name: 'Owner', valueType: 'equalValue', entityType: 'initiative', sortPriority: 30, defaultCompareOption: 'is' },
  { id: 'creatorId', key: 'creatorId', name: 'Creator', valueType: 'equalValue', entityType: 'initiative', sortPriority: 40, defaultCompareOption: 'is' },
  { id: 'leadTeamId', key: 'leadTeamId', name: 'Lead team', valueType: 'equalValue', entityType: 'initiative', sortPriority: 50, defaultCompareOption: 'is' },
  { id: 'teamId', key: 'teamId', name: 'Team', valueType: 'equalValue', entityType: 'initiative', sortPriority: 60, defaultCompareOption: 'is' },
  { id: 'health', key: 'health', name: 'Health', valueType: 'equalValue', entityType: 'initiative', sortPriority: 70, defaultCompareOption: 'is' },
  { id: 'labelId', key: 'labelId', name: 'Label', valueType: 'equalValue', entityType: 'initiative', sortPriority: 80, defaultCompareOption: 'is' },
  { id: 'date', key: 'dateFilter', name: 'Dates', valueType: 'date', inputType: 'fuzzyDate', entityType: 'initiative', sortPriority: 90, defaultCompareOption: 'within' },
]

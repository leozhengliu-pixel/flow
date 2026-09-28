import type { FilterBlockDefinition } from '../filter-block-types'

export const teamFilterBlocks: FilterBlockDefinition[] = [
  { id: 'name', key: 'name', name: 'Name', valueType: 'str', inputType: 'freeForm', entityType: 'team', sortPriority: 10, defaultCompareOption: 'contains' },
  { id: 'parent', key: 'parentId', name: 'Parent team', valueType: 'equalValue', entityType: 'team', sortPriority: 20, defaultCompareOption: 'is' },
  { id: 'members', key: 'memberId', name: 'Members', valueType: 'equalValue', entityType: 'team', sortPriority: 30, defaultCompareOption: 'is' },
  { id: 'retired', key: 'retired', name: 'Retired', valueType: 'bool', entityType: 'team', sortPriority: 40, defaultCompareOption: 'is' },
]

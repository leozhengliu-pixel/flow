import type { FilterBlockDefinition } from '../filter-block-types'

export const pullRequestFilterBlocks: FilterBlockDefinition[] = [
  { id: 'status', key: 'status', name: 'Status', valueType: 'equalValue', entityType: 'pullRequest', sortPriority: 10, defaultCompareOption: 'is' },
  { id: 'author', key: 'authorId', name: 'Author', valueType: 'equalValue', entityType: 'pullRequest', sortPriority: 20, defaultCompareOption: 'is' },
  { id: 'reviewer', key: 'reviewerId', name: 'Reviewer', valueType: 'equalValue', entityType: 'pullRequest', sortPriority: 30, defaultCompareOption: 'is' },
  { id: 'repository', key: 'repository', name: 'Repository', valueType: 'equalValue', entityType: 'pullRequest', sortPriority: 40, defaultCompareOption: 'is' },
]

export const customerFilterBlocks: FilterBlockDefinition[] = [
  { id: 'status', key: 'status', name: 'Status', valueType: 'equalValue', entityType: 'customer', sortPriority: 10, defaultCompareOption: 'is' },
  { id: 'tier', key: 'tier', name: 'Tier', valueType: 'equalValue', entityType: 'customer', sortPriority: 20, defaultCompareOption: 'is' },
  { id: 'owner', key: 'ownerId', name: 'Owner', valueType: 'equalValue', entityType: 'customer', sortPriority: 30, defaultCompareOption: 'is' },
]

export const documentFilterBlocks: FilterBlockDefinition[] = [
  { id: 'creator', key: 'creatorId', name: 'Creator', valueType: 'equalValue', entityType: 'document', sortPriority: 10, defaultCompareOption: 'is' },
  { id: 'project', key: 'projectId', name: 'Project', valueType: 'equalValue', entityType: 'document', sortPriority: 20, defaultCompareOption: 'is' },
  { id: 'updated', key: 'updatedAt', name: 'Updated', valueType: 'date', inputType: 'fuzzyDate', entityType: 'document', sortPriority: 30, defaultCompareOption: 'within' },
]

export const memberFilterBlocks: FilterBlockDefinition[] = [
  { id: 'status', key: 'status', name: 'Status', valueType: 'equalValue', entityType: 'member', sortPriority: 10, defaultCompareOption: 'is' },
  { id: 'team', key: 'teamId', name: 'Team', valueType: 'equalValue', entityType: 'member', sortPriority: 20, defaultCompareOption: 'is' },
  { id: 'role', key: 'role', name: 'Role', valueType: 'equalValue', entityType: 'member', sortPriority: 30, defaultCompareOption: 'is' },
]

export const feedItemFilterBlocks: FilterBlockDefinition[] = [
  { id: 'type', key: 'type', name: 'Type', valueType: 'equalValue', entityType: 'feedItem', sortPriority: 10, defaultCompareOption: 'is' },
  { id: 'author', key: 'authorId', name: 'Author', valueType: 'equalValue', entityType: 'feedItem', sortPriority: 20, defaultCompareOption: 'is' },
]

export const searchResultFilterBlocks: FilterBlockDefinition[] = [
  { id: 'statusType', key: 'statusType', name: 'Status type', valueType: 'equalValue', entityType: 'searchResult', sortPriority: 10, defaultCompareOption: 'is' },
  { id: 'updatedAt', key: 'updatedAt', name: 'Updated', valueType: 'date', inputType: 'fuzzyDate', entityType: 'searchResult', sortPriority: 20, defaultCompareOption: 'before' },
  { id: 'createdAt', key: 'createdAt', name: 'Created', valueType: 'date', inputType: 'fuzzyDate', entityType: 'searchResult', sortPriority: 30, defaultCompareOption: 'before' },
]

export const workflowDefinitionFilterBlocks: FilterBlockDefinition[] = [
  { id: 'user', key: 'userId', name: 'User', valueType: 'equalValue', entityType: 'workflowDefinition', sortPriority: 10, defaultCompareOption: 'is' },
  { id: 'team', key: 'teamId', name: 'Team', valueType: 'equalValue', entityType: 'workflowDefinition', sortPriority: 20, defaultCompareOption: 'is' },
  { id: 'date', key: 'date', name: 'Date', valueType: 'date', inputType: 'fuzzyDate', entityType: 'workflowDefinition', sortPriority: 30, defaultCompareOption: 'within' },
  { id: 'number', key: 'number', name: 'Number', valueType: 'number', entityType: 'workflowDefinition', sortPriority: 40, defaultCompareOption: 'eq' },
]

export const baseFilterBlocks: FilterBlockDefinition[] = []

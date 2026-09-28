import type { FilterBlockDefinition } from '../filter-block-types'

export const projectFilterBlocks: FilterBlockDefinition[] = [
  { id: 'status', key: 'status', name: 'Status', valueType: 'equalValue', entityType: 'project', sortPriority: 10, defaultCompareOption: 'is' },
  { id: 'priority', key: 'priority', name: 'Priority', valueType: 'equalValue', entityType: 'project', sortPriority: 20, defaultCompareOption: 'is' },
  { id: 'lead', key: 'leadId', name: 'Lead', valueType: 'equalValue', entityType: 'project', sortPriority: 30, defaultCompareOption: 'is' },
  { id: 'members', key: 'memberId', name: 'Members', valueType: 'equalValue', entityType: 'project', sortPriority: 40, defaultCompareOption: 'is' },
  { id: 'health', key: 'health', name: 'Health', valueType: 'equalValue', entityType: 'project', sortPriority: 50, defaultCompareOption: 'is' },
  { id: 'dates', key: 'dateFilter', name: 'Dates', valueType: 'date', inputType: 'fuzzyDate', entityType: 'project', sortPriority: 60, defaultCompareOption: 'within' },
  { id: 'milestones', key: 'milestoneId', name: 'Milestones', valueType: 'equalValue', entityType: 'project', sortPriority: 70, defaultCompareOption: 'is' },
  { id: 'labels', key: 'labelId', name: 'Labels', valueType: 'equalValue', entityType: 'project', sortPriority: 80, defaultCompareOption: 'is' },
  { id: 'teams', key: 'teamId', name: 'Teams', valueType: 'equalValue', entityType: 'project', sortPriority: 90, defaultCompareOption: 'is' },
  { id: 'project', key: 'projectId', name: 'Project', valueType: 'equalValue', entityType: 'project', sortPriority: 100, defaultCompareOption: 'is' },
]

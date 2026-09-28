import type { FilterBlockDefinition } from '../filter-block-types'

export const notificationFilterBlocks: FilterBlockDefinition[] = [
  { id: 'notificationType', key: 'notificationType', name: 'Notification type', valueType: 'equalValue', entityType: 'notification', sortPriority: 10, defaultCompareOption: 'is' },
  { id: 'from', key: 'from', name: 'From', valueType: 'equalValue', entityType: 'notification', sortPriority: 20, defaultCompareOption: 'is' },
  { id: 'project', key: 'projectId', name: 'Project', valueType: 'equalValue', entityType: 'notification', sortPriority: 30, defaultCompareOption: 'is' },
  { id: 'initiative', key: 'initiativeId', name: 'Initiative', valueType: 'equalValue', entityType: 'notification', sortPriority: 40, defaultCompareOption: 'is' },
  { id: 'issuePriority', key: 'issuePriority', name: 'Issue priority', valueType: 'equalValue', entityType: 'notification', sortPriority: 50, defaultCompareOption: 'is' },
  { id: 'issueStatusType', key: 'issueStatusType', name: 'Issue status', valueType: 'equalValue', entityType: 'notification', sortPriority: 60, defaultCompareOption: 'is' },
  { id: 'reviewStatus', key: 'reviewStatus', name: 'Review status', valueType: 'equalValue', entityType: 'notification', sortPriority: 70, defaultCompareOption: 'is' },
]

import type { MyIssuesDisplayOptions, MyIssuesProperty } from '@/components/my-issues/my-issues-surface'
import { createIssueDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'

export const DEFAULT_PROJECT_ISSUE_DISPLAY: MyIssuesDisplayOptions = createIssueDisplayOptions({
  grouping: 'status',
  ordering: 'priority',
  completedWindow: 'all',
  properties: new Set<MyIssuesProperty>(['id', 'status', 'assignee', 'priority', 'dueDate', 'labels', 'created']),
})

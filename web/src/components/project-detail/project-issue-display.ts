import type { MyIssuesDisplayOptions, MyIssuesProperty } from '@/components/my-issues/my-issues-surface'

export const DEFAULT_PROJECT_ISSUE_DISPLAY: MyIssuesDisplayOptions = {
  layout: 'list',
  grouping: 'status',
  groupOrder: 'asc',
  subGrouping: 'none',
  ordering: 'priority',
  completedWindow: 'all',
  orderCompletedByRecency: false,
  showSubIssues: true,
  showEmptyGroups: false,
  nestedSubIssues: false,
  hiddenGroupIds: [],
  properties: new Set<MyIssuesProperty>(['id', 'status', 'assignee', 'priority', 'dueDate', 'labels', 'created']),
}

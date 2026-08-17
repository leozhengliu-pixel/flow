import type { MyIssuesDisplayOptions, MyIssuesProperty } from './my-issues-surface'

export const defaultMyIssuesDisplayOptions: MyIssuesDisplayOptions = {
  layout: 'list',
  grouping: 'focus',
  groupOrder: 'asc',
  subGrouping: 'none',
  ordering: 'importance',
  completedWindow: 'pastDay',
  orderCompletedByRecency: false,
  showSubIssues: true,
  showEmptyGroups: false,
  nestedSubIssues: false,
  properties: new Set<MyIssuesProperty>(['id', 'status', 'assignee', 'priority', 'project', 'labels', 'created']),
}

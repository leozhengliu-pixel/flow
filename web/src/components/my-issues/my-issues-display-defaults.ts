import type { MyIssuesDisplayOptions, MyIssuesProperty } from './my-issues-surface'

export function createIssueDisplayOptions(overrides: Partial<Omit<MyIssuesDisplayOptions,'properties'>> & {properties?:Iterable<MyIssuesProperty>} = {}): MyIssuesDisplayOptions {
  const { properties, ...rest } = overrides
  return {
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
  hiddenGroupIds: [],
  properties: new Set(properties ?? ['id', 'status', 'assignee', 'priority', 'project', 'labels', 'created']),
  ...rest,
  }
}

export const defaultMyIssuesDisplayOptions = createIssueDisplayOptions()

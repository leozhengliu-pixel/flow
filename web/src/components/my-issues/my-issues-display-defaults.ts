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
  // Estimates show by default; rows and group headers hide them for teams that do not use estimates.
  properties: new Set(properties ?? ['id', 'status', 'assignee', 'priority', 'project', 'estimate', 'labels', 'created']),
  ...rest,
  }
}

export const defaultMyIssuesDisplayOptions = createIssueDisplayOptions()

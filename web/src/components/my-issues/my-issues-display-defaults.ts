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

export function issueDisplayEqual(left: MyIssuesDisplayOptions, right: MyIssuesDisplayOptions) {
  const snapshot = (value: MyIssuesDisplayOptions) => Object.fromEntries(Object.entries(value)
    .filter(([, item]) => item !== undefined)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, item]) => [key, item instanceof Set || Array.isArray(item) ? [...item].sort() : item]))
  return JSON.stringify(snapshot(left)) === JSON.stringify(snapshot(right))
}

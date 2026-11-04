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

/**
 * Where a desktop list starts: Due date, Cycle and Pull requests show as well, and sub-issues nest under a matching
 * parent ("Show matching"). Phones keep the plain defaults, which list a sub-issue with its parent trail.
 */
export function desktopDisplayDefaults(): Pick<MyIssuesDisplayOptions, 'nestedSubIssues' | 'properties'> {
  return { nestedSubIssues: true, properties: new Set<MyIssuesProperty>([...defaultMyIssuesDisplayOptions.properties, 'dueDate', 'cycle', 'pullRequests']) }
}

export function issueDisplayEqual(left: MyIssuesDisplayOptions, right: MyIssuesDisplayOptions) {
  const snapshot = (value: MyIssuesDisplayOptions) => Object.fromEntries(Object.entries(value)
    .filter(([, item]) => item !== undefined)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, item]) => [key, item instanceof Set || Array.isArray(item) ? [...item].sort() : item]))
  return JSON.stringify(snapshot(left)) === JSON.stringify(snapshot(right))
}

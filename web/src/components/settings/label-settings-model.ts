import { matchesDateChoice, teamDateChoices } from '@/components/workspace-directory/team-directory-model'
import { isWorkspaceLabel } from '@/lib/labels'
import type { BootstrapData, IssueLabel, LabelResourceType } from '@/types/flow'

/** Columns of Linear's label settings table, in display order. */
export type LabelColumn = 'title' | 'description' | 'team' | 'rules' | 'usage' | 'lastAppliedAt' | 'createdAt' | 'archivedAt'
export type LabelGrouping = 'none' | 'team'

export interface LabelDisplayState {
  grouping: LabelGrouping
  ordering: LabelColumn
  descending: boolean
  showTeamLabels: boolean
  showArchived: boolean
}

export const DEFAULT_LABEL_DISPLAY: LabelDisplayState = { grouping: 'none', ordering: 'title', descending: false, showTeamLabels: false, showArchived: false }

export interface LabelFilters {
  teams: string[]
  teamOperator: 'is' | 'isNot'
  /** `never`, a relative day count from Linear's date choices, or `date:from[/to]`. */
  lastApplied?: string
  lastAppliedOperator: 'after' | 'before'
}

export const EMPTY_LABEL_FILTERS: LabelFilters = { teams: [], teamOperator: 'is', lastAppliedOperator: 'after' }

export const LAST_APPLIED_CHOICES = [{ id: 'never', label: 'Never applied' }, ...teamDateChoices]

export function isFilteringLabels(filters: LabelFilters, query: string) {
  return Boolean(query.trim() || filters.teams.length || filters.lastApplied)
}

/** Columns with a numeric or date value sort descending first, like Linear's `orderingDirection: 'desc'`. */
export const DESCENDING_FIRST: ReadonlySet<LabelColumn> = new Set(['rules', 'usage', 'archivedAt'])

export const LABEL_COLUMN_WIDTHS: Record<LabelColumn, string> = {
  title: 'minmax(240px, 2fr)',
  description: 'minmax(200px, 3fr)',
  team: '100px',
  rules: '75px',
  usage: '75px',
  lastAppliedAt: '102px',
  createdAt: '102px',
  archivedAt: '102px',
}

export interface LabelColumnContext {
  resourceType: LabelResourceType
  rulesVisible: boolean
  showTeamColumn: boolean
  showArchived: boolean
  /** Viewport width; Linear drops columns on laptop, tablet and phone widths. */
  width: number
}

export function visibleLabelColumns({ resourceType, rulesVisible, showTeamColumn, showArchived, width }: LabelColumnContext): LabelColumn[] {
  const laptop = width < 1024, tablet = width < 800, phone = width < 640
  const columns: LabelColumn[] = ['title']
  if (!tablet) columns.push('description')
  if (showTeamColumn && !tablet) columns.push('team')
  if (rulesVisible && resourceType !== 'initiative' && !laptop) columns.push('rules')
  if (!laptop) columns.push('usage')
  if (!phone) columns.push('lastAppliedAt', 'createdAt')
  if (showArchived && !phone) columns.push('archivedAt')
  return columns
}

export function labelGridTemplate(columns: LabelColumn[], resourceType: LabelResourceType) {
  const usage = resourceType === 'initiative' ? '85px' : resourceType === 'project' ? '70px' : '75px'
  return ['[indent] 60px', ...columns.map(column => `[${column}] ${column === 'usage' ? usage : LABEL_COLUMN_WIDTHS[column]}`), '[menu] 28px', '[end-padding] 32px'].join(' ')
}

/** Linear shows the Rules column when SLAs are available or a team triages issues. */
export function labelRulesVisible(data: BootstrapData, resourceType: LabelResourceType) {
  if (resourceType === 'initiative') return false
  const sla = (data.settings?.sla ?? {}) as Record<string, unknown>
  return sla.enabled === true || Object.values(data.teamSettings ?? {}).some(settings => settings?.triageEnabled)
}

export interface LabelRuleUsage { sla: number; triage: Map<string, number>; total: number }

/** SLA rules filtering on the label and team triage rules matching or applying it. */
export function labelRuleUsage(label: IssueLabel, data: BootstrapData): LabelRuleUsage {
  const sla = (data.slaRules ?? []).filter(rule => {
    const value = rule.filters?.label
    return value === label.id || (typeof value === 'string' && value.toLowerCase() === label.name.toLowerCase())
  }).length
  const triage = new Map<string, number>()
  for (const rule of data.triageRoutingRules ?? []) {
    if (rule.conditions?.labelId === label.id || (rule.labelIds ?? []).includes(label.id)) triage.set(rule.teamId, (triage.get(rule.teamId) ?? 0) + 1)
  }
  return { sla, triage, total: sla + [...triage.values()].reduce((sum, count) => sum + count, 0) }
}

export function labelUsage(label: IssueLabel, data: BootstrapData, resourceType: LabelResourceType) {
  return label.issueCount ?? (resourceType === 'issue'
    ? data.issues.filter(issue => !issue.archivedAt && issue.labels.some(item => item.id === label.id)).length
    : resourceType === 'project'
      ? data.projects.filter(project => !project.archivedAt && (project.labelIds ?? []).includes(label.id)).length
      : data.initiatives.filter(initiative => (initiative.labelIds ?? []).includes(label.id)).length)
}

/** Issue counts per team for the label, from the issues loaded on this client. */
export function labelTeamUsage(label: IssueLabel, data: BootstrapData) {
  const usage = new Map<string, number>()
  for (const issue of data.issues) {
    if (issue.archivedAt || !issue.labels.some(item => item.id === label.id)) continue
    usage.set(issue.team.id, (usage.get(issue.team.id) ?? 0) + 1)
  }
  return usage
}

export function matchesLabelFilters(label: IssueLabel, filters: LabelFilters, context: { usage: number; teamUsage: () => Map<string, number>; now?: Date }) {
  if (filters.teams.length) {
    const owned = !isWorkspaceLabel(label) && filters.teams.includes(label.scope ?? '')
    const used = owned || (isWorkspaceLabel(label) && filters.teams.some(team => context.teamUsage().has(team)))
    if (filters.teamOperator === 'is' ? !used : used) return false
  }
  const value = filters.lastApplied
  if (value) {
    const timestamp = label.lastAppliedAt ? Date.parse(label.lastAppliedAt) : NaN
    if (value === 'never') return !Number.isFinite(timestamp) && context.usage === 0
    if (!Number.isFinite(timestamp)) return false
    const inRange = matchesDateChoice(timestamp, value, context.now)
    if (value.startsWith('date:')) return filters.lastAppliedOperator === 'after' ? inRange : !inRange
    return filters.lastAppliedOperator === 'after' ? inRange : !inRange && timestamp < (context.now ?? new Date()).getTime()
  }
  return true
}

export interface LabelSortContext { usage: (label: IssueLabel) => number; rules: (label: IssueLabel) => number; teamName: (label: IssueLabel) => string }

export function compareLabels(left: IssueLabel, right: IssueLabel, ordering: LabelColumn, descending: boolean, context: LabelSortContext) {
  const names = left.name.localeCompare(right.name, undefined, { numeric: true, sensitivity: 'base' })
  const time = (value?: string) => (value ? Date.parse(value) : 0) || 0
  let result: number
  switch (ordering) {
    case 'description': result = (left.description ?? '').localeCompare(right.description ?? ''); break
    case 'team': result = context.teamName(left).localeCompare(context.teamName(right)); break
    case 'rules': result = context.rules(left) - context.rules(right); break
    case 'usage': result = context.usage(left) - context.usage(right); break
    case 'lastAppliedAt': result = time(left.lastAppliedAt) - time(right.lastAppliedAt); break
    case 'createdAt': result = time(left.createdAt) - time(right.createdAt); break
    case 'archivedAt': result = time(left.archivedAt) - time(right.archivedAt); break
    default: result = names
  }
  return (descending ? -result : result) || names
}

/** Linear keeps the broadest, most used, most recently applied label when merging. */
export function mergeTarget(labels: IssueLabel[], usage: (label: IssueLabel) => number) {
  return [...labels].sort((left, right) =>
    Number(!isWorkspaceLabel(left)) - Number(!isWorkspaceLabel(right))
    || usage(right) - usage(left)
    || ((right.lastAppliedAt ? Date.parse(right.lastAppliedAt) : 0) - (left.lastAppliedAt ? Date.parse(left.lastAppliedAt) : 0)),
  )[0]
}

/** Labels can be merged when there are at least two, none archived-group mismatched: all ungrouped or all in one group, same team scope unless merging into a workspace label. */
export function canMergeLabels(labels: IssueLabel[]) {
  if (labels.length < 2) return false
  const group = labels[0].groupId ?? ''
  if (labels.some(label => (label.groupId ?? '') !== group)) return false
  const teamScopes = new Set(labels.filter(label => !isWorkspaceLabel(label)).map(label => label.scope))
  return teamScopes.size <= 1 || labels.some(isWorkspaceLabel)
}

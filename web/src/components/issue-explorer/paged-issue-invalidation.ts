import type { BootstrapData, Issue } from '@/types/flow'
import type { IssueQueryInput } from '@/lib/api'

export const ISSUE_QUERY_INVALIDATED = 'flow-issue-query-invalidated'
export type IssueQueryInvalidation = { workspaceKey: string; issueId?: string; issue?: Issue; force?: boolean; labelIds?: string[] }

export function queryUsesLabels(query: IssueQueryInput): boolean {
  const visit = (node?: Record<string, unknown>): boolean => Boolean(node && (node.field === 'labels' || node.field === 'labelId' || ['and', 'or'].some(key => Array.isArray(node[key]) && node[key].some(visit))))
  return query.groupBy === 'label' || visit(query.filter)
}

// This is an exclusion check, not a second query engine. Unknown predicates
// remain candidates and are reconciled by the authoritative server query.
export function issueMayMatchQuery(issue: Issue, query: IssueQueryInput, data: Pick<BootstrapData, 'teamSettings'>): boolean {
  const ids = (value?: string | string[]) => typeof value === 'string' ? value.split(',') : value
  const inScope = (id: string, values?: string | string[]) => !values || !ids(values)!.length || ids(values)!.includes(id)
  if (query.archived !== 'all' && Boolean(issue.archivedAt) !== (query.archived === 'true')) return false
  if (!inScope(issue.project?.id ?? '', query.projectId) || !inScope(issue.state.id, query.stateId)) return false
  if (query.teamId && !inScope(issue.team.id, query.teamId)) {
    if (!query.includeSubTeams) return false
    const seen = new Set<string>()
    let parent = data.teamSettings?.[issue.team.id]?.parentTeamId
    while (parent && !seen.has(parent) && !inScope(parent, query.teamId)) { seen.add(parent); parent = data.teamSettings?.[parent]?.parentTeamId }
    if (!parent || seen.has(parent)) return false
  }
  const evaluate = (node: Record<string, unknown>): boolean => {
    if (Array.isArray(node.and) && !node.and.every(child => evaluate(child))) return false
    if (Array.isArray(node.or) && node.or.length && !node.or.some(child => evaluate(child))) return false
    const field = String(node.field ?? '')
    const properties: Record<string, string[]> = {
      id: [issue.id], status: [issue.state.id, issue.state.type], stateId: [issue.state.id], priority: [String(issue.priority)],
      assignee: [issue.assignee?.id ?? ''], assigneeId: [issue.assignee?.id ?? ''], creator: [issue.creator.id], creatorId: [issue.creator.id],
      team: [issue.team.id], teamId: [issue.team.id], project: [issue.project?.id ?? ''], projectId: [issue.project?.id ?? ''],
      cycle: [issue.cycleId ?? ''], cycleId: [issue.cycleId ?? ''], labels: issue.labels.map(label => label.id), parent: [issue.parentId ?? ''],
    }
    if (!field || !(field in properties)) return true
    const actual = properties[field], expected = Array.isArray(node.values) ? node.values.map(String) : 'value' in node ? [String(node.value)] : []
    const matched = actual.some(value => expected.includes(value))
    switch (String(node.operator ?? 'is').toLowerCase()) {
      case 'is': case 'in': return matched
      case 'isnot': case 'notin': return !matched
      case 'isempty': return !actual.length || actual.every(value => !value)
      case 'isnotempty': return actual.some(Boolean)
      default: return true
    }
  }
  return !query.filter || evaluate(query.filter)
}

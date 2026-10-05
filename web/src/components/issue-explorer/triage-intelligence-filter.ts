import type { BootstrapData, Issue, IssueLabel } from '@/types/flow'
import type { MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { IssueQueryAstNode } from '@/components/my-issues/my-issues-filter-types'
import { assigneeCandidates } from '@/lib/agent-members'

/**
 * Linear's "Triage Intelligence ▸" filter: Suggested assignee / project / label / team (each with
 * "Any …" and one value per target) and the boolean Suggested relations / Suggested duplicates.
 * Values are `<kind>:<id>` (`<kind>:*` = any) and `related` / `duplicate`. Only active suggestions
 * count; accepted and dismissed ones are no longer on the issue.
 */
export type TriageSuggestionKind = 'assignee' | 'project' | 'label' | 'team'

export const TRIAGE_FILTER_LABELS: Record<TriageSuggestionKind, string> = {
  assignee: 'Suggested assignee', project: 'Suggested project', label: 'Suggested label', team: 'Suggested team',
}

type SuggestionTargets = Pick<MyIssuesRowData, 'suggestedAssigneeIds' | 'suggestedProjectIds' | 'suggestedLabelIds' | 'suggestedTeamIds' | 'suggestedDuplicateIds' | 'suggestedRelatedIds'>

function targets(issue: SuggestionTargets, kind: TriageSuggestionKind) {
  return (kind === 'assignee' ? issue.suggestedAssigneeIds : kind === 'project' ? issue.suggestedProjectIds : kind === 'label' ? issue.suggestedLabelIds : issue.suggestedTeamIds) ?? []
}

export function matchesTriageIntelligence(issue: SuggestionTargets, value: string) {
  if (value === 'related') return Boolean(issue.suggestedRelatedIds?.length)
  if (value === 'duplicate') return Boolean(issue.suggestedDuplicateIds?.length)
  const [kind, id] = splitValue(value)
  if (!kind) return false
  const ids = targets(issue, kind)
  return id === '*' ? ids.length > 0 : ids.includes(id)
}

/** Server vocabulary: sparse `suggested<Kind>:<id>` rows and `suggested:<kind>` presence rows. */
export function triageIntelligenceQueryNode(value: string): IssueQueryAstNode | undefined {
  if (value === 'related' || value === 'duplicate') return { field: `suggested:${value}`, operator: 'isNotEmpty' }
  const [kind, id] = splitValue(value)
  if (!kind || !id) return undefined
  if (id === '*') return { field: `suggested:${kind}`, operator: 'isNotEmpty' }
  return { field: `suggested${kind[0].toUpperCase()}${kind.slice(1)}:${id}`, operator: 'isNotEmpty' }
}

function splitValue(value: string): [TriageSuggestionKind | undefined, string] {
  const index = value.indexOf(':')
  const kind = value.slice(0, index) as TriageSuggestionKind
  return index > 0 && kind in TRIAGE_FILTER_LABELS ? [kind, value.slice(index + 1)] : [undefined, '']
}

export interface TriageCounts { any: Record<TriageSuggestionKind | 'related' | 'duplicate', number>; byTarget: Record<TriageSuggestionKind, Map<string, number>> }

export function emptyTriageCounts(): TriageCounts {
  return { any: { assignee: 0, project: 0, label: 0, team: 0, related: 0, duplicate: 0 }, byTarget: { assignee: new Map(), project: new Map(), label: new Map(), team: new Map() } }
}

export function countTriageSuggestions(counts: TriageCounts, issue: Pick<Issue, 'suggestedAssigneeIds' | 'suggestedProjectIds' | 'suggestedLabelIds' | 'suggestedTeamIds' | 'suggestedDuplicateIds' | 'suggestedRelatedIds'>) {
  for (const kind of ['assignee', 'project', 'label', 'team'] as const) {
    const ids = targets(issue, kind)
    if (ids.length) counts.any[kind] += 1
    for (const id of ids) counts.byTarget[kind].set(id, (counts.byTarget[kind].get(id) ?? 0) + 1)
  }
  if (issue.suggestedRelatedIds?.length) counts.any.related += 1
  if (issue.suggestedDuplicateIds?.length) counts.any.duplicate += 1
}

export function triageIntelligenceFilterOptions(data: Pick<BootstrapData, 'users' | 'projects' | 'teams'>, labels: Pick<IssueLabel, 'id' | 'name' | 'color'>[], counts: TriageCounts): MyIssuesFilterOption[] {
  const value = (kind: TriageSuggestionKind, id: string, label: string, extra: Partial<MyIssuesFilterOption> = {}): MyIssuesFilterOption =>
    ({ id: `${kind}:${id}`, label, filterLabel: TRIAGE_FILTER_LABELS[kind], count: id === '*' ? counts.any[kind] : counts.byTarget[kind].get(id) ?? 0, ...extra })
  const people = assigneeCandidates(data.users.filter(user => user.active))
  return [
    { id: 'triage-assignee', label: TRIAGE_FILTER_LABELS.assignee, kind: 'projectLeadCategory', children: [
      value('assignee', '*', 'Any user', { kind: 'projectLeadCategory' }),
      ...people.map(user => value('assignee', user.id, user.displayName, { kind: 'assignee', avatarUrl: user.avatarUrl, ...(user.app ? { agent: true } : {}) })),
    ] },
    { id: 'triage-project', label: TRIAGE_FILTER_LABELS.project, children: [
      value('project', '*', 'Any project'),
      ...data.projects.filter(project => !project.archivedAt).map(project => value('project', project.id, project.name, { kind: 'project', color: project.color })),
    ] },
    { id: 'triage-label', label: TRIAGE_FILTER_LABELS.label, children: [
      value('label', '*', 'Any label'),
      ...labels.map(label => value('label', label.id, label.name, { kind: 'labels', color: label.color })),
    ] },
    { id: 'triage-team', label: TRIAGE_FILTER_LABELS.team, children: [
      value('team', '*', 'Any team'),
      ...data.teams.map(team => value('team', team.id, team.name)),
    ] },
    { id: 'related', label: 'Suggested relations', filterLabel: 'Triage Intelligence', operatorLabel: 'has', negativeOperatorLabel: 'does not have', count: counts.any.related },
    { id: 'duplicate', label: 'Suggested duplicates', filterLabel: 'Triage Intelligence', operatorLabel: 'has', negativeOperatorLabel: 'does not have', count: counts.any.duplicate },
  ]
}

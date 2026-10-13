import type { ActivityEvent, Issue, IssueRelation } from '@/types/flow'

export type IssueRelationSectionKind = 'blockedBy' | 'blocking' | 'related' | 'duplicateOf' | 'duplicates'

export interface IssueRelationEntry {
  /** Relation id (shared by both issues' rows). */
  id: string
  kind: IssueRelationSectionKind
  targetId: string
  /** Issue whose relation row is deleted to remove it: this issue, or the other one for an inbound-only row. */
  ownerId: string
}

/**
 * Groups an issue's relations into the sidebar sections. Duplicate rows carry no direction, so the issue whose
 * history recorded "marked as duplicate" is the duplicate; the other side lists it under Duplicates.
 * Newest relation first.
 */
export function issueRelationEntries(issue: Issue, issues: Issue[], activities: ActivityEvent[] = []): IssueRelationEntry[] {
  const markedDuplicateOf = new Set(activities.filter(event => event.type === 'issue.relation_added' && event.metadata?.type === 'duplicate').map(event => event.metadata.relatedIssueId))
  const own = issue.relations.flatMap((relation): IssueRelationEntry[] => {
    const kind = ownKind(relation, markedDuplicateOf)
    return kind ? [{ id: relation.id, kind, targetId: relation.relatedIssueId, ownerId: issue.id }] : []
  })
  // Relations recorded only on the other issue (no reciprocal row here).
  const inbound = issues.flatMap(source => source.id === issue.id ? [] : source.relations
    .filter(relation => relation.relatedIssueId === issue.id && !issue.relations.some(item => item.id === relation.id || item.relatedIssueId === source.id))
    .flatMap((relation): IssueRelationEntry[] => {
      const kind = inboundKind(relation)
      return kind ? [{ id: relation.id, kind, targetId: source.id, ownerId: source.id }] : []
    }))
  return [...own, ...inbound].reverse()
}

function ownKind(relation: IssueRelation, markedDuplicateOf: Set<string>): IssueRelationSectionKind | undefined {
  if (relation.type === 'blocked_by') return 'blockedBy'
  if (relation.type === 'blocks') return 'blocking'
  if (relation.type === 'related') return 'related'
  if (relation.type === 'duplicate') return markedDuplicateOf.has(relation.relatedIssueId) ? 'duplicateOf' : 'duplicates'
  return undefined
}

function inboundKind(relation: IssueRelation): IssueRelationSectionKind | undefined {
  if (relation.type === 'blocks') return 'blockedBy'
  if (relation.type === 'blocked_by') return 'blocking'
  if (relation.type === 'related') return 'related'
  if (relation.type === 'duplicate') return 'duplicates'
  return undefined
}

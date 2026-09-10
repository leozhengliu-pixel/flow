import type { BootstrapData, Issue } from '@/types/flow'

export function mergeIssueRecords(current: Issue[], incoming: Issue[], limit = 2000): Issue[] {
  limit = Math.max(0, Math.min(2000, Math.floor(limit)))
  if (!limit) return []
  const existing = new Map(current.map(issue => [issue.id, issue]))
  const result = new Map<string, Issue>()
  for (const issue of incoming) {
    if (!result.has(issue.id) && result.size >= limit) continue
    const previous = result.get(issue.id) ?? existing.get(issue.id)
    if (previous && (previous.version ?? 0) > (issue.version ?? 0)) { result.set(issue.id, previous); continue }
    // Preserve every field omitted by issueListProjection, not just the body.
    if (previous && issue.isSummary && !previous.isSummary) {
      result.set(issue.id, { ...previous, ...issue, isSummary: false,
        needsDetailRefresh: Boolean(previous.needsDetailRefresh || (issue.version ?? 0) > (previous.version ?? 0)),
        description: previous.description, descriptionState: previous.descriptionState, documentContent: previous.documentContent,
        reactions: previous.reactions, subscriberIds: previous.subscriberIds,
      })
    } else result.set(issue.id, !issue.isSummary && issue.needsDetailRefresh ? { ...issue, needsDetailRefresh: false } : issue)
  }
  for (const issue of current) {
    if (result.size >= limit) break
    if (!result.has(issue.id)) result.set(issue.id, issue)
  }
  return [...result.values()]
}

export function mergeWorkspaceDirectory(current: BootstrapData | null, next: BootstrapData, visibleIds?: ReadonlySet<string>): BootstrapData {
  if (!current || current.workspace.id !== next.workspace.id || current.viewer.id !== next.viewer.id || !next.issueCollectionPaged) return next
  const retained = visibleIds ? current.issues.filter(issue => visibleIds.has(issue.id)) : current.issues
  const incoming = visibleIds ? (next.issues ?? []).filter(issue => visibleIds.has(issue.id)) : next.issues ?? []
  let issues = mergeIssueRecords(retained, incoming)
  const issueIds = new Set([...current.issues, ...(next.issues ?? [])].map(issue => issue.id))
  if (visibleIds) {
    const projects = new Set(next.projects.map(project => project.id))
    const labels = new Set(next.labels.map(label => label.id))
    const revoked = (id: string) => issueIds.has(id) && !visibleIds.has(id)
    issues = issues.map(issue => ({ ...issue,
      needsDetailRefresh: !issue.isSummary || issue.needsDetailRefresh,
      project: issue.project && projects.has(issue.project.id) ? issue.project : undefined,
      projectMilestoneId: issue.project && projects.has(issue.project.id) ? issue.projectMilestoneId : undefined,
      labels: issue.labels.filter(label => labels.has(label.id)),
      parentId: issue.parentId && revoked(issue.parentId) ? undefined : issue.parentId,
      subIssueIds: issue.subIssueIds?.filter(id => !revoked(id)),
      relations: issue.relations?.filter(relation => !revoked(relation.relatedIssueId)),
    }))
  }
  const keep = new Set(issues.map(issue => issue.id))
  const retainHistory = <T,>(previous: Record<string,T> = {}, fresh: Record<string,T> = {}): Record<string,T> => ({
    ...Object.fromEntries(Object.entries(fresh).filter(([id]) => !issueIds.has(id) || keep.has(id))),
    ...Object.fromEntries(Object.entries(previous).filter(([id]) => keep.has(id))),
  })
  return {...next, issues,
    comments: retainHistory(current.comments, next.comments),
    activities: retainHistory(current.activities, next.activities),
    issueHistoryCursors: retainHistory(current.issueHistoryCursors, next.issueHistoryCursors),
    issueCollectionRevision: current.issueCollectionRevision,
  }
}

export function requiresIssueVisibilityCheck(eventType: string) {
  return /^(resync$|workspace\.resync_required$|team[._]|workspace_member[._]|membership\.|issue_permission\.|issue\.(permissions_updated|permission_updated|permission_deleted|shared|unshared)$)/.test(eventType)
}

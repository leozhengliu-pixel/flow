import { mergeIssueRecords } from '@/lib/issue-detail-cache'
import { deriveResourceCounts } from '@/lib/resource-counts'
import { syncIssueProjectSummaries } from '@/lib/workspace-metadata-refresh'
import type { BootstrapData, Issue, IssueLabel, Project, RealtimeEvent, WorkflowState } from '@/types/flow'

export type RealtimePatchResult =
  | { handled: true; data: BootstrapData }
  | { handled: false }

function isIssueLike(value: unknown): value is Issue {
  return Boolean(value && typeof value === 'object' && 'identifier' in value && 'id' in value)
}

function isProjectLike(value: unknown): value is Project {
  return Boolean(
    value &&
      typeof value === 'object' &&
      'id' in value &&
      'name' in value &&
      ('slugId' in value || 'status' in value),
  )
}

function stringIds(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((id): id is string => typeof id === 'string') : []
}

/** Issues an event rewrites with a full projection: payload.issue, a matching entity, or a batch's payload.issues. */
function eventIssues(event: RealtimeEvent): Issue[] {
  const payload = event.payload
  if (event.type === 'issue.batch_updated') return Array.isArray(payload?.issues) ? payload.issues.filter(isIssueLike) : []
  if (event.type.startsWith('comment.')) return []
  // issue.created/updated trust payload.issue unconditionally, as before; other issue.* and
  // attachment.* events only patch when the entity is the aggregate itself.
  const trusted = event.type === 'issue.updated' || event.type === 'issue.created'
  if (trusted && payload?.issue) return [payload.issue as Issue]
  const entity = payload?.issue ?? payload?.entity
  if (!isIssueLike(entity)) return []
  if (trusted) return [entity]
  return /^(issue\.|attachment\.)/.test(event.type) && event.aggregateId === entity.id ? [entity] : []
}

/** Ids the viewer must drop: deleted issues (with trashed sub-issues) and rows the viewer can no longer see. */
function removedIssueIds(event: RealtimeEvent): string[] {
  const ids = stringIds(event.payload?.removedIssueIds)
  if (event.type === 'issue.deleted') ids.push(...(event.aggregateId ? [event.aggregateId] : []), ...stringIds(event.payload?.issueIds))
  return ids
}

function upsertById<T extends { id: string }>(items: T[], next: T): T[] {
  return items.some((item) => item.id === next.id)
    ? items.map((item) => (item.id === next.id ? next : item))
    : [next, ...items]
}

function removeById<T extends { id: string }>(items: T[], id: string): T[] {
  return items.filter((item) => item.id !== id)
}

/**
 * Apply an SSE / realtime event as an entity-level patch when the payload is
 * self-contained. Returns `{ handled: false }` so callers can fall back to a
 * full REST bootstrap refresh (Wave 2 progressive store — LS-0718).
 */
export function applyRealtimePatch(
  current: BootstrapData,
  event: RealtimeEvent,
): RealtimePatchResult {
  const entity = event.payload?.entity

  if (event.type.startsWith('notification.') && entity && typeof entity === 'object' && 'recipientId' in entity) {
    const notification = entity as BootstrapData['notifications'][number]
    if (notification.recipientId !== current.viewer.id) {
      return { handled: true, data: current }
    }
    return {
      handled: true,
      data: {
        ...current,
        notifications: upsertById(current.notifications, notification),
      },
    }
  }

  const issues = eventIssues(event)
  const removed = removedIssueIds(event)
  if (issues.length || removed.length) {
    const drop = new Set(removed)
    let next = current.issues.filter(item => !drop.has(item.id))
    for (const issue of issues) {
      next = current.issueCollectionPaged ? mergeIssueRecords(next, [issue]) : upsertById(next, issue)
    }
    return { handled: true, data: deriveResourceCounts({ ...current, issues: next }) }
  }

  if ((event.type === 'project.updated' || event.type === 'project.created') && isProjectLike(entity)) {
    return {
      handled: true,
      data: deriveResourceCounts({
        ...current,
        projects: upsertById(current.projects, entity),
        issues: syncIssueProjectSummaries(current.issues, entity),
      }),
    }
  }

  if (event.type === 'project.deleted' && event.aggregateId) {
    return {
      handled: true,
      data: deriveResourceCounts({
        ...current,
        projects: removeById(current.projects, event.aggregateId),
      }),
    }
  }

  return { handled: false }
}

/** True when the event can be merged locally without a full bootstrap fetch. */
export function canApplyRealtimePatch(event: RealtimeEvent): boolean {
  if (event.type.startsWith('notification.') && event.payload?.entity) return true
  if (event.type === 'issue.deleted') return Boolean(event.aggregateId)
  if (event.type === 'issue.batch_updated') {
    // Ids without projections need a targeted refetch (see realtime-dispatch).
    const issues = event.payload?.issues
    return Array.isArray(issues) && issues.length === stringIds(event.payload?.issueIds).length && eventIssues(event).length === issues.length
  }
  if (eventIssues(event).length) return true
  // Permission changes that hide an issue carry only the ids to drop.
  if (stringIds(event.payload?.removedIssueIds).length && !stringIds(event.payload?.issueIds).some(id => !stringIds(event.payload?.removedIssueIds).includes(id))) return true
  if (event.type === 'project.updated' || event.type === 'project.created') {
    return isProjectLike(event.payload?.entity)
  }
  if (event.type === 'project.deleted') return Boolean(event.aggregateId)
  return false
}

/** Label edits and merges and status edits: a metadata reload plus a local rewrite of the copies issues embed. */
export function isLabelStateRenameEvent(event: Pick<RealtimeEvent, 'type'>): boolean {
  return event.type === 'label.updated' || event.type === 'label.merged' || event.type === 'workflow_state.updated'
}

function entityWithId<T extends { id: string }>(value: unknown): T | undefined {
  return value && typeof value === 'object' && typeof (value as { id?: unknown }).id === 'string' ? value as T : undefined
}

/**
 * Rewrite the label/state copies held on issues from the event's updated object (payload.label / payload.state).
 * Without one nothing changes here; the metadata reload that follows syncs names and colors (syncIssueReferences).
 */
export function applyLabelStateRename(current: BootstrapData, event: RealtimeEvent): BootstrapData {
  const payload = event.payload as Record<string, unknown> | undefined
  if (event.type === 'workflow_state.updated') {
    const state = entityWithId<WorkflowState>(payload?.state)
    if (!state) return current
    const matches = (item: { id: string; teamId?: string }) => item.id === state.id && (!state.teamId || !item.teamId || item.teamId === state.teamId)
    return {
      ...current,
      states: current.states.map(item => matches(item) ? { ...item, ...state } : item),
      issues: current.issues.map(issue => issue.state?.id === state.id && (!state.teamId || issue.team?.id === state.teamId) ? { ...issue, state: { ...issue.state, ...state } } : issue),
    }
  }
  if (event.type === 'label.merged') {
    const toId = typeof payload?.toLabelId === 'string' ? payload.toLabelId : event.aggregateId
    const from = new Set(stringIds(payload?.fromLabelIds))
    const target = entityWithId<IssueLabel>(payload?.label) ?? current.labels.find(label => label.id === toId)
    if (!target || !from.size) return current
    const retarget = (labels: IssueLabel[]) => {
      const next: IssueLabel[] = []
      for (const label of labels) {
        const mapped = from.has(label.id) || label.id === target.id ? { ...label, ...target } : label
        if (!next.some(item => item.id === mapped.id)) next.push(mapped)
      }
      return next
    }
    return {
      ...current,
      labels: current.labels.filter(label => !from.has(label.id)).map(label => label.id === target.id ? { ...label, ...target } : label),
      issues: current.issues.map(issue => issue.labels?.some(label => from.has(label.id)) ? { ...issue, labels: retarget(issue.labels) } : issue),
    }
  }
  const label = entityWithId<IssueLabel>(payload?.label)
  if (!label) return current
  return {
    ...current,
    labels: current.labels.map(item => item.id === label.id ? { ...item, ...label } : item),
    issues: current.issues.map(issue => issue.labels?.some(item => item.id === label.id)
      ? { ...issue, labels: issue.labels.map(item => item.id === label.id ? { ...item, ...label } : item) }
      : issue),
  }
}

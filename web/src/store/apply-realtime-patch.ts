import { mergeIssueRecords } from '@/lib/issue-detail-cache'
import { deriveResourceCounts } from '@/lib/resource-counts'
import type { BootstrapData, Issue, Project, RealtimeEvent } from '@/types/flow'

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

  const issue =
    event.payload?.issue ??
    (isIssueLike(entity) ? entity : undefined)

  if ((event.type === 'issue.updated' || event.type === 'issue.created') && issue) {
    const issues = current.issueCollectionPaged
      ? mergeIssueRecords(current.issues, [issue])
      : upsertById(current.issues, issue)
    return {
      handled: true,
      data: deriveResourceCounts({ ...current, issues }),
    }
  }

  if (event.type === 'issue.deleted' && event.aggregateId) {
    return {
      handled: true,
      data: deriveResourceCounts({
        ...current,
        issues: removeById(current.issues, event.aggregateId),
      }),
    }
  }

  if ((event.type === 'project.updated' || event.type === 'project.created') && isProjectLike(entity)) {
    return {
      handled: true,
      data: deriveResourceCounts({
        ...current,
        projects: upsertById(current.projects, entity),
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
  if (event.type === 'issue.updated' || event.type === 'issue.created') {
    return Boolean(event.payload?.issue || isIssueLike(event.payload?.entity))
  }
  if (event.type === 'issue.deleted') return Boolean(event.aggregateId)
  if (event.type === 'project.updated' || event.type === 'project.created') {
    return isProjectLike(event.payload?.entity)
  }
  if (event.type === 'project.deleted') return Boolean(event.aggregateId)
  return false
}

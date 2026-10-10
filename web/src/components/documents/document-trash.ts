/**
 * A deleted document stays reachable on its URL for the 30 days it spends in
 * the trash: the page renders it read-only with a "Deleted" badge and a
 * "Restore document" action. The trash entry carries the full document JSON.
 */
import type { FlowDocument, TrashEntry } from '@/types/flow'

export interface TrashedDocument {
  entry: TrashEntry
  document: FlowDocument
}

function parsePayload(payload: unknown): Record<string, unknown> | undefined {
  if (typeof payload === 'string') {
    try { return parsePayload(JSON.parse(payload)) } catch { return undefined }
  }
  return payload && typeof payload === 'object' && !Array.isArray(payload) ? payload as Record<string, unknown> : undefined
}

/** Fills the fields a live document always has so the page can render a trashed one unchanged. */
function normalizeTrashedDocument(entry: TrashEntry, payload: Record<string, unknown>): FlowDocument {
  const stamp = typeof payload.updatedAt === 'string' ? payload.updatedAt : entry.deletedAt
  return {
    id: String(payload.id ?? entry.resourceId),
    slugId: String(payload.slugId ?? entry.resourceId),
    title: typeof payload.title === 'string' ? payload.title : entry.title,
    content: typeof payload.content === 'string' ? payload.content : '',
    creator: (payload.creator as FlowDocument['creator'] | undefined) ?? entry.deletedBy,
    projectIds: Array.isArray(payload.projectIds) ? payload.projectIds as string[] : [],
    teamIds: Array.isArray(payload.teamIds) ? payload.teamIds as string[] : [],
    subscriberIds: Array.isArray(payload.subscriberIds) ? payload.subscriberIds as string[] : [],
    favorite: false,
    createdAt: typeof payload.createdAt === 'string' ? payload.createdAt : stamp,
    updatedAt: stamp,
    revisions: Array.isArray(payload.revisions) ? payload.revisions as FlowDocument['revisions'] : [],
    ...payload,
  } as FlowDocument
}

/** The trashed document whose id or slug matches `slugOrId` (most recently deleted first). */
export function findTrashedDocument(trash: TrashEntry[] | undefined, slugOrId: string | undefined): TrashedDocument | undefined {
  if (!slugOrId || !trash?.length) return undefined
  for (const entry of trash) {
    if (entry.resourceType !== 'document') continue
    const payload = parsePayload(entry.payload)
    if (entry.resourceId !== slugOrId && payload?.slugId !== slugOrId && payload?.id !== slugOrId) continue
    if (!payload) continue
    return { entry, document: normalizeTrashedDocument(entry, payload) }
  }
  return undefined
}

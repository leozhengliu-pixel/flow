import type { LocalResume } from './local-store-db'
import type { LocalStoreAuthority } from './local-store-model'

export type LocalSliceKind = 'activity' | 'issue'

export interface LocalSliceStamp {
  authzEpoch: string
  seq: string
  revision: string
  eventCursor: string
}

export interface LocalSliceEntry extends LocalSliceStamp {
  kind: LocalSliceKind
  key: string
  complete: boolean
  cursor?: string | { commentsCursor?: string; activitiesCursor?: string }
  loadedAt: number
  lastAccessAt: number
  bytes: number
}

export interface LocalSlice<T> extends LocalSliceEntry {
  value: T
}

export function localSliceStamp(authority: LocalStoreAuthority & Pick<LocalResume, 'revision' | 'eventCursor'>): LocalSliceStamp {
  return { authzEpoch: authority.authzEpoch, seq: authority.head, revision: authority.revision, eventCursor: authority.eventCursor }
}

export function matchesSliceStamp(slice: LocalSliceStamp, stamp: LocalSliceStamp) {
  return slice.authzEpoch === stamp.authzEpoch && slice.seq === stamp.seq &&
    slice.revision === stamp.revision && slice.eventCursor === stamp.eventCursor
}

export function localHistorySliceKey(id: string, cursor?: { commentsCursor?: string; activitiesCursor?: string }) {
  return `history:issue:${id}${cursor ? `:${JSON.stringify([cursor.commentsCursor, cursor.activitiesCursor])}` : ''}`
}

export function localArchiveSliceKey(teamId: string, limit: number, cursor?: string) {
  return `archive:team:${teamId}:${limit}:${cursor ?? ''}`
}

export function selectSlicesToEvict(entries: readonly LocalSliceEntry[], bytesToFree: number) {
  const selected: LocalSliceEntry[] = []
  const byEvictionOrder = [...entries].sort((a, b) => a.kind === b.kind
    ? a.lastAccessAt - b.lastAccessAt : a.kind === 'activity' ? -1 : 1)
  for (const entry of byEvictionOrder) {
    if (bytesToFree <= 0) break
    selected.push(entry)
    bytesToFree -= entry.bytes
  }
  return selected
}

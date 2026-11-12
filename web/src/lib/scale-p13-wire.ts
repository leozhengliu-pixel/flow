import type { RecordKey, SyncChanges, SyncManifest, SyncRecord, SyncRecordBatch, SyncRecordPage } from '@/types/flow'

export const SYNC_V2_STREAM_SCHEMA = 3
export class SyncWireVersionError extends Error {}

export const syncCollections = {
  issue: 'issues', project: 'projects', initiative: 'initiatives', document: 'documents',
  team: 'teams', user: 'users', state: 'states', label: 'labels', cycle: 'cycles',
  notification: 'notifications', favorite: 'favorites', agentSession: 'agentSessions', ask: 'asks', savedView: 'savedViews',
} as const

const modelKinds = new Map(Object.entries(syncCollections).map(([kind, field]) => [field as string, kind]))
export const wireRecordKind = (kind: string) => Object.hasOwn(syncCollections, kind) ? syncCollections[kind as keyof typeof syncCollections] : kind
export const modelRecordKind = (kind: string) => modelKinds.get(kind) ?? kind

// The canonical record kernel uses Bootstrap field names and a `data` projection.
// Its Go RecordKey currently serializes Kind/ID; HTTP DTOs may use kind/id.
export interface SyncWireRecord {
  key: RecordKey | { Kind: string; ID: string }
  version: string
  data?: Record<string, unknown>
  value?: Record<string, unknown>
  completeness?: SyncRecord['completeness']
  projection?: 'summary' | 'detail'
  detailStale?: boolean
  omitted?: boolean
  missing?: boolean
  deleted?: boolean
}

export function modelRecordKey(key: SyncWireRecord['key']): RecordKey {
  return { kind: modelRecordKind('kind' in key ? key.kind : key.Kind), id: 'id' in key ? key.id : key.ID }
}

function legacyData(data: Record<string, unknown> | undefined) {
  if (!data) return data
  const value = { ...data }
  for (const field of ['version', 'contentVersion']) {
    const version = value[field]
    if (typeof version !== 'string' || !/^\d+$/.test(version)) continue
    const number = Number(version)
    if (!Number.isSafeInteger(number)) throw new SyncWireVersionError('Domain version requires a legacy adapter')
    value[field] = number
  }
  return value
}

function records(items: SyncWireRecord[], projection: 'summary' | 'detail'): SyncRecord[] {
  return (items ?? []).filter(item => !item.missing).map(item => ({
    key: modelRecordKey(item.key), version: item.version,
    value: legacyData(item.data ?? item.value),
    completeness: item.deleted ? 'deleted' : item.omitted ? 'omitted' : item.completeness ?? item.projection ?? projection,
    ...(item.detailStale ? { detailStale: true } : {}),
  }))
}

export function modelRecordBatch(batch: Omit<SyncRecordBatch, 'items'> & { items: SyncWireRecord[] }, projection: 'summary' | 'detail'): SyncRecordBatch {
  return { ...batch, items: records(batch.items, projection) }
}

export function modelRecordPage(page: Omit<SyncRecordPage, 'items'> & { items: SyncWireRecord[] }): SyncRecordPage {
  return { ...page, items: records(page.items, 'summary') }
}

export function modelManifest(manifest: SyncManifest): SyncManifest {
  if (manifest.schema !== 2 || !manifest.capabilities?.includes('sync-v2')) return manifest
  return { ...manifest, collections: Object.fromEntries(Object.entries(manifest.collections).map(([kind, value]) => [modelRecordKind(kind), value])) }
}

export function modelChanges(page: SyncChanges): SyncChanges {
  return { ...page, transactions: (page.transactions ?? []).map(transaction => ({
    ...transaction,
    upserts: records(transaction.upserts as SyncWireRecord[], 'summary'),
    removals: (transaction.removals ?? []).map(item => ({ ...item, key: modelRecordKey(item.key) })),
    invalidations: (transaction.invalidations ?? []).map(modelRecordKind),
  })) }
}

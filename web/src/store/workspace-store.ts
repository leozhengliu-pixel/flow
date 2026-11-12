import { setLiveApplicationIdentities } from '@/lib/actor-attribution'
import { syncClientFor, syncCollections } from '@/lib/sync-client'
import type { QueryWindow } from '@/lib/query-window-store'
import { modelRecordKind } from '@/lib/scale-p13-wire'
import type { BootstrapData, RecordCompleteness, RealtimeEvent, SyncPageRequest } from '@/types/flow'
import { applyRealtimePatch, type RealtimePatchResult } from './apply-realtime-patch'
import {
  buildEntityDirectory,
  getEntityById,
  type EntityDirectory,
} from './entity-directory'
import { normalizeEntityType, type WorkspaceEntityType } from './entity-type'

/**
 * Progressive workspace entity store (LS-0718 / LS-0770).
 * Holds a bootstrap snapshot + O(1) entity Maps. SSE patches merge into the
 * snapshot; IndexedDB realtime cursor stays in `lib/realtime-cache` (not MobX).
 */
export type WorkspaceStore = {
  /** Latest bootstrap snapshot (arrays). Null before workspace load. */
  readonly data: BootstrapData | null
  /** Entity Maps rebuilt from `data` for getById. */
  readonly entities: EntityDirectory
  getById: (type: WorkspaceEntityType | string, id: string) => unknown | undefined
  completeness: (type: string, id: string) => RecordCompleteness
  collectionComplete: (type: string) => boolean
  queryWindow: (query: SyncPageRequest) => QueryWindow | undefined
  hydrateModel: (type: WorkspaceEntityType | string, id: string) => Promise<unknown | undefined>
  applyBootstrap: (next: BootstrapData | null) => WorkspaceStore
  applyPatch: (event: RealtimeEvent) => { store: WorkspaceStore; result: RealtimePatchResult }
}

export function createWorkspaceStore(data: BootstrapData | null = null, previous?: WorkspaceStore): WorkspaceStore {
  const sameScope = data && previous?.data && data.workspace.id === previous.data.workspace.id && data.viewer.id === previous.data.viewer.id && data.sync?.authzEpoch === previous.data.sync?.authzEpoch
  const entities = sameScope ? { ...previous.entities } : buildEntityDirectory(null)
  for (const [kind, field] of Object.entries(syncCollections)) {
    if (sameScope && data[field] === previous.data![field]) continue
    Object.assign(entities, { [kind]: new Map((data?.[field] ?? []).map(item => [item.id, item])) })
  }
  const client = syncClientFor(data)
  // Application attributions on records show the application's current name and logo.
  if (!sameScope || data?.users !== previous?.data?.users) setLiveApplicationIdentities(data?.users)

  const store: WorkspaceStore = {
    data,
    entities,
    getById(type, id) {
      return getEntityById(entities, modelRecordKind(type), id)
    },
    completeness(type, id) {
      const kind = modelRecordKind(normalizeEntityType(type))
      const record = client?.get({ kind, id })
      if (record) return record.completeness
      const value = getEntityById(entities, kind, id) as { isSummary?: boolean } | undefined
      if (data?.sync) return value && ['user', 'team', 'state', 'label', 'cycle', 'favorite'].includes(kind) ? 'detail' : 'omitted'
      return value ? value.isSummary ? 'summary' : 'detail' : 'omitted'
    },
    collectionComplete(type) {
      const kind = modelRecordKind(normalizeEntityType(type))
      if (data?.sync) return data.sync.collections[kind]?.complete === true
      return Boolean(data && !(kind === 'issue' && data.issueCollectionPaged) && !(['document', 'project', 'initiative'].includes(kind) && data.resourceDetailsOmitted))
    },
    queryWindow(query) { return client?.windows.get(query) },
    async hydrateModel(type, id) {
      if (client) {
        if (store.completeness(type, id) === 'detail') return getEntityById(entities, modelRecordKind(type), id)
        const record = await client.readDetail({ kind: normalizeEntityType(type), id })
        return record?.completeness === 'detail' ? record.value : undefined
      }
      return getEntityById(entities, modelRecordKind(type), id)
    },
    applyBootstrap(next) {
      return createWorkspaceStore(next, store)
    },
    applyPatch(event) {
      if (!data) return { store, result: { handled: false } }
      const result = applyRealtimePatch(data, event)
      if (!result.handled) return { store, result }
      return { store: createWorkspaceStore(result.data, store), result }
    },
  }

  return store
}

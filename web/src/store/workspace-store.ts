import type { BootstrapData, RealtimeEvent } from '@/types/flow'
import { applyRealtimePatch, type RealtimePatchResult } from './apply-realtime-patch'
import {
  buildEntityDirectory,
  getEntityById,
  type EntityDirectory,
} from './entity-directory'
import type { WorkspaceEntityType } from './entity-type'

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
  /**
   * Reserved hydrate API (LS-0770 / future LS-0736/0738).
   * Wave 2 resolves from the in-memory directory only; later waves may REST-fetch.
   */
  hydrateModel: (type: WorkspaceEntityType | string, id: string) => Promise<unknown | undefined>
  applyBootstrap: (next: BootstrapData | null) => WorkspaceStore
  applyPatch: (event: RealtimeEvent) => { store: WorkspaceStore; result: RealtimePatchResult }
}

export function createWorkspaceStore(data: BootstrapData | null = null): WorkspaceStore {
  const entities = buildEntityDirectory(data)

  const store: WorkspaceStore = {
    data,
    entities,
    getById(type, id) {
      return getEntityById(entities, type, id)
    },
    async hydrateModel(type, id) {
      // Reserved signature for markdown / AI hydrate hooks (Wave 17 / P2).
      return getEntityById(entities, type, id)
    },
    applyBootstrap(next) {
      return createWorkspaceStore(next)
    },
    applyPatch(event) {
      if (!data) return { store, result: { handled: false } }
      const result = applyRealtimePatch(data, event)
      if (!result.handled) return { store, result }
      return { store: createWorkspaceStore(result.data), result }
    },
  }

  return store
}

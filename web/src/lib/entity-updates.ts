import { useEffect, useRef } from 'react'
import { listInitiativeUpdates, listProjectUpdates } from '@/lib/api'
import type { BootstrapData, InitiativeUpdate, ProjectUpdate, RealtimeEvent } from '@/types/flow'

/**
 * Paged workspaces no longer receive project/initiative updates in the
 * bootstrap. Surfaces that show them ask for the entities they render
 * (`useRequestEntityUpdates`); the app fetches those from
 * GET /api/{projects|initiatives}/{id}/updates and folds them into
 * `data.projectUpdates` / `data.initiativeUpdates`, so every existing
 * consumer and mutation handler keeps working. Non-paged workspaces keep the
 * bootstrap copy and requests are ignored.
 */
export type EntityUpdatesKind = 'project' | 'initiative'
const REQUEST_EVENT = 'flow:entity-updates-request'
type Request = { kind: EntityUpdatesKind; ids: string[] }

/** Asks the app to load updates for these entities (no-op outside paged mode). */
export function requestEntityUpdates(kind: EntityUpdatesKind, ids: readonly (string | undefined)[]) {
  const unique = [...new Set(ids.filter((id): id is string => Boolean(id)))]
  if (unique.length) window.dispatchEvent(new CustomEvent<Request>(REQUEST_EVENT, { detail: { kind, ids: unique } }))
}

/** Requests updates for the given entities whenever the id list changes. */
export function useRequestEntityUpdates(kind: EntityUpdatesKind, ids: readonly (string | undefined)[]) {
  const key = ids.filter(Boolean).join(',')
  useEffect(() => {
    if (key) requestEntityUpdates(kind, key.split(','))
  }, [key, kind])
}

/** Realtime update events name the project/initiative they belong to. */
export function entityUpdatesEventTarget(event: Pick<RealtimeEvent, 'type' | 'aggregateId' | 'payload'>): { kind: EntityUpdatesKind; id: string } | undefined {
  const match = event.type.match(/^(project|initiative)\.update_|^(project|initiative)_update\./)
  if (!match) return undefined
  const kind = (match[1] ?? match[2]) as EntityUpdatesKind
  const payload = (event.payload ?? {}) as Record<string, unknown>
  const id = (kind === 'project' ? payload.projectId : payload.initiativeId) ?? event.aggregateId
  return typeof id === 'string' && id ? { kind, id } : undefined
}

const MAX_CONCURRENT = 4
/** Per request burst; a surface never needs more than a screenful of entities. */
const MAX_PER_REQUEST = 60

type SetData = (update: (current: BootstrapData | null) => BootstrapData | null) => void

export function mergeEntityUpdates(current: BootstrapData, kind: EntityUpdatesKind, id: string, updates: ProjectUpdate[] | InitiativeUpdate[]): BootstrapData {
  return kind === 'project'
    ? { ...current, projectUpdates: { ...(current.projectUpdates ?? {}), [id]: updates as ProjectUpdate[] } }
    : { ...current, initiativeUpdates: { ...(current.initiativeUpdates ?? {}), [id]: updates as InitiativeUpdate[] } }
}

/** App-level loader: fetches requested entities' updates in paged mode. */
export function useEntityUpdatesLoader(data: BootstrapData | null, setData: SetData) {
  const workspaceKey = data?.workspace.urlKey
  const viewerId = data?.viewer.id
  const paged = Boolean(data?.issueCollectionPaged)
  const loaded = useRef(new Set<string>())
  const inflight = useRef(new Set<string>())
  const queue = useRef<{ kind: EntityUpdatesKind; id: string }[]>([])
  const active = useRef(0)

  useEffect(() => {
    loaded.current = new Set()
    inflight.current = new Set()
    queue.current = []
    if (!paged || !workspaceKey || !viewerId) return
    let disposed = false
    const pump = () => {
      while (!disposed && active.current < MAX_CONCURRENT && queue.current.length) {
        const next = queue.current.shift()!
        const key = `${next.kind}:${next.id}`
        active.current += 1
        const load = next.kind === 'project' ? listProjectUpdates(next.id) : listInitiativeUpdates(next.id)
        void load.then(updates => {
          if (disposed) return
          loaded.current.add(key)
          setData(current => current?.workspace.urlKey === workspaceKey && current.viewer.id === viewerId ? mergeEntityUpdates(current, next.kind, next.id, Array.isArray(updates) ? updates : []) : current)
        }).catch(() => undefined).finally(() => {
          inflight.current.delete(key)
          active.current = Math.max(0, active.current - 1)
          pump()
        })
      }
    }
    const enqueue = (kind: EntityUpdatesKind, id: string, force = false) => {
      const key = `${kind}:${id}`
      if (inflight.current.has(key) || (!force && loaded.current.has(key))) return
      inflight.current.add(key)
      queue.current.push({ kind, id })
    }
    const onRequest = (event: Event) => {
      const detail = (event as CustomEvent<Request>).detail
      if (!detail) return
      detail.ids.slice(0, MAX_PER_REQUEST).forEach(id => enqueue(detail.kind, id))
      pump()
    }
    const onActivity = (event: Event) => {
      const target = entityUpdatesEventTarget((event as CustomEvent<RealtimeEvent>).detail ?? { type: '' })
      if (!target || !loaded.current.has(`${target.kind}:${target.id}`)) return
      enqueue(target.kind, target.id, true)
      pump()
    }
    window.addEventListener(REQUEST_EVENT, onRequest)
    window.addEventListener('flow:pulse-activity', onActivity)
    return () => {
      disposed = true
      window.removeEventListener(REQUEST_EVENT, onRequest)
      window.removeEventListener('flow:pulse-activity', onActivity)
    }
  }, [paged, setData, viewerId, workspaceKey])
}

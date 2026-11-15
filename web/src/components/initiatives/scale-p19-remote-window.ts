import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { ApplicationStoreContext } from '@/store/application-store-contexts'
import { scaleP19InitiativesEnabled, scaleP19ManifestMatches } from '@/lib/scale-p19-surfaces'
import { compareSyncVersions, syncClientFor } from '@/lib/sync-client'
import { ApiError } from '@/lib/api-client'
import { readScaleP19Window, SCALE_P19_QUERY_CAPABILITY, type ScaleP19ServerInitiativePage, type ScaleP19ServerQuery } from '@/lib/scale-p19-window-api'
import { cacheStringBytes } from '@/lib/scale-p08'
import { fetchSyncManifest } from '@/lib/api'
import { releaseScaleP19Bytes, retainScaleP19Bytes } from '@/lib/scale-p19-budget'
import type { Initiative } from '@/types/flow'

export const ScaleP19InitiativeContext = createContext<ScaleP19ServerInitiativePage | undefined>(undefined)
export function useScaleP19InitiativeSupport() {
  const context = useContext(ApplicationStoreContext)
  const client = syncClientFor(context?.data)
  return { context, client, enabled: Boolean(client?.manifest.capabilities.includes(SCALE_P19_QUERY_CAPABILITY)) && scaleP19InitiativesEnabled(context?.data) }
}
type Entry = { key: string; kind: 'group'; label: string; count: number; entityName: boolean } | { key: string; kind: 'initiative'; initiative: Initiative; depth: number; childCount: number; contextOnly?: boolean }

export function useScaleP19RemoteInitiativeWindow(query: ScaleP19ServerQuery, selected: Set<string>) {
  const { context, client, enabled } = useScaleP19InitiativeSupport()
  const queryKey = JSON.stringify(query)
  const stableQuery = useMemo(() => JSON.parse(queryKey) as ScaleP19ServerQuery, [queryKey])
  const key = queryKey
  const head = context?.data?.sync?.head
  const [state, setState] = useState<{ client?: typeof client; key?: string; index: number; page?: ScaleP19ServerInitiativePage; loading: boolean; error?: string }>({ index: 0, loading: enabled })
  const trail = useRef<{ client: typeof client; key: string; head?: string; cursors: Map<number, string | undefined>; clock?: { now: string; localMonth: string } }>({ client, key, head, cursors: new Map([[0, undefined]]) })
  const position = useRef(0)
  const request = useRef<AbortController | undefined>(undefined)
  const [selection, setSelection] = useState<{ key: string; page?: ScaleP19ServerInitiativePage; revision: number; items: Map<string, { value: Initiative; version: string }> }>({ key: '', revision: -1, items: new Map() })
  const selectionBytes = useRef(0)
  const owner = useRef({})
  const mounted = useRef(false)
  const load = useCallback(async function readPage(index: number, retried = false): Promise<void> {
    if (!enabled || !client || client.disposed) return
    if (trail.current.client !== client || trail.current.key !== key) { trail.current = { client, key, head, cursors: new Map([[0, undefined]]) }; index = 0 }
    // Date filters refresh with the head; their cursors bind the same clock.
    else if (trail.current.head !== head) trail.current = { client, key, head, cursors: new Map([[0, undefined]]) }
    if (!trail.current.cursors.has(index) && index !== position.current) return
    position.current = index
    request.current?.abort()
    const controller = request.current = new AbortController()
    setState(current => ({ client, key, index, page: current.client === client && current.key === key ? current.page : undefined, loading: true }))
    try {
      let pageIndex = trail.current.cursors.has(index) ? index : 0
      for (;;) {
        if (!trail.current.clock) { const now = new Date(); trail.current.clock = { now: now.toISOString(), localMonth: `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}` } }
        let page: ScaleP19ServerInitiativePage
        try {
          page = await readScaleP19Window<ScaleP19ServerInitiativePage>(client.manifest.core.workspace.urlKey, 'initiatives', { ...trail.current.clock, ...stableQuery }, trail.current.cursors.get(pageIndex), client.requestSignal(controller.signal))
        } catch (error) {
          if (controller.signal.aborted || !mounted.current || client.disposed) return
          if (!(error instanceof ApiError) || error.status !== 409 || retried) throw error
          retried = true
          trail.current.cursors = new Map([[0, undefined]])
          trail.current.clock = undefined
          pageIndex = 0
          continue
        }
        if (controller.signal.aborted || !mounted.current || client.disposed) return
        if (page.authzEpoch !== client.manifest.authzEpoch) { client.requestReset(); return }
        const manifest = await fetchSyncManifest(client.manifest.core.workspace.urlKey, client.requestSignal(controller.signal))
        if (controller.signal.aborted || !mounted.current || client.disposed) return
        if (!scaleP19ManifestMatches(client.manifest, manifest)) { client.requestReset(); return }
        if (!page.entries || !page.aggregates) throw new ApiError('Initiative aggregates unavailable', 501)
        if (page.hasMore) trail.current.cursors.set(pageIndex + 1, page.nextCursor)
        else for (const cursor of trail.current.cursors.keys()) if (cursor > pageIndex) trail.current.cursors.delete(cursor)
        const bytes = cacheStringBytes(JSON.stringify(page)) + cacheStringBytes(JSON.stringify([...trail.current.cursors])) + selectionBytes.current
        if (!retainScaleP19Bytes(owner.current, bytes)) { client.requestReset(true); return }
        // Refreshing a cursor rebuilds only its trail, without publishing
        // intermediate pages or replacing the user's navigation intent.
        if (pageIndex < index && page.hasMore) { pageIndex++; continue }
        position.current = pageIndex
        setState({ client, key, index: pageIndex, page, loading: false })
        return
      }
    } catch (error) {
      if (controller.signal.aborted || !mounted.current || client.disposed) return
      if (error instanceof ApiError && [401, 403, 409].includes(error.status)) { client.requestReset(); return }
      if (error instanceof ApiError && [400, 404, 405, 501].includes(error.status)) { client.requestReset(true); return }
      setState({ client, key, index, loading: false, error: error instanceof Error ? error.message : 'Could not load projects' })
    }
  }, [client, enabled, head, key, stableQuery])
  useEffect(() => { mounted.current = true; const token = owner.current; return () => { mounted.current = false; request.current?.abort(); releaseScaleP19Bytes(token) } }, [])
  useEffect(() => { if (enabled) void load(position.current); return () => request.current?.abort() }, [enabled, load])
  const current = state.client === client && state.key === key && !client?.disposed
  const page = current ? state.page : undefined
  const byId = useMemo(() => new Map(page?.items.map(item => [item.id, item]) ?? []), [page])
  const selectedKey = JSON.stringify([...selected])
  const revision = context?.data?.sync?.revision ?? 0
  const selectedItems = client?.disposed ? [] : (JSON.parse(selectedKey) as string[]).flatMap(id => {
    const record = client?.get({ kind: 'initiative', id })
    const cached = byId.get(id) ?? selection.items.get(id)?.value
    const version = byId.has(id) ? page!.versions[id] : selection.items.get(id)?.version ?? '0'
    if (record?.completeness === 'deleted' && compareSyncVersions(record.version, version) >= 0) return []
    const item = cached && record?.value && compareSyncVersions(record.version, version) > 0 ? { ...cached, ...record.value } as Initiative : cached
    return item ? [item] : []
  })
  if (enabled && (selection.key !== selectedKey || selection.page !== page || selection.revision !== revision)) {
    setSelection({ key: selectedKey, page, revision, items: new Map(selectedItems.map(item => {
      const record = client?.get({ kind: 'initiative', id: item.id })
      const version = byId.has(item.id) ? page!.versions[item.id] : selection.items.get(item.id)?.version ?? '0'
      return [item.id, { value: item, version: record && compareSyncVersions(record.version, version) > 0 ? record.version : version }]
    })) })
  }
  useEffect(() => {
    if (!enabled) return
    selectionBytes.current = cacheStringBytes(JSON.stringify([...selection.items])) + selection.items.size * 64
    if (!retainScaleP19Bytes(owner.current, cacheStringBytes(JSON.stringify(page ?? {})) + cacheStringBytes(JSON.stringify([...trail.current.cursors])) + selectionBytes.current)) client?.requestReset(true)
  }, [client, enabled, page, selection.items])
  const entries = (page?.entries ?? []).flatMap<Entry>(entry => {
    if (entry.kind === 'group') return [{ ...entry, kind: 'group', label: entry.label ?? '', count: entry.count ?? 0, entityName: entry.entityName ?? false }]
    const initiative = byId.get(entry.id!)
    return initiative ? [{ ...entry, kind: 'initiative', initiative }] : []
  })
  return { enabled, entries, page, selectedItems, index: current ? state.index : 0, loading: enabled && (!current || state.loading), error: current ? state.error : undefined, load }
}

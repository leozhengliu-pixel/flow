import { useCallback, useContext, useEffect, useRef, useState } from 'react'
import { ApplicationStoreContext } from '@/store/application-store-contexts'
import { fetchSyncManifest } from '@/lib/api'
import { ApiError } from '@/lib/api-client'
import { initiativeWindowsEnabled, queryWindowManifestMatches, projectWindowsEnabled } from '@/lib/query-window-surfaces'
import { cacheStringBytes } from '@/lib/realtime-cache-limits'
import { releaseQueryWindowBytes, retainQueryWindowBytes } from '@/lib/query-window-budget'
import { syncClientFor } from '@/lib/sync-client'
import type { InitiativeUpdate, ProjectUpdate } from '@/types/flow'
import { readUpdateWindow, type UpdateWindowPage } from '@/lib/query-window-api'

function useUpdateWindow<T extends ProjectUpdate | InitiativeUpdate>(projectId: string, legacy: T[], kind: 'project' | 'initiative') {
  const context = useContext(ApplicationStoreContext)
  const client = syncClientFor(context?.data)
  const enabled = Boolean(client) && Boolean(projectId) && (kind === 'project' ? projectWindowsEnabled(context?.data) : initiativeWindowsEnabled(context?.data))
  const [state, setState] = useState<{ client: typeof client; id: string; items: T[]; loading: boolean; page?: Omit<UpdateWindowPage, 'items'> & { items: T[] }; index: number; error?: string }>({ client, id: projectId, items: [], loading: enabled, index: 0 })
  const cursors = useRef(new Map<number, string | undefined>([[0, undefined]]))
  const request = useRef<AbortController | undefined>(undefined)
  const owner = useRef({})
  useEffect(() => { const token = owner.current; return () => releaseQueryWindowBytes(token) }, [])
  const load = useCallback(async (index = 0) => {
    if (!enabled) return
    request.current?.abort()
    const controller = request.current = new AbortController()
    try {
      const page = await readUpdateWindow<T>(client!.manifest.core.workspace.urlKey, projectId, cursors.current.get(index), client?.requestSignal(controller.signal) ?? controller.signal, kind === 'project' ? 'projects' : 'initiatives')
      const items = page.items
      if (controller.signal.aborted || client?.disposed) return
      if (client) {
        const manifest = await fetchSyncManifest(client.manifest.core.workspace.urlKey, client.requestSignal(controller.signal))
        if (!queryWindowManifestMatches(client.manifest, manifest)) { client.requestReset(); return }
      }
      if (controller.signal.aborted || client?.disposed) return
      if (!Array.isArray(items) || !retainQueryWindowBytes(owner.current, cacheStringBytes(JSON.stringify(items)))) { client?.requestReset(true); return }
      if (page.authzEpoch !== client?.manifest.authzEpoch) { client?.requestReset(); return }
      if (page.hasMore) cursors.current.set(index + 1, page.nextCursor)
      setState({ client, id: projectId, items, page, index, loading: false })
    } catch (error) {
      if (controller.signal.aborted || error instanceof DOMException && error.name === 'AbortError') return
      if (error instanceof ApiError && [401, 403, 409].includes(error.status)) { client?.requestReset(); return }
      if (error instanceof ApiError && [400, 404, 405, 501].includes(error.status)) { client?.requestReset(true); return }
      setState({ client, id: projectId, items: [], index, loading: false, error: error instanceof Error ? error.message : 'Could not load projects' })
    }
  }, [client, enabled, projectId, kind])
  useEffect(() => { cursors.current = new Map([[0, undefined]]); void load(); return () => request.current?.abort() }, [load, context?.data?.sync?.head])
  const current = state.client === client && state.id === projectId && !client?.disposed
  return { enabled, page: current && state.page ? { ...state.page, index: state.index } : undefined, onPage: (index: number) => { setState(current => ({ ...current, loading: true })); return load(index) }, items: enabled ? current ? state.items : [] : legacy, loading: enabled && (!current || state.loading), error: enabled ? state.error : undefined, reload: () => { cursors.current = new Map([[0, undefined]]); if (enabled) setState(current => ({ ...current, loading: true })); return load() } }
}

export function useProjectUpdateWindow(id: string, legacy: ProjectUpdate[]) { return useUpdateWindow(id, legacy, 'project') }
export function useInitiativeUpdateWindow(id: string, legacy: InitiativeUpdate[]) { return useUpdateWindow(id, legacy, 'initiative') }

import { useCallback, useContext, useEffect, useRef, useState } from 'react'
import { ApplicationStoreContext } from '@/store/application-store-contexts'
import { fetchSyncManifest, listProjectUpdates } from '@/lib/api'
import { ApiError } from '@/lib/api-client'
import { scaleP19ManifestMatches, scaleP19ProjectsEnabled } from '@/lib/scale-p19-surfaces'
import { cacheStringBytes } from '@/lib/scale-p08'
import { syncClientFor } from '@/lib/sync-client'
import type { ProjectUpdate } from '@/types/flow'

export function useScaleP19ProjectUpdates(projectId: string, legacy: ProjectUpdate[]) {
  const context = useContext(ApplicationStoreContext)
  const client = syncClientFor(context?.data)
  const enabled = Boolean(client) && Boolean(projectId) && scaleP19ProjectsEnabled(context?.data)
  const [state, setState] = useState<{ client: typeof client; id: string; items: ProjectUpdate[]; loading: boolean; error?: string }>({ client, id: projectId, items: [], loading: enabled })
  const request = useRef<AbortController | undefined>(undefined)
  const load = useCallback(async () => {
    if (!enabled) return
    request.current?.abort()
    const controller = request.current = new AbortController()
    try {
      const items = await listProjectUpdates(projectId, client?.requestSignal(controller.signal) ?? controller.signal)
      if (controller.signal.aborted || client?.disposed) return
      if (client) {
        const manifest = await fetchSyncManifest(client.manifest.core.workspace.urlKey, client.requestSignal(controller.signal))
        if (!scaleP19ManifestMatches(client.manifest, manifest)) { client.requestReset(); return }
        if (!manifest.capabilities.includes('sync-v2')) { client.requestReset(true); return }
      }
      if (!Array.isArray(items) || cacheStringBytes(JSON.stringify(items)) > 8 * 1024 * 1024) { client?.requestReset(true); return }
      setState({ client, id: projectId, items, loading: false })
    } catch (error) {
      if (controller.signal.aborted || error instanceof DOMException && error.name === 'AbortError') return
      if (error instanceof ApiError && [401, 403].includes(error.status)) { client?.requestReset(); return }
      setState({ client, id: projectId, items: [], loading: false, error: error instanceof Error ? error.message : 'Could not load projects' })
    }
  }, [client, enabled, projectId])
  useEffect(() => { void load(); return () => request.current?.abort() }, [load, context?.data?.sync?.revision])
  return { enabled, items: enabled ? state.client === client && state.id === projectId && !client?.disposed ? state.items : [] : legacy, loading: enabled && (state.client !== client || state.id !== projectId || state.loading), error: enabled ? state.error : undefined, reload: load }
}

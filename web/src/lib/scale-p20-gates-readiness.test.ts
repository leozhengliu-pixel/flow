import { afterEach, expect, it, vi } from 'vitest'
import { makeBootstrap } from '@/test/fixtures'
import { ApiError } from '@/lib/api-client'
import { fetchResourceSurfaceBootstrap, refreshResourceWindow } from '@/lib/resource-window-adapters'
import { SyncClient, syncClientFor } from '@/lib/sync-client'

const api = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('@/lib/api-client', async original => ({ ...await original<object>(), request: api.request }))

afterEach(() => { vi.unstubAllEnvs(); vi.resetAllMocks() })

it('scale-p20 returns the authorized legacy bootstrap when member windows are unready', async () => {
  vi.stubEnv('VITE_SYNC_V2', 'true')
  vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
  const core = makeBootstrap()
  let directorySignal: AbortSignal | undefined
  api.request.mockImplementation(async (path: string, options: { signal?: AbortSignal }) => {
    if (path.startsWith('/api/sync/manifest')) return { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, core, authzEpoch: 'epoch', head: '1', capabilities: ['resource-windows'], collections: {} }
    directorySignal = options.signal
    throw new ApiError('Sync records are not ready; use the legacy read endpoint', 503, 'SERVICE_UNAVAILABLE')
  })
  const legacy = vi.fn().mockResolvedValue(core)
  expect(await fetchResourceSurfaceBootstrap(core.workspace.urlKey, 'document-detail', legacy)).toBe(core)
  expect(legacy).toHaveBeenCalledTimes(1)
  expect(directorySignal?.aborted).toBe(true)
  expect(api.request).toHaveBeenCalledTimes(2)
})

it.each([401, 403, 500, 503])('scale-p20 does not classify unrelated failure %s as readiness', async status => {
  vi.stubEnv('VITE_SYNC_V2', 'true')
  vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
  api.request.mockRejectedValue(new ApiError('Read failed', status))
  const legacy = vi.fn()
  await expect(fetchResourceSurfaceBootstrap('workspace', 'document-detail', legacy)).rejects.toMatchObject({ status })
  expect(legacy).not.toHaveBeenCalled()
})

it('scale-p20 builds a fresh authorized directory before replacing a background-event scope', async () => {
  vi.stubEnv('VITE_SYNC_V2', 'true')
  vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
  const core = makeBootstrap()
  const manifest = { schema: 2 as const, workspaceId: core.workspace.id, viewerId: core.viewer.id, core, authzEpoch: 'old', head: '1', capabilities: ['resource-windows'], collections: {} }
  const previous = new SyncClient(manifest, core.workspace.urlKey)
  previous.surface = 'document-directory'
  api.request.mockImplementation(async (path: string) => {
    if (path.startsWith('/api/sync/manifest')) return { ...manifest, authzEpoch: 'fresh', head: '2' }
    if (path.startsWith('/api/sync/changes')) return { next: '2', head: '2', authzEpoch: 'fresh', transactions: [], hasMore: false, resyncRequired: false }
    return { items: [], revision: '2', authzEpoch: 'fresh', next: '', hasMore: false }
  })
  const current = previous.adapt()
  const next = await refreshResourceWindow(current)
  expect(next.sync?.authzEpoch).toBe('fresh')
  expect(syncClientFor(next)).not.toBe(previous)
  expect(current.sync?.authzEpoch).toBe('old')
  expect(next.documents).toEqual([])
  syncClientFor(next)?.dispose()
  previous.dispose()
})

it('scale-p20 rejects denied fresh authority during a background refresh', async () => {
  vi.stubEnv('VITE_SYNC_V2', 'true')
  vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
  const core = makeBootstrap()
  const previous = new SyncClient({ schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, core, authzEpoch: 'old', head: '1', capabilities: ['resource-windows'], collections: {} }, core.workspace.urlKey)
  previous.surface = 'document-directory'
  api.request.mockRejectedValue(new ApiError('Revoked', 403))
  await expect(refreshResourceWindow(previous.adapt())).rejects.toMatchObject({ status: 403 })
  previous.dispose()
})

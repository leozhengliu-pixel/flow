import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeBootstrap, project } from '@/test/fixtures'
import { SyncClient } from '@/lib/sync-client'
import { useScaleP19ProjectWindow } from './scale-p19-project-window'
import { useScaleP19ProjectUpdates } from './scale-p19-project-updates'
import type { SyncManifest, ProjectUpdate } from '@/types/flow'
import { ApiError } from '@/lib/api-client'
import { scaleP19ProjectSummary } from '@/lib/scale-p19-project-summary'

const api = vi.hoisted(() => ({ listProjectRecords: vi.fn(), fetchSyncManifest: vi.fn(), fetchSyncBatch: vi.fn(), listProjectUpdates: vi.fn() }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), ...api }))
afterEach(() => { vi.unstubAllEnvs(); vi.resetAllMocks() })

function scope(principal = 'member', epoch = 'epoch-1', workspace = 'workspace') {
  vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_PROJECT_QUERY_WINDOWS', 'true')
  const core = makeBootstrap()
  core.viewer = { ...core.viewer, id: principal }
  core.workspace = { ...core.workspace, id: workspace, urlKey: workspace }
  const manifest: SyncManifest = { schema: 2, workspaceId: workspace, viewerId: principal, core, authzEpoch: epoch, head: '0', capabilities: ['sync-v2'], collections: {} }
  const client = new SyncClient(manifest, workspace)
  client.surface = 'projects-page'
  const data = client.adapt()
  api.fetchSyncManifest.mockResolvedValue(manifest)
  api.fetchSyncBatch.mockImplementation(async (_workspace: string, keys: Array<{ kind: string; id: string }>) => ({ authzEpoch: epoch, items: keys.map(key => ({ key, version: '1', completeness: 'summary', value: { ...project, id: key.id } })) }))
  const wrapper = ({ children }: { children: ReactNode }) => <WorkspaceStoreProvider data={data} session={null} account={null}>{children}</WorkspaceStoreProvider>
  return { client, manifest, wrapper }
}

describe('scale-p19 scope and authorization barriers', () => {
  it.each(['admin', 'admin-private-outsider', 'member', 'guest-grant', 'private-outsider', 'sub-team', 'key-unrestricted', 'key-selected-empty', 'key-selected-set', 'old-slug'])('uses only the server projection and total for %s', async principal => {
    const { wrapper } = scope(principal)
    const empty = ['private-outsider', 'key-selected-empty'].includes(principal)
    api.listProjectRecords.mockResolvedValue({ items: empty ? [] : [project], total: empty ? 0 : 312, hasMore: !empty, nextCursor: empty ? undefined : 'authorized-position' })
    const hook = renderHook(() => useScaleP19ProjectWindow('workspace', { q: 'old-slug', archived: 'all' }), { wrapper })
    await waitFor(() => expect(hook.result.current.page).toBeDefined())
    expect(hook.result.current.page!.items).toEqual(empty ? [] : [scaleP19ProjectSummary(project)])
    expect(hook.result.current.page!.total).toBe(empty ? 0 : 312)
    expect(api.listProjectRecords.mock.calls[0][2]).toBe('workspace')
  })

  it('rejects a late page after revocation, even if its request returned rows', async () => {
    const { wrapper, client, manifest } = scope()
    let finish!: (value: unknown) => void
    api.listProjectRecords.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const hook = renderHook(() => useScaleP19ProjectWindow('workspace', {}), { wrapper })
    await waitFor(() => expect(api.listProjectRecords).toHaveBeenCalled())
    api.fetchSyncManifest.mockResolvedValue({ ...manifest, authzEpoch: 'revoked' })
    await act(async () => { finish({ items: [project], total: 999, hasMore: false }) })
    await waitFor(() => expect(client.disposed).toBe(true))
    expect(hook.result.current.page).toBeUndefined()
    expect(hook.result.current.window.retainedProjects).toBe(0)
  })

  it('rejects a queued page when permission checking fails and never reports it as an empty directory', async () => {
    const { wrapper, client } = scope()
    api.listProjectRecords.mockRejectedValue(new ApiError('forbidden', 403))
    const hook = renderHook(() => useScaleP19ProjectWindow('workspace', {}), { wrapper })
    await waitFor(() => expect(client.disposed).toBe(true))
    expect(hook.result.current.page).toBeUndefined()
  })

  it('cancels pages on unmount and never promotes their cursors', async () => {
    const { wrapper } = scope()
    api.listProjectRecords.mockImplementation(() => new Promise(() => {}))
    const hook = renderHook(() => useScaleP19ProjectWindow('workspace', {}), { wrapper })
    await waitFor(() => expect(api.listProjectRecords).toHaveBeenCalled())
    const signal = api.listProjectRecords.mock.calls[0][1] as AbortSignal
    hook.unmount()
    expect(signal.aborted).toBe(true)
  })

  it('loads only the open project updates and cancels the previous project on navigation', async () => {
    const { wrapper } = scope()
    const update = { id: 'update-1', body: 'Only this project' } as ProjectUpdate
    api.listProjectUpdates.mockResolvedValue([update])
    const hook = renderHook(({ id }) => useScaleP19ProjectUpdates(id, []), { wrapper, initialProps: { id: 'p-1' } })
    await waitFor(() => expect(hook.result.current.items).toEqual([update]))
    const previousSignal = api.listProjectUpdates.mock.calls[0][1] as AbortSignal
    hook.rerender({ id: 'p-2' })
    await waitFor(() => expect(api.listProjectUpdates).toHaveBeenCalledTimes(2))
    expect(previousSignal.aborted).toBe(true)
    expect(api.listProjectUpdates.mock.calls.map(([id]) => id)).toEqual(['p-1', 'p-2'])
  })
})

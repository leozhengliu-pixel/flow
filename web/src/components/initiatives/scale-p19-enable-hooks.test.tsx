import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { makeBootstrap } from '@/test/fixtures'
import { SyncClient } from '@/lib/sync-client'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { useScaleP19RemoteInitiativeWindow } from './scale-p19-remote-window'
import type { Initiative, SyncManifest } from '@/types/flow'

const api = vi.hoisted(() => ({ readScaleP19Window: vi.fn(), fetchSyncManifest: vi.fn() }))
vi.mock('@/lib/scale-p19-window-api', async original => ({ ...await original<typeof import('@/lib/scale-p19-window-api')>(), ...api }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), fetchSyncManifest: api.fetchSyncManifest }))
afterEach(() => { vi.unstubAllEnvs(); vi.resetAllMocks() })
function scope() {
  vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_INITIATIVE_QUERY_WINDOWS', 'true')
  const core = makeBootstrap({ initiatives: [], projects: [] })
  const manifest: SyncManifest = { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, authzEpoch: 'epoch', head: '0', capabilities: ['sync-v2', 'query-windows-v1'], collections: {}, core }
  const client = new SyncClient(manifest, core.workspace.urlKey)
  api.fetchSyncManifest.mockResolvedValue(manifest)
  const data = client.adapt()
  const wrapper = ({ children }: { children: ReactNode }) => <WorkspaceStoreProvider data={data} session={null} account={null}>{children}</WorkspaceStoreProvider>
  return { client, wrapper }
}
const row = (id: string) => ({ id, name: id, labelIds: [] }) as unknown as Initiative
const result = (id: string, offset = 0) => ({ schema: 1, revision: '99', versions: { [id]: '1' }, authzEpoch: 'epoch', items: [row(id)], entries: [{ kind: 'initiative', key: id, id, depth: 0, childCount: 0 }], aggregates: { [id]: { projects: 23210, completed: 7100, needingUpdate: 6210, hasUpdates: false } }, total: 205, offset, hasMore: true, nextCursor: `cursor-${offset + 1}` })

describe('scale-p19 remote initiative windows', () => {
  it('keeps complete aggregates and selected row labels across page and filter changes', async () => {
    const { wrapper } = scope()
    api.readScaleP19Window.mockResolvedValueOnce(result('first')).mockResolvedValueOnce(result('second', 1)).mockResolvedValueOnce(result('filtered'))
    const hook = renderHook(({ filter, selected }) => useScaleP19RemoteInitiativeWindow({ schema: 1, view: filter }, selected), { wrapper, initialProps: { filter: 'active', selected: new Set<string>() } })
    await waitFor(() => expect(hook.result.current.page?.items[0].id).toBe('first'))
    expect(hook.result.current.page!.aggregates!.first.projects).toBe(23210)
    hook.rerender({ filter: 'active', selected: new Set(['first']) })
    await act(() => hook.result.current.load(1))
    expect(hook.result.current.selectedItems.map(item => item.name)).toEqual(['first'])
    hook.rerender({ filter: 'planned', selected: new Set(['first']) })
    await waitFor(() => expect(hook.result.current.page?.items[0].id).toBe('filtered'))
    expect(hook.result.current.index).toBe(0)
    expect(hook.result.current.selectedItems.map(item => item.name)).toEqual(['first'])
    hook.unmount()
  })
  it('discards a response that arrives after a filter change and cancels the previous request', async () => {
    const { wrapper } = scope()
    let finish!: (value: unknown) => void
    api.readScaleP19Window.mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValueOnce(result('current'))
    const hook = renderHook(({ view }) => useScaleP19RemoteInitiativeWindow({ schema: 1, view }, new Set()), { wrapper, initialProps: { view: 'active' } })
    await waitFor(() => expect(api.readScaleP19Window).toHaveBeenCalledTimes(1))
    const signal = api.readScaleP19Window.mock.calls[0][4] as AbortSignal
    hook.rerender({ view: 'planned' })
    await waitFor(() => expect(hook.result.current.page?.items[0].id).toBe('current'))
    await act(async () => finish(result('stale')))
    expect(signal.aborted).toBe(true)
    expect(hook.result.current.page?.items[0].id).toBe('current')
  })
  it('does not issue window reads with the page flag off', async () => {
    const { wrapper } = scope()
    vi.stubEnv('VITE_INITIATIVE_QUERY_WINDOWS', 'false')
    const hook = renderHook(() => useScaleP19RemoteInitiativeWindow({ schema: 1 }, new Set()), { wrapper })
    expect(hook.result.current.enabled).toBe(false)
    expect(api.readScaleP19Window).not.toHaveBeenCalled()
  })
})

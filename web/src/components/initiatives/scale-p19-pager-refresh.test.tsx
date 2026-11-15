import { act, renderHook, waitFor } from '@testing-library/react'
import { useEffect, useState, type ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api-client'
import { SyncClient } from '@/lib/sync-client'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeBootstrap } from '@/test/fixtures'
import type { Initiative, SyncManifest } from '@/types/flow'
import type { ScaleP19ServerInitiativePage } from '@/lib/scale-p19-window-api'
import { useScaleP19RemoteInitiativeWindow } from './scale-p19-remote-window'

const api = vi.hoisted(() => ({ readScaleP19Window: vi.fn(), fetchSyncManifest: vi.fn() }))
vi.mock('@/lib/scale-p19-window-api', async original => ({ ...await original<typeof import('@/lib/scale-p19-window-api')>(), ...api }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), fetchSyncManifest: api.fetchSyncManifest }))

afterEach(() => { vi.useRealTimers(); vi.unstubAllEnvs(); vi.resetAllMocks() })

function scope() {
  vi.stubEnv('VITE_SYNC_V2', 'true')
  vi.stubEnv('VITE_INITIATIVE_QUERY_WINDOWS', 'true')
  const core = makeBootstrap({ initiatives: [], projects: [] })
  const manifest: SyncManifest = { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, authzEpoch: 'epoch', head: '0', capabilities: ['sync-v2', 'query-windows-v1'], collections: {}, core }
  const client = new SyncClient(manifest, core.workspace.urlKey)
  api.fetchSyncManifest.mockResolvedValue(manifest)
  let advanceHead!: (head: string) => void
  function Wrapper({ children }: { children: ReactNode }) {
    const [data, setData] = useState(() => client.adapt())
    useEffect(() => {
      advanceHead = head => {
        client.install([], [], head)
        setData(current => client.adapt(current))
      }
    }, [])
    return <WorkspaceStoreProvider data={data} session={null} account={null}>{children}</WorkspaceStoreProvider>
  }
  return { client, wrapper: Wrapper, advanceHead: (head: string) => advanceHead(head) }
}

function page(id: string, offset: number, nextCursor?: string): ScaleP19ServerInitiativePage {
  return {
    schema: 1, revision: '99', authzEpoch: 'epoch', versions: { [id]: '1' },
    items: [{ id, name: id, labelIds: [] } as unknown as Initiative],
    entries: [{ kind: 'initiative', key: id, id, depth: 0, childCount: 0 }],
    aggregates: { [id]: { projects: 1, completed: 0, needingUpdate: 0, hasUpdates: false } },
    total: 105, offset, hasMore: Boolean(nextCursor), nextCursor,
  }
}

describe('scale-p19 initiative pager refresh', () => {
  it.each(['created7', 'updated7', 'targetMonth'])('refreshes the %s clock after a same-head cursor expiry without publishing the first rebuilt page', async date => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-11-30T12:00:00Z'))
    const fixture = scope()
    let failPending!: (error: Error) => void
    let finishRebuilt!: (value: ScaleP19ServerInitiativePage) => void
    api.readScaleP19Window.mockResolvedValueOnce(page('November first page', 0, 'november-next'))
      .mockImplementationOnce(() => new Promise((_resolve, reject) => { failPending = reject }))
      .mockResolvedValueOnce(page('December first rebuilt page', 0, 'december-next'))
      .mockImplementationOnce(() => new Promise(resolve => { finishRebuilt = resolve }))
    const hook = renderHook(() => useScaleP19RemoteInitiativeWindow({
      schema: 1, view: 'active', initiativeFilters: { date },
    }, new Set()), { wrapper: fixture.wrapper })
    await waitFor(() => expect(hook.result.current.page?.items[0].name).toBe('November first page'))
    act(() => { void hook.result.current.load(1) })
    await waitFor(() => expect(api.readScaleP19Window).toHaveBeenCalledTimes(2))
    vi.setSystemTime(new Date('2026-12-01T12:00:00Z'))
    await act(async () => failPending(new ApiError('Cursor expired', 409)))
    await waitFor(() => expect(api.readScaleP19Window).toHaveBeenCalledTimes(4))
    expect(fixture.client.manifest.head).toBe('0')
    expect(api.readScaleP19Window.mock.calls.map(call => call[3])).toEqual([undefined, 'november-next', undefined, 'december-next'])
    expect(api.readScaleP19Window.mock.calls.map(call => call[2].now)).toEqual([
      '2026-11-30T12:00:00.000Z', '2026-11-30T12:00:00.000Z',
      '2026-12-01T12:00:00.000Z', '2026-12-01T12:00:00.000Z',
    ])
    expect(api.readScaleP19Window.mock.calls.map(call => call[2].localMonth)).toEqual(['2026-11', '2026-11', '2026-12', '2026-12'])
    expect(hook.result.current.index).toBe(1)
    expect(hook.result.current.loading).toBe(true)
    expect(hook.result.current.page?.items[0].name).toBe('November first page')
    await act(async () => finishRebuilt(page('December later rebuilt page', 100)))
    expect(hook.result.current.page?.items[0].name).toBe('December later rebuilt page')
    expect(hook.result.current.index).toBe(1)
    expect(hook.result.current.loading).toBe(false)
    hook.unmount()
    fixture.client.dispose()
  })

  it.each(['created7', 'updated7', 'targetMonth'])('refreshes the %s clock on a new head while rebuilding the requested page', async date => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-11-30T12:00:00Z'))
    const fixture = scope()
    let finishRefresh!: (value: ScaleP19ServerInitiativePage) => void
    api.readScaleP19Window.mockResolvedValueOnce(page('November first page', 0, 'november-next'))
      .mockResolvedValueOnce(page('November later page', 100))
      .mockResolvedValueOnce(page('December first page', 0, 'december-next'))
      .mockImplementationOnce(() => new Promise(resolve => { finishRefresh = resolve }))
    const hook = renderHook(() => useScaleP19RemoteInitiativeWindow({
      schema: 1, view: 'active', initiativeFilters: { date },
    }, new Set()), { wrapper: fixture.wrapper })
    await waitFor(() => expect(hook.result.current.page?.items[0].name).toBe('November first page'))
    await act(() => hook.result.current.load(1))
    vi.setSystemTime(new Date('2026-12-01T12:00:00Z'))
    act(() => fixture.advanceHead('1'))
    await waitFor(() => expect(api.readScaleP19Window).toHaveBeenCalledTimes(4))
    expect(api.readScaleP19Window.mock.calls.map(call => call[3])).toEqual([undefined, 'november-next', undefined, 'december-next'])
    expect(api.readScaleP19Window.mock.calls.map(call => call[2].now)).toEqual([
      '2026-11-30T12:00:00.000Z', '2026-11-30T12:00:00.000Z',
      '2026-12-01T12:00:00.000Z', '2026-12-01T12:00:00.000Z',
    ])
    expect(api.readScaleP19Window.mock.calls.map(call => call[2].localMonth)).toEqual(['2026-11', '2026-11', '2026-12', '2026-12'])
    expect(hook.result.current.page?.items[0].name).toBe('November later page')
    expect(hook.result.current.index).toBe(1)
    expect(hook.result.current.loading).toBe(true)
    await act(async () => finishRefresh(page('December later page', 100)))
    expect(hook.result.current.page?.items[0].name).toBe('December later page')
    expect(hook.result.current.index).toBe(1)
    hook.unmount()
    fixture.client.dispose()
  })

  it('keeps the requested next page when a realtime head advances during navigation', async () => {
    const fixture = scope()
    let finishPrevious!: (value: ScaleP19ServerInitiativePage) => void
    let nextReads = 0
    api.readScaleP19Window.mockImplementation((_workspace, _kind, _query, cursor) => {
      if (!cursor) return Promise.resolve(page('Initiative 000', 0, 'next-100'))
      if (++nextReads === 1) return new Promise(resolve => { finishPrevious = resolve })
      return Promise.resolve(page('Initiative 100 refreshed', 100))
    })
    const hook = renderHook(() => useScaleP19RemoteInitiativeWindow({ schema: 1, view: 'active' }, new Set()), { wrapper: fixture.wrapper })
    await waitFor(() => expect(hook.result.current.page?.items[0].name).toBe('Initiative 000'))
    act(() => { void hook.result.current.load(1) })
    await waitFor(() => expect(api.readScaleP19Window).toHaveBeenCalledTimes(2))
    const previousSignal = api.readScaleP19Window.mock.calls[1][4] as AbortSignal
    act(() => fixture.advanceHead('1'))
    await waitFor(() => expect(hook.result.current.page?.items[0].name).toBe('Initiative 100 refreshed'))
    expect(hook.result.current.index).toBe(1)
    expect(previousSignal.aborted).toBe(true)
    await act(async () => finishPrevious(page('stale next page', 100)))
    expect(hook.result.current.page?.items[0].name).toBe('Initiative 100 refreshed')
    hook.unmount()
    fixture.client.dispose()
  })

  it('refreshes an already open later page without clearing it or returning to the first page', async () => {
    const fixture = scope()
    let finishRefresh!: (value: ScaleP19ServerInitiativePage) => void
    api.readScaleP19Window.mockResolvedValueOnce(page('Initiative 000', 0, 'next-100'))
      .mockResolvedValueOnce(page('Initiative 100', 100))
      .mockResolvedValueOnce(page('first page after refresh', 0, 'refreshed-next-100'))
      .mockImplementationOnce(() => new Promise(resolve => { finishRefresh = resolve }))
    const hook = renderHook(() => useScaleP19RemoteInitiativeWindow({ schema: 1, view: 'active' }, new Set()), { wrapper: fixture.wrapper })
    await waitFor(() => expect(hook.result.current.page?.items[0].name).toBe('Initiative 000'))
    await act(() => hook.result.current.load(1))
    act(() => fixture.advanceHead('1'))
    await waitFor(() => expect(api.readScaleP19Window).toHaveBeenCalledTimes(4))
    expect(api.readScaleP19Window.mock.calls.map(call => call[3])).toEqual([undefined, 'next-100', undefined, 'refreshed-next-100'])
    expect(hook.result.current.loading).toBe(true)
    expect(hook.result.current.index).toBe(1)
    expect(hook.result.current.page?.items[0].name).toBe('Initiative 100')
    await act(async () => finishRefresh(page('Initiative 100 refreshed', 100)))
    expect(hook.result.current.index).toBe(1)
    expect(hook.result.current.page?.items[0].name).toBe('Initiative 100 refreshed')
    hook.unmount()
    fixture.client.dispose()
  })

  it('rebuilds an expired cursor trail to the requested page without publishing the first page', async () => {
    const fixture = scope()
    let finishLast!: (value: ScaleP19ServerInitiativePage) => void
    api.readScaleP19Window.mockResolvedValueOnce(page('Initiative 000', 0, 'old-next'))
      .mockRejectedValueOnce(new ApiError('Cursor expired', 409))
      .mockResolvedValueOnce(page('first page after refresh', 0, 'new-next'))
      .mockImplementationOnce(() => new Promise(resolve => { finishLast = resolve }))
    const hook = renderHook(() => useScaleP19RemoteInitiativeWindow({ schema: 1, view: 'active' }, new Set()), { wrapper: fixture.wrapper })
    await waitFor(() => expect(hook.result.current.page?.items[0].name).toBe('Initiative 000'))
    act(() => { void hook.result.current.load(1) })
    await waitFor(() => expect(api.readScaleP19Window).toHaveBeenCalledTimes(4))
    expect(api.readScaleP19Window.mock.calls.map(call => call[3])).toEqual([undefined, 'old-next', undefined, 'new-next'])
    expect(hook.result.current.loading).toBe(true)
    expect(hook.result.current.index).toBe(1)
    expect(hook.result.current.page?.items[0].name).toBe('Initiative 000')
    await act(async () => finishLast(page('Initiative 100 refreshed', 100)))
    expect(hook.result.current.index).toBe(1)
    expect(hook.result.current.page?.items[0].name).toBe('Initiative 100 refreshed')
    hook.unmount()
    fixture.client.dispose()
  })

  it('keeps navigation intent if another head arrives while the cursor trail is rebuilding', async () => {
    const fixture = scope()
    let failPrevious!: (error: Error) => void
    api.readScaleP19Window.mockResolvedValueOnce(page('Initiative 000', 0, 'old-next'))
      .mockRejectedValueOnce(new ApiError('Cursor expired', 409))
      .mockImplementationOnce(() => new Promise((_resolve, reject) => { failPrevious = reject }))
      .mockResolvedValueOnce(page('first page after another refresh', 0, 'current-next'))
      .mockResolvedValueOnce(page('Initiative 100 current', 100))
    const hook = renderHook(() => useScaleP19RemoteInitiativeWindow({ schema: 1, view: 'active' }, new Set()), { wrapper: fixture.wrapper })
    await waitFor(() => expect(hook.result.current.page?.items[0].name).toBe('Initiative 000'))
    act(() => { void hook.result.current.load(1) })
    await waitFor(() => expect(api.readScaleP19Window).toHaveBeenCalledTimes(3))
    const previousSignal = api.readScaleP19Window.mock.calls[2][4] as AbortSignal
    act(() => fixture.advanceHead('1'))
    await waitFor(() => expect(hook.result.current.page?.items[0].name).toBe('Initiative 100 current'))
    expect(hook.result.current.index).toBe(1)
    expect(previousSignal.aborted).toBe(true)
    expect(api.readScaleP19Window.mock.calls.map(call => call[3])).toEqual([undefined, 'old-next', undefined, undefined, 'current-next'])
    await act(async () => failPrevious(new ApiError('Superseded cursor expired', 409)))
    expect(api.readScaleP19Window).toHaveBeenCalledTimes(5)
    expect(hook.result.current.page?.items[0].name).toBe('Initiative 100 current')
    expect(hook.result.current.index).toBe(1)
    hook.unmount()
    fixture.client.dispose()
  })
})

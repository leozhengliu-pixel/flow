import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { useResourcePeople } from '@/hooks/scale-p20-resource-pickers'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import { loadResourcePeople } from '@/lib/resource-window-adapters'

const api = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('@/lib/api-client', async importOriginal => ({ ...await importOriginal<object>(), request: api.request }))
afterEach(() => { vi.unstubAllEnvs(); vi.resetAllMocks() })
const peoplePage = (users: typeof viewer[], hasMore = false, next = '') => ({ items: users.map(user => ({ key: { kind: 'users', id: user.id }, version: '1', data: user })), revision: '1', authzEpoch: 'epoch', hasMore, next })

describe('scale-p20 people picker scope and completeness', () => {
  it('does not search with flags off', () => {
    renderHook(() => useResourcePeople([viewer]))
    expect(api.request).not.toHaveBeenCalled()
  })

  it('follows every page, retaining the complete set for fuzzy search and explicit selection', async () => {
    api.request.mockResolvedValueOnce(peoplePage([viewer], true, 'next'))
      .mockResolvedValueOnce(peoplePage([teammate]))
    expect(await loadResourcePeople('workspace', new AbortController().signal)).toEqual([viewer, teammate])
    expect(api.request.mock.calls[1][0]).toContain('cursor=next')
    expect(api.request.mock.calls[1][1].headers).toEqual({ 'X-Workspace-Key': 'workspace' })
  })

  it('rejects a nonadvancing page instead of declaring a partial set complete', async () => {
    api.request.mockResolvedValue(peoplePage([viewer], true))
    await expect(loadResourcePeople('workspace', new AbortController().signal)).rejects.toThrow('did not advance')
  })

  it('retains a departed reference as inactive, and ignores a response from the previous viewer', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_PICKER_WINDOWS', 'true')
    let data = makeBootstrap()
    let finish!: (value: unknown) => void
    api.request.mockResolvedValueOnce(peoplePage([viewer]))
    const wrapper = ({ children }: { children: ReactNode }) => <WorkspaceStoreProvider data={data} session={null} account={null}>{children}</WorkspaceStoreProvider>
    const view = renderHook(() => useResourcePeople(data.users), { wrapper })
    await waitFor(() => expect(view.result.current.find(user => user.id === teammate.id)?.active).toBe(false))
    api.request.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    data = makeBootstrap({ viewer: { ...viewer, id: 'other-viewer' } })
    view.rerender()
    await waitFor(() => expect(api.request).toHaveBeenCalledTimes(2))
    const oldSignal = api.request.mock.calls[1][1].signal as AbortSignal
    api.request.mockResolvedValueOnce(peoplePage([teammate]))
    data = makeBootstrap({ workspace: { ...data.workspace, id: 'other-workspace', urlKey: 'other-workspace' } })
    view.rerender()
    expect(oldSignal.aborted).toBe(true)
    await waitFor(() => expect(view.result.current.find(user => user.id === viewer.id)?.active).toBe(false))
    await act(async () => { finish(peoplePage([viewer])) })
    expect(view.result.current.find(user => user.id === viewer.id)?.active).toBe(false)
    view.unmount()
  })

  it('keeps intentionally restricted picker pools intact instead of adding workspace-wide candidates', () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_PICKER_WINDOWS', 'true')
    const data = makeBootstrap({ users: [viewer, teammate] })
    const users = [viewer]
    const wrapper = ({ children }: { children: ReactNode }) => <WorkspaceStoreProvider data={data} session={null} account={null}>{children}</WorkspaceStoreProvider>
    const view = renderHook(() => useResourcePeople(users), { wrapper })
    expect(view.result.current).toBe(users)
    expect(api.request).not.toHaveBeenCalled()
  })
})

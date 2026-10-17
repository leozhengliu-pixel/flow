import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api-client'
import { clearNavigationCache, rememberWorkspace, writeNavigationCacheWhenIdle, readNavigationCache } from '@/lib/navigation-cache'
import { settleBootstrapRequest, startStartup, type StartupDeps } from '@/lib/startup'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { AccountBootstrap, AuthSession } from '@/types/flow'

const session = { user: { id: viewer.id } } as unknown as AuthSession
const accountFor = (viewerId: string, keys: string[]) =>
  ({ viewer: { id: viewerId }, workspaces: keys.map(urlKey => ({ workspace: { urlKey } })), lastWorkspaceKey: keys[0] }) as unknown as AccountBootstrap

function deps(overrides: Partial<StartupDeps> = {}) {
  const calls: string[] = []
  const value: StartupDeps = {
    fetchSession: vi.fn(async () => { calls.push('session'); return session }),
    fetchAccount: vi.fn(async () => { calls.push('account'); return accountFor(viewer.id, ['acme']) }),
    fetchBootstrap: vi.fn(async (key: string) => { calls.push(`bootstrap:${key}`); return makeBootstrap() }),
    ...overrides,
  }
  return { value, calls }
}

describe('startup ordering', () => {
  beforeEach(() => { localStorage.clear(); clearNavigationCache() })

  it('requests session and account together before either settles', async () => {
    const resolvers: Array<() => void> = []
    const hold = <T,>(result: T) => () => new Promise<T>(resolve => resolvers.push(() => resolve(result)))
    const fetchSession = vi.fn(hold(session)), fetchAccount = vi.fn(hold(accountFor(viewer.id, ['acme'])))
    const { value } = deps({ fetchSession, fetchAccount })
    const { ready } = startStartup(value, { requestedWorkspaceKey: 'acme', isRoot: false })
    expect(fetchSession).toHaveBeenCalledTimes(1)
    expect(fetchAccount).toHaveBeenCalledTimes(1)
    resolvers.forEach(resolve => resolve())
    await expect(ready).resolves.toMatchObject({ session })
  })

  it('does not speculate for a workspace key this device has not used', async () => {
    rememberWorkspace(viewer.id, 'acme')
    const { value } = deps()
    const { speculative, ready } = startStartup(value, { requestedWorkspaceKey: 'other', isRoot: false })
    await ready
    expect(speculative).toBeUndefined()
    expect(value.fetchBootstrap).not.toHaveBeenCalled()
  })

  it('does not speculate when nothing was remembered', async () => {
    const { value } = deps()
    const { speculative, ready } = startStartup(value, { requestedWorkspaceKey: 'acme', isRoot: false })
    await ready
    expect(speculative).toBeUndefined()
    expect(value.fetchBootstrap).not.toHaveBeenCalled()
  })

  it('starts the remembered workspace bootstrap with session and account, and keeps it when membership matches', async () => {
    rememberWorkspace(viewer.id, 'acme')
    const { value, calls } = deps()
    const { speculative, ready } = startStartup(value, { requestedWorkspaceKey: 'acme', isRoot: false, projection: 'issue-detail' })
    expect([...calls].sort()).toEqual(['account', 'bootstrap:acme', 'session'])
    expect(value.fetchBootstrap).toHaveBeenCalledWith('acme', 'issue-detail', expect.any(AbortSignal), true)
    expect(speculative?.key).toBe(`${viewer.id}:acme:issue-detail`)
    await ready
    expect(speculative?.controller.signal.aborted).toBe(false)
  })

  it('speculates on the remembered workspace from the root route', async () => {
    rememberWorkspace(viewer.id, 'acme')
    const { value } = deps()
    const { speculative, ready } = startStartup(value, { requestedWorkspaceKey: '', isRoot: true })
    await ready
    expect(speculative?.key).toBe(`${viewer.id}:acme:full`)
    expect(speculative?.controller.signal.aborted).toBe(false)
  })

  it('discards the speculative request when membership is gone', async () => {
    rememberWorkspace(viewer.id, 'acme')
    const { value } = deps({ fetchAccount: async () => accountFor(viewer.id, ['elsewhere']) })
    const { speculative, ready } = startStartup(value, { requestedWorkspaceKey: 'acme', isRoot: false })
    await ready
    expect(speculative?.controller.signal.aborted).toBe(true)
  })

  it('discards the speculative request when another account is signed in', async () => {
    rememberWorkspace('someone-else', 'acme')
    const { value } = deps()
    const { speculative, ready } = startStartup(value, { requestedWorkspaceKey: 'acme', isRoot: false })
    await ready
    expect(speculative?.controller.signal.aborted).toBe(true)
  })

  it('discards it and fails when the viewer is logged out', async () => {
    rememberWorkspace(viewer.id, 'acme')
    const { value } = deps({ fetchAccount: async () => { throw new ApiError('no', 401) } })
    const { speculative, ready } = startStartup(value, { requestedWorkspaceKey: 'acme', isRoot: false })
    await expect(ready).rejects.toBeInstanceOf(ApiError)
    expect(speculative?.controller.signal.aborted).toBe(true)
  })

  it('does not raise unhandled rejections when the speculative call is refused', async () => {
    rememberWorkspace(viewer.id, 'acme')
    const { value } = deps({ fetchBootstrap: async () => { throw new ApiError('forbidden', 403) } })
    const { speculative, ready } = startStartup(value, { requestedWorkspaceKey: 'acme', isRoot: false })
    await ready
    await expect(settleBootstrapRequest(speculative!, async () => makeBootstrap())).resolves.toBeTruthy()
  })

  it('retries a refused speculative request normally and passes other errors through', async () => {
    rememberWorkspace(viewer.id, 'acme')
    const retry = vi.fn(async () => makeBootstrap())
    const refused = startStartup(deps({ fetchBootstrap: async () => { throw new ApiError('nope', 404) } }).value, { requestedWorkspaceKey: 'acme', isRoot: false })
    await refused.ready
    await settleBootstrapRequest(refused.speculative!, retry)
    expect(retry).toHaveBeenCalledTimes(1)
    const broken = startStartup(deps({ fetchBootstrap: async () => { throw new ApiError('boom', 500) } }).value, { requestedWorkspaceKey: 'acme', isRoot: false })
    await broken.ready
    await expect(settleBootstrapRequest(broken.speculative!, retry)).rejects.toMatchObject({ status: 500 })
    expect(retry).toHaveBeenCalledTimes(1)
  })
})

describe('navigation cache writes', () => {
  beforeEach(() => { localStorage.clear(); clearNavigationCache() })

  it('serialise after the current task, not synchronously', async () => {
    const data = makeBootstrap()
    writeNavigationCacheWhenIdle(viewer.id, data.workspace.urlKey, data)
    expect(readNavigationCache(viewer.id, data.workspace.urlKey)).toBeUndefined()
    await new Promise(resolve => setTimeout(resolve, 10))
    expect(readNavigationCache(viewer.id, data.workspace.urlKey)?.workspace.urlKey).toBe(data.workspace.urlKey)
  })

  it('are cancelled by logout', async () => {
    const data = makeBootstrap()
    writeNavigationCacheWhenIdle(viewer.id, data.workspace.urlKey, data)
    clearNavigationCache()
    await new Promise(resolve => setTimeout(resolve, 10))
    expect(readNavigationCache(viewer.id, data.workspace.urlKey)).toBeUndefined()
  })

  it('forgets the remembered workspace on logout', () => {
    rememberWorkspace(viewer.id, 'acme')
    clearNavigationCache()
    const { value } = deps()
    expect(startStartup(value, { requestedWorkspaceKey: 'acme', isRoot: false }).speculative).toBeUndefined()
  })
})

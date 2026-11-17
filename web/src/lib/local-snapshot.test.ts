import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AccountBootstrap, AuthSession, BootstrapData } from '@/types/flow'

/** Just enough of IndexedDB for the snapshot store: one database, object stores keyed by a path, async callbacks. */
function fakeIndexedDB() {
  const stores = new Map<string, Map<string, unknown>>()
  const later = (run: () => void) => setTimeout(run, 0)
  const request = <T,>(produce: () => T) => {
    const result: { result?: T; onsuccess?: () => void; onerror?: () => void } = {}
    later(() => { result.result = produce(); result.onsuccess?.() })
    return result
  }
  const database = {
    objectStoreNames: { contains: (name: string) => stores.has(name) },
    createObjectStore: (name: string) => { stores.set(name, new Map()) },
    close: () => undefined,
    onversionchange: null as null | (() => void),
    transaction: (name: string) => {
      const store = stores.get(name)!
      const transaction = { oncomplete: undefined as undefined | (() => void), onerror: undefined, onabort: undefined, objectStore: () => objectStore }
      const objectStore = {
        get: (key: string) => request(() => structuredClone(store.get(key))),
        put: (value: { key: string }) => request(() => { store.set(value.key, structuredClone(value)); return value.key }),
        delete: (key: string) => request(() => { store.delete(key) }),
        clear: () => request(() => { store.clear() }),
        getAllKeys: () => request(() => [...store.keys()]),
      }
      later(() => later(() => transaction.oncomplete?.()))
      return transaction
    },
  }
  return {
    stores,
    open: () => {
      const open: { result?: typeof database; onupgradeneeded?: () => void; onsuccess?: () => void; onerror?: () => void; onblocked?: () => void } = {}
      later(() => {
        open.result = database
        if (!stores.has('snapshots')) open.onupgradeneeded?.()
        open.onsuccess?.()
      })
      return open
    },
  }
}

const viewer = { id: 'user-a', name: 'A', email: 'a@example.test' }
function session(expiresAt = '2999-01-01T00:00:00Z', userId = viewer.id): AuthSession {
  return { user: { ...viewer, id: userId }, memberships: [], expiresAt } as unknown as AuthSession
}
function account(keys = ['acme'], userId = viewer.id, last = keys[0]): AccountBootstrap {
  return { viewer: { ...viewer, id: userId }, workspaces: keys.map(urlKey => ({ workspace: { id: `ws-${urlKey}`, urlKey, name: urlKey } })), lastWorkspaceKey: last, workspaceRegionSelectorEnabled: false, workspaceDefaultRegion: 'us' } as unknown as AccountBootstrap
}
function data(urlKey = 'acme', userId = viewer.id, title = 'Cached issue'): BootstrapData {
  return {
    viewer: { ...viewer, id: userId }, workspace: { id: `ws-${urlKey}`, urlKey, name: urlKey },
    issues: [{ id: 'issue-1', identifier: 'ACM-1', title, description: 'secret body', labels: [] }],
    users: [], teams: [], states: [], labels: [], projects: [], documents: [], comments: {}, activities: {}, notifications: [{ id: 'n1' }],
  } as unknown as BootstrapData
}

let indexed: ReturnType<typeof fakeIndexedDB>
let snapshot: typeof import('./local-snapshot')

beforeEach(async () => {
  localStorage.clear()
  vi.resetModules()
  indexed = fakeIndexedDB()
  vi.stubGlobal('indexedDB', indexed)
  snapshot = await import('./local-snapshot')
  localStorage.setItem('flow:last-workspace', JSON.stringify({ viewerId: viewer.id, workspaceKey: 'acme' }))
})
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })

describe('local workspace snapshot', () => {
  it('saves a confirmed workspace and reads it back for the same viewer and workspace only', async () => {
    expect(await snapshot.saveLocalSnapshot({ session: session(), account: account(), data: data() })).toBe('saved')
    const read = await snapshot.readLocalSnapshot(viewer.id, 'acme')
    expect(read?.data.issues[0].title).toBe('Cached issue')
    // Stored as the navigation snapshot: no issue bodies, no inbox rows.
    expect(read?.data.issues[0].description).toBeUndefined()
    expect(read?.data.notifications).toEqual([])
    expect(await snapshot.readLocalSnapshot('user-b', 'acme')).toBeUndefined()
    expect(await snapshot.readLocalSnapshot(viewer.id, 'other')).toBeUndefined()
  })

  it('refuses data whose viewer or workspace does not match the session and account', async () => {
    expect(await snapshot.saveLocalSnapshot({ session: session(undefined, 'user-b'), account: account(), data: data() })).toBe('invalid')
    expect(await snapshot.saveLocalSnapshot({ session: session(), account: account(['other']), data: data() })).toBe('invalid')
    expect(await snapshot.saveLocalSnapshot({ session: session('2000-01-01T00:00:00Z'), account: account(), data: data() })).toBe('invalid')
    expect(await snapshot.saveLocalSnapshot({ session: session(), account: account(), data: { ...data(), sync: {} } as unknown as BootstrapData })).toBe('invalid')
    expect(indexed.stores.get('snapshots')?.size ?? 0).toBe(0)
  })

  it('ignores and deletes a snapshot from another schema, another build, or an expired session', async () => {
    await snapshot.saveLocalSnapshot({ session: session(), account: account(), data: data() })
    const store = indexed.stores.get('snapshots')!
    const [key] = [...store.keys()]
    const record = store.get(key) as { schema: number; build: string }
    store.set(key, { ...record, schema: snapshot.LOCAL_SNAPSHOT_SCHEMA + 1 })
    expect(await snapshot.readLocalSnapshot(viewer.id, 'acme')).toBeUndefined()
    await vi.waitFor(() => expect(store.size).toBe(0))

    await snapshot.saveLocalSnapshot({ session: session(), account: account(), data: data() })
    const [again] = [...store.keys()]
    store.set(again, { ...(store.get(again) as object), build: '/assets/index-OldBuild1.js' })
    expect(await snapshot.readLocalSnapshot(viewer.id, 'acme')).toBeUndefined()

    await snapshot.saveLocalSnapshot({ session: session('2030-01-01T00:00:00Z'), account: account(), data: data() })
    expect(await snapshot.readLocalSnapshot(viewer.id, 'acme', Date.parse('2031-01-01T00:00:00Z'))).toBeUndefined()
  })

  it('is unreadable as soon as it is cleared, even before the IndexedDB delete finishes', async () => {
    await snapshot.saveLocalSnapshot({ session: session(), account: account(['acme', 'beta']), data: data() })
    await snapshot.saveLocalSnapshot({ session: session(), account: account(['acme', 'beta']), data: data('beta') })
    void snapshot.clearLocalSnapshots(viewer.id, 'acme')
    expect(await snapshot.readLocalSnapshot(viewer.id, 'acme')).toBeUndefined()
    expect(await snapshot.readLocalSnapshot(viewer.id, 'beta')).toBeDefined()
    // Logout (everything) and session caches cleared through the registry (clearNavigationCache) both apply.
    const { clearNavigationCache } = await import('./navigation-cache')
    clearNavigationCache(viewer.id)
    expect(await snapshot.readLocalSnapshot(viewer.id, 'beta')).toBeUndefined()
    await vi.waitFor(() => expect(indexed.stores.get('snapshots')!.size).toBe(0))
  })

  it('drops a save that a clear overtook', async () => {
    const saving = snapshot.saveLocalSnapshot({ session: session(), account: account(), data: data() })
    void snapshot.clearLocalSnapshots()
    expect(await saving).toBe('superseded')
    expect(await snapshot.readLocalSnapshot(viewer.id, 'acme')).toBeUndefined()
  })

  it('skips a snapshot above the size cap and removes the older one it would replace', async () => {
    await snapshot.saveLocalSnapshot({ session: session(), account: account(), data: data() })
    const huge = data('acme', viewer.id, 'x'.repeat(snapshot.LOCAL_SNAPSHOT_MAX_BYTES / 2))
    expect(await snapshot.saveLocalSnapshot({ session: session(), account: account(), data: huge })).toBe('too-large')
    expect(await snapshot.readLocalSnapshot(viewer.id, 'acme')).toBeUndefined()
  })

  it('falls back to no snapshot without IndexedDB or localStorage', async () => {
    vi.stubGlobal('indexedDB', undefined)
    vi.resetModules()
    const withoutIdb = await import('./local-snapshot')
    expect(await withoutIdb.saveLocalSnapshot({ session: session(), account: account(), data: data() })).toBe('unavailable')
    expect(await withoutIdb.readLocalSnapshot(viewer.id, 'acme')).toBeUndefined()
    const getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('denied') })
    try {
      expect(await snapshot.saveLocalSnapshot({ session: session(), account: account(), data: data() })).toBe('unavailable')
      expect(await snapshot.prepareStartupSnapshot('/acme/inbox')).toBeUndefined()
    } finally { getItem.mockRestore() }
  })

  it('picks the startup snapshot from the address: the workspace in it, or the last one on the root page', async () => {
    await snapshot.saveLocalSnapshot({ session: session(), account: account(['acme', 'beta'], viewer.id, 'acme'), data: data() })
    expect((await snapshot.prepareStartupSnapshot('/acme/team/ACM/all'))?.workspaceKey).toBe('acme')
    expect(snapshot.startupSnapshot()?.workspaceKey).toBe('acme')
    expect((await snapshot.prepareStartupSnapshot('/'))?.workspaceKey).toBe('acme')
    for (const path of ['/login', '/auth/google/callback', '/join', '/invite/abc', '/oauth/authorize', '/acme/welcome', '/beta/inbox']) {
      expect(await snapshot.prepareStartupSnapshot(path), path).toBeUndefined()
      expect(snapshot.startupSnapshot()).toBeUndefined()
    }
    expect(await snapshot.prepareStartupSnapshot('/acme/inbox', { enabled: false })).toBeUndefined()
    snapshot.discardStartupSnapshot()
    expect(snapshot.startupSnapshot()).toBeUndefined()
  })

  it('opens nothing on the root page when the account would go to another workspace', async () => {
    await snapshot.saveLocalSnapshot({ session: session(), account: account(['acme', 'beta'], viewer.id, 'beta'), data: data() })
    expect(await snapshot.prepareStartupSnapshot('/')).toBeUndefined()
    expect((await snapshot.prepareStartupSnapshot('/acme'))?.workspaceKey).toBe('acme')
  })

  it('without a remembered viewer nothing is read', async () => {
    await snapshot.saveLocalSnapshot({ session: session(), account: account(), data: data() })
    localStorage.removeItem('flow:last-workspace')
    expect(await snapshot.prepareStartupSnapshot('/acme/inbox')).toBeUndefined()
  })

  it('saves at most once per interval while data changes, reading the latest data when it runs', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('requestIdleCallback', undefined)
    let current = data('acme', viewer.id, 'first')
    const reads: string[] = []
    const saver = snapshot.createLocalSnapshotSaver(() => { reads.push(current.issues[0].title); return { session: session(), account: account(), data: current } }, 30_000)
    saver.schedule(viewer.id, 'acme')
    await vi.advanceTimersByTimeAsync(0)
    expect(reads.at(-1)).toBe('first')
    current = data('acme', viewer.id, 'second')
    saver.schedule(viewer.id, 'acme')
    current = data('acme', viewer.id, 'third')
    saver.schedule(viewer.id, 'acme')
    await vi.advanceTimersByTimeAsync(10_000)
    expect(reads.at(-1)).not.toBe('third')
    // Hiding the page writes the pending state at once.
    saver.flush()
    expect(reads.at(-1)).toBe('third')
    const count = reads.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(reads.length).toBe(count)
    saver.cancel()
  })
})

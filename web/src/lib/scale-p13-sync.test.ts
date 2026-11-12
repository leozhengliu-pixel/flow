import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api-client'
import { QueryWindowStore } from '@/lib/query-window-store'
import { RecordLoader } from '@/lib/record-loader'
import { compareSyncVersions, fetchSurfaceBootstrap, migratedSyncSignal, syncBootstrapRequestKey, syncScopeMatches, SyncClient, SyncProtocolUnavailable, SyncResetRequired } from '@/lib/sync-client'
import { createWorkspaceStore } from '@/store/workspace-store'
import { makeBootstrap } from '@/test/fixtures'
import type { RecordKey, SyncChanges, SyncManifest, SyncRecord, SyncRecordBatch } from '@/types/flow'

export function manifest(): SyncManifest {
  const { projects: _projects, issues: _issues, ...core } = makeBootstrap({ documents: [], notifications: [] })
  return { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, core, authzEpoch: 'epoch-1', head: '100', capabilities: ['sync-v2'], collections: { document: { revision: '10' }, issue: { revision: '20' } } }
}
const row = (version: string, completeness: SyncRecord['completeness'] = 'detail', value: Record<string, unknown> = { id: 'doc-1', title: 'Visible document', content: 'Body' }): SyncRecord => ({ key: { kind: 'document', id: 'doc-1' }, version, completeness, ...(completeness === 'deleted' ? {} : { value }) })
const changes = (overrides: Partial<SyncChanges> = {}): SyncChanges => ({ next: '100', head: '100', authzEpoch: 'epoch-1', transactions: [], hasMore: false, resyncRequired: false, ...overrides })
function transport() {
  return { batch: vi.fn(async (_keys: RecordKey[], _signal: AbortSignal): Promise<SyncRecordBatch> => ({ items: [], authzEpoch: 'epoch-1' })), page: vi.fn(async () => ({ items: [row('1', 'summary')], next: '', hasMore: false, revision: '10', authzEpoch: 'epoch-1' })), changes: vi.fn(async (_after: string, _signal: AbortSignal) => changes()) }
}

afterEach(() => { vi.useRealTimers(); vi.unstubAllEnvs(); vi.unstubAllGlobals() })

describe('scale-p13 record state and adapters', () => {
  it('keeps collection completeness separate from short/empty query windows', async () => {
    const client = new SyncClient(manifest(), 'workspace', transport())
    const store = createWorkspaceStore(client.adapt())
    expect(store.collectionComplete('document')).toBe(false)
    expect(store.collectionComplete('issue')).toBe(false)
    expect(store.completeness('document', 'missing')).toBe('omitted')
    const window = await client.readWindow({ kind: 'document', filter: '{"team":"public"}' })
    expect(window.completeness).toBe('complete')
    expect(createWorkspaceStore(client.adapt()).collectionComplete('document')).toBe(false)
    expect(client.get({ kind: 'document', id: 'doc-1' })?.completeness).toBe('summary')
    await expect(store.hydrateModel('User', manifest().viewerId)).resolves.toEqual(manifest().core.viewer)
  })

  it('does not call a collection complete unless its baseline was actually included', () => {
    const m = manifest()
    m.collections.issue = { revision: '20', complete: true }
    const client = new SyncClient(m, 'workspace', transport())
    expect(createWorkspaceStore(client.adapt()).collectionComplete('issue')).toBe(false)
  })

  it('accepts canonical collection keys and model aliases in the same loader and window contracts', async () => {
    vi.useFakeTimers()
    const api = transport()
    api.batch.mockResolvedValue({ items: [row('2')], authzEpoch: 'epoch-1' })
    const client = new SyncClient(manifest(), 'workspace', api)
    await client.readWindow({ kind: 'documents' })
    const detail = client.readDetail({ kind: 'documents', id: 'doc-1' })
    await vi.advanceTimersByTimeAsync(10)
    expect((await detail)?.completeness).toBe('detail')
    expect(client.windows.get({ kind: 'documents' })).toBe(client.windows.get({ kind: 'document' }))
    client.install([], ['documents'])
    expect(client.windows.get({ kind: 'document' })?.completeness).toBe('stale')
  })

  it('preserves omitted detail fields, rejects old versions, and prevents tombstone resurrection', () => {
    const client = new SyncClient(manifest(), 'workspace', transport())
    client.install([row('9007199254740993')])
    client.install([row('9007199254740994', 'summary', { id: 'doc-1', title: 'New title' })])
    expect(client.get(row('1').key)).toMatchObject({ completeness: 'stale', value: { title: 'New title', content: 'Body' } })
    client.install([row('9007199254740993')])
    expect(client.get(row('1').key)?.version).toBe('9007199254740994')
    client.install([row('9007199254740995', 'deleted')])
    client.install([row('9007199254740994'), row('9007199254740995')])
    expect(client.get(row('1').key)?.completeness).toBe('deleted')
    expect(client.adapt().documents).toEqual([])
    expect(compareSyncVersions('00010', '9')).toBe(1)
    expect(() => compareSyncVersions('opaque', '1')).toThrow()
  })

  it('replaces a full detail projection so removed or redacted fields do not survive', () => {
    const client = new SyncClient(manifest(), 'workspace', transport())
    client.install([row('1', 'detail', { id: 'doc-1', content: 'Body', creator: { id: 'old-actor' }, projectIds: ['old-project'] })])
    client.install([row('2', 'detail', { id: 'doc-1', content: 'Body', projectIds: [] })])
    expect(client.adapt().documents).toEqual([{ id: 'doc-1', content: 'Body', projectIds: [] }])
  })

  it('keeps a mounted document model intact while a newer summary waits for its body', () => {
    const client = new SyncClient(manifest(), 'workspace', transport())
    client.install([row('1', 'detail', { id: 'doc-1', title: 'Old title', content: 'Old body', contentVersion: 1 })])
    const first = client.adapt()
    const acknowledged = { ...first.documents[0], title: 'Saved title', content: 'Saved body', contentVersion: 2 }
    const current = { ...first, documents: [acknowledged] }
    client.install([row('2', 'summary', { id: 'doc-1', title: 'Saved title', contentVersion: 2 })])
    const stale = client.adapt(current)
    expect(stale.documents[0]).toBe(acknowledged)
    expect(client.get(row('1').key)?.completeness).toBe('stale')
    client.install([row('2', 'detail', { id: 'doc-1', title: 'Server title', content: 'Server body', contentVersion: 2 })])
    expect(client.adapt(stale).documents[0]).toMatchObject({ title: 'Server title', content: 'Server body' })
  })

  it('keeps collection arrays and indexes when an already applied version is replayed', () => {
    const client = new SyncClient(manifest(), 'workspace', transport())
    client.install([row('1', 'summary')])
    const first = client.adapt()
    const before = createWorkspaceStore(first)
    client.install([row('1', 'summary')])
    const next = client.adapt(first)
    expect(next.documents).toBe(first.documents)
    expect(before.applyBootstrap(next).entities.document).toBe(before.entities.document)
  })

  it('installs whole transactions and advances the scanned cursor across invisible gaps from H0', async () => {
    const api = transport()
    api.changes.mockResolvedValueOnce(changes({ next: '150', head: '200', hasMore: true })).mockResolvedValueOnce(changes({ next: '200', head: '200', transactions: [{ txId: 'tx', seq: '180', upserts: [row('2')], removals: [], invalidations: [] }] }))
    const client = new SyncClient(manifest(), 'workspace', api)
    const publish = vi.fn()
    client.subscribe(publish)
    await client.catchUp()
    expect(api.changes.mock.calls.map(call => call[0])).toEqual(['100', '150'])
    expect(client.head).toBe('200')
    expect(client.adapt().documents[0]).toMatchObject({ title: 'Visible document' })
    expect(publish).toHaveBeenCalledTimes(2)
    expect(() => client.install([row('3'), { ...row('4'), key: { kind: 'unknown', id: 'x' } }])).toThrow(SyncResetRequired)
    expect(client.disposed).toBe(true)
    expect(client.legacyRequired).toBe(true)
  })

  it('invalidates history only for changed issue records', () => {
    const client = new SyncClient(manifest(), 'workspace', transport())
    const issue = (id: string, version: string): SyncRecord => ({ key: { kind: 'issue', id }, version, completeness: 'summary', value: { id } })
    client.install([issue('a', '1'), issue('b', '1')])
    const current = { ...client.adapt(), issueHistoryCursors: { a: {}, b: {} } }
    client.install([issue('a', '2')])
    expect(client.adapt(current).issueHistoryCursors).toEqual({ b: {} })
  })

  it('rolls an unconverted live collection back before applying any of its rows', () => {
    const client = new SyncClient(manifest(), 'workspace', transport())
    client.surface = 'document-detail'
    expect(() => client.applyChanges(changes({ next: '110', head: '110', transactions: [{ txId: 'project', seq: '110', upserts: [{ key: { kind: 'project', id: 'p' }, version: '1', completeness: 'summary', value: { id: 'p' } }], removals: [], invalidations: [] }] }))).toThrow(SyncProtocolUnavailable)
    expect(client.disposed).toBe(true)
    expect(client.legacyRequired).toBe(true)
    expect(migratedSyncSignal('document.comment_created')).toBe(false)
    expect(migratedSyncSignal('comment.created')).toBe(false)
    expect(migratedSyncSignal('issue.updated')).toBe(true)
  })

  it('invalidates only affected windows and marks an open detail stale', async () => {
    const client = new SyncClient(manifest(), 'workspace', transport())
    await client.readWindow({ kind: 'document' })
    client.windows.begin({ kind: 'project' })
    client.install([row('2')])
    client.install([], ['document'])
    expect(client.get(row('1').key)?.completeness).toBe('stale')
    expect(client.windows.get({ kind: 'document' })?.completeness).toBe('stale')
    expect(client.windows.get({ kind: 'project' })?.completeness).toBe('omitted')
  })

  it('applies a projected live delta atomically without refetching summaries', () => {
    const api = transport()
    const client = new SyncClient(manifest(), 'workspace', api)
    const publish = vi.fn(() => expect(client.adapt().documents).toHaveLength(1))
    client.subscribe(publish)
    const applied = client.applyFrame(changes({ next: '110', head: '110', transactions: [{ txId: 'live', seq: '110', upserts: [row('5', 'summary')], removals: [], invalidations: [] }] }))
    expect(applied).toBe(true)
    expect(client.head).toBe('110')
    expect(publish).toHaveBeenCalledTimes(1)
    expect(api.changes).not.toHaveBeenCalled()
  })

  it('consumes the landed P12 transaction envelope and explicit detail invalidation', () => {
    const api = transport()
    const client = new SyncClient(manifest(), 'workspace', api)
    client.install([row('5')])
    const transaction = { txId: 'live', seq: '110', upserts: [{ key: { kind: 'documents', id: 'doc-1' }, version: '5', data: { id: 'doc-1', title: 'Frame title', contentVersion: '2' }, projection: 'summary', detailStale: true }], removals: [], invalidations: [] }
    expect(client.applyFrame({ schema: 2, authzEpoch: 'epoch-1', transaction })).toBe(true)
    expect(client.head).toBe('110')
    expect(client.get(row('1').key)).toMatchObject({ completeness: 'stale', value: { title: 'Frame title', content: 'Body', contentVersion: 2 } })
    expect(client.applyFrame({ schema: 2, authzEpoch: 'old-epoch', transaction: { ...transaction, seq: '109' } })).toBe(true)
    expect(client.disposed).toBe(false)
    expect(api.changes).not.toHaveBeenCalled()
    expect(() => client.applyFrame({ schema: 2, authzEpoch: 'epoch-2', transaction: { ...transaction, seq: '111' } })).toThrow(SyncResetRequired)
    expect(client.disposed).toBe(true)
  })

  it('cancels old pages and batches on an epoch change, even when a server ignores abort', async () => {
    vi.useFakeTimers()
    const api = transport()
    let finish!: (batch: SyncRecordBatch) => void
    api.batch.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    api.changes.mockResolvedValue(changes({ authzEpoch: 'epoch-2' }))
    const client = new SyncClient(manifest(), 'workspace', api)
    const detail = client.readDetail(row('1').key)
    const rejected = expect(detail).rejects.toThrow()
    await vi.advanceTimersByTimeAsync(10)
    await expect(client.catchUp()).rejects.toThrow(SyncResetRequired)
    expect(api.batch.mock.calls[0][1].aborted).toBe(true)
    finish({ items: [row('1')], authzEpoch: 'epoch-1' })
    await rejected
    expect(client.get(row('1').key)).toBeUndefined()
    expect(() => client.adapt()).toThrow()
  })

  it('rejects cross-workspace/viewer manifests and drops old entities after a scope change', () => {
    expect(() => new SyncClient({ ...manifest(), workspaceId: 'other' }, 'workspace', transport())).toThrow()
    expect(() => new SyncClient({ ...manifest(), viewerId: 'other' }, 'workspace', transport())).toThrow()
    const first = new SyncClient(manifest(), 'workspace', transport())
    first.install([row('1')])
    const next = new SyncClient({ ...manifest(), authzEpoch: 'epoch-2' }, 'workspace', transport())
    expect(next.adapt(first.adapt()).documents).toEqual([])
  })

  it('cancels existing issue detail/history requests and their queued updates when the client scope expires', () => {
    const client = new SyncClient(manifest(), 'workspace', transport())
    const initial = client.adapt()
    const signal = client.requestSignal(new AbortController().signal)
    expect(syncScopeMatches(initial, client)).toBe(true)
    client.install([row('1')])
    expect(syncScopeMatches(client.adapt(initial), client)).toBe(true)
    client.dispose()
    expect(signal.aborted).toBe(true)
    expect(syncScopeMatches(initial, client)).toBe(false)
    expect(syncScopeMatches(initial, undefined)).toBe(false)
  })

  it('signals recovery and aborts every detail request when a batch observes a new epoch', async () => {
    vi.useFakeTimers()
    const api = transport()
    api.batch.mockResolvedValue({ items: [row('1')], authzEpoch: 'epoch-2' })
    const client = new SyncClient(manifest(), 'workspace', api)
    const reset = vi.fn()
    client.subscribe(reset)
    const detail = client.readDetail(row('1').key)
    const rejected = expect(detail).rejects.toThrow()
    await vi.advanceTimersByTimeAsync(10)
    await rejected
    expect(client.disposed).toBe(true)
    expect(reset).toHaveBeenCalledTimes(1)
    expect(client.legacyRequired).toBe(false)
  })

  it('restores legacy loading when a detail batch reaches an old replica after a new manifest', async () => {
    vi.useFakeTimers()
    const api = transport()
    api.batch.mockRejectedValue(new ApiError('Not found', 404))
    const client = new SyncClient(manifest(), 'workspace', api)
    client.install([row('1', 'summary')])
    const reset = vi.fn()
    client.subscribe(reset)
    const rejected = expect(client.readDetail(row('1').key)).rejects.toBeInstanceOf(SyncProtocolUnavailable)
    await vi.advanceTimersByTimeAsync(10)
    await rejected
    expect(client.disposed).toBe(true)
    expect(client.legacyRequired).toBe(true)
    expect(reset).toHaveBeenCalledTimes(1)
  })

  it('does not let a superseded page overwrite the current query window', async () => {
    const api = transport()
    let finish!: (page: Awaited<ReturnType<typeof api.page>>) => void
    api.page.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const client = new SyncClient(manifest(), 'workspace', api)
    const first = client.readWindow({ kind: 'document' })
    const rejected = expect(first).rejects.toThrow('superseded')
    await client.readWindow({ kind: 'document' })
    finish({ items: [row('0')], authzEpoch: 'epoch-1', next: 'old', hasMore: true, revision: '9' })
    await rejected
    expect(client.windows.get({ kind: 'document' })?.revision).toBe('10')
    expect(client.get(row('1').key)?.version).toBe('1')
  })

  it.each(['admin', 'admin-private-outsider', 'member', 'guest-grant', 'private-outsider', 'sub-team-inheritance', 'key-unrestricted', 'key-selected-empty', 'key-selected-set', 'old-slug', 'hidden-references-counts-actors'])('preserves the server projection for %s without synthesizing hidden fields', identity => {
    const m = manifest()
    m.viewerId = identity
    m.core.viewer = { ...m.core.viewer, id: identity }
    const client = new SyncClient(m, 'workspace', transport())
    const projected = { id: 'doc-1', title: identity, teamIds: [], projectIds: [], creator: { id: identity }, content: '' }
    client.install([row('1', 'detail', projected)])
    expect(client.adapt().documents).toEqual([projected])
    expect(client.adapt().projects).toEqual([])
  })
})

describe('scale-p13 bounded detail loader', () => {
  it('bounds encoded request bytes as well as key count', async () => {
    vi.useFakeTimers()
    const fetch = vi.fn(async (_keys: RecordKey[]) => ({ items: [], authzEpoch: 'epoch-1' }))
    const loader = new RecordLoader({ authzEpoch: 'epoch-1', fetch, onEpochChange: vi.fn() })
    await expect(loader.load({ kind: 'document', id: 'x'.repeat(256 * 1024) })).rejects.toThrow('budget')
    const calls = Array.from({ length: 10 }, (_, index) => loader.load({ kind: 'document', id: `${index}${'x'.repeat(40 * 1024)}` }))
    await vi.advanceTimersByTimeAsync(10)
    await Promise.all(calls)
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(fetch.mock.calls.every(call => new TextEncoder().encode(JSON.stringify(call[0])).byteLength < 256 * 1024)).toBe(true)
    loader.dispose()
  })
  it('coalesces for 10ms, deduplicates, and limits batches to 50 keys and 4 concurrent calls', async () => {
    vi.useFakeTimers()
    const finishes: Array<() => void> = []
    let active = 0, maximum = 0
    const fetch = vi.fn((keys: RecordKey[]) => new Promise<SyncRecordBatch>(resolve => {
      active++; maximum = Math.max(maximum, active)
      finishes.push(() => { active--; resolve({ authzEpoch: 'epoch-1', items: keys.map(key => ({ ...row('1'), key, value: { id: key.id } })) }) })
    }))
    const loader = new RecordLoader({ authzEpoch: 'epoch-1', fetch, onEpochChange: vi.fn() })
    const calls = Array.from({ length: 251 }, (_, index) => loader.load({ kind: 'document', id: String(index) }))
    calls.push(loader.load({ kind: 'document', id: '0' }))
    await vi.advanceTimersByTimeAsync(9)
    expect(fetch).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(fetch).toHaveBeenCalledTimes(4)
    while (finishes.length) { finishes.shift()!(); await vi.advanceTimersByTimeAsync(0) }
    const results = await Promise.all(calls)
    expect(fetch).toHaveBeenCalledTimes(6)
    expect(maximum).toBe(4)
    expect(fetch.mock.calls.every(([keys]) => keys.length <= 50)).toBe(true)
    expect(fetch.mock.calls.flatMap(([keys]) => keys)).toHaveLength(251)
    expect(results[0]).toEqual(results.at(-1))
    loader.dispose()
  })

  it('isolates cancellation between consumers and rejects unrequested/old-epoch rows', async () => {
    vi.useFakeTimers()
    const fetch = vi.fn(async () => ({ items: [row('1')], authzEpoch: 'epoch-1' }))
    const loader = new RecordLoader({ authzEpoch: 'epoch-1', fetch, onEpochChange: vi.fn() })
    const signal = new AbortController()
    const cancelled = loader.load(row('1').key, signal.signal)
    const rejected = expect(cancelled).rejects.toThrow()
    const retained = loader.load(row('1').key)
    signal.abort()
    await rejected
    await vi.advanceTimersByTimeAsync(10)
    expect(await retained).toEqual(row('1'))
    const missing = loader.load({ kind: 'document', id: 'unrequested' })
    const unexpected = expect(missing).rejects.toThrow('unrequested')
    await vi.advanceTimersByTimeAsync(10)
    await unexpected
    loader.dispose()
  })
})

describe('scale-p13 window revisions and fallback', () => {
  it('keeps manifest detail requests separate from complete legacy snapshots and other surfaces', () => {
    vi.stubEnv('VITE_SYNC_V2', 'true')
    expect(syncBootstrapRequestKey('viewer:workspace:full', 'document-detail')).not.toBe(syncBootstrapRequestKey('viewer:workspace:full'))
    expect(syncBootstrapRequestKey('viewer:workspace:full', 'document-detail')).not.toBe(syncBootstrapRequestKey('viewer:workspace:full', 'issue-detail'))
    vi.stubEnv('VITE_SYNC_V2', 'false')
    expect(syncBootstrapRequestKey('viewer:workspace:full', 'document-detail')).toBe('viewer:workspace:full')
  })
  it('rejects mixed revisions and bounds stored windows', () => {
    const windows = new QueryWindowStore(2)
    const page = { items: [row('1')], next: 'next', hasMore: true, revision: '10', authzEpoch: 'epoch-1' }
    windows.apply({ kind: 'document' }, page)
    expect(() => windows.apply({ kind: 'document', cursor: 'next' }, { ...page, revision: '11' })).toThrow()
    expect(windows.get({ kind: 'document' })?.completeness).toBe('stale')
    windows.begin({ kind: 'project' }); windows.begin({ kind: 'issue' })
    expect(windows.get({ kind: 'document' })).toBeUndefined()
  })

  it('serves exactly the legacy object with the flag off, an unmigrated surface, or no capability', async () => {
    const original = makeBootstrap()
    const legacy = vi.fn(async () => original)
    const fetch = vi.fn(async () => new Response(JSON.stringify({ ...manifest(), capabilities: [] }), { status: 200 }))
    vi.stubGlobal('fetch', fetch)
    vi.stubEnv('VITE_SYNC_V2', 'false')
    expect(await fetchSurfaceBootstrap('workspace', 'document-detail', legacy)).toBe(original)
    expect(fetch).not.toHaveBeenCalled()
    vi.stubEnv('VITE_SYNC_V2', 'true')
    expect(await fetchSurfaceBootstrap('workspace', undefined, legacy)).toBe(original)
    expect(fetch).not.toHaveBeenCalled()
    expect(await fetchSurfaceBootstrap('workspace', 'document-detail', legacy)).toBe(original)
  })

  it.each([404, 405, 501])('falls back against an old server (%i)', async status => {
    vi.stubEnv('VITE_SYNC_V2', 'true')
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status })))
    const original = makeBootstrap()
    expect(await fetchSurfaceBootstrap('workspace', 'issue-detail', async () => original)).toBe(original)
  })

  it.each([404, 405, 501])('falls back if catch-up reaches an old replica after a new manifest (%i)', async status => {
    vi.stubEnv('VITE_SYNC_V2', 'true')
    const fetch = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify(manifest()), { status: 200 })).mockResolvedValueOnce(new Response('{}', { status }))
    vi.stubGlobal('fetch', fetch)
    const original = makeBootstrap()
    expect(await fetchSurfaceBootstrap('workspace', 'document-detail', async () => original)).toBe(original)
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('does not turn a permission refusal into a legacy read', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true')
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 403 })))
    const legacy = vi.fn()
    await expect(fetchSurfaceBootstrap('workspace', 'document-detail', legacy)).rejects.toBeInstanceOf(ApiError)
    expect(legacy).not.toHaveBeenCalled()
  })
})

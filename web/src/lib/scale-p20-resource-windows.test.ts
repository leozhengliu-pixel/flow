import { afterEach, describe, expect, it, vi } from 'vitest'
import { makeBootstrap } from '@/test/fixtures'
import { QueryWindowStore } from '@/lib/query-window-store'
import { SyncClient } from '@/lib/sync-client'
import { ApiError } from '@/lib/api-client'
import { documentBodyLoaded, fetchResourceSurfaceBootstrap, loadResourceDocument, loadResourceSummaries, resourceDocumentForAction, resourceSelection, resourceSyncSurface, resourceTrashedDocument, resourceWindowDocuments, resourceWindowEnabled, resourceWindowStale } from '@/lib/resource-window-adapters'
import type { FlowDocument, SyncManifest, SyncRecord } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchSyncManifest: vi.fn(), fetchSyncChanges: vi.fn(), fetchSyncRecords: vi.fn(), fetchSyncBatch: vi.fn(), fetchBootstrap: vi.fn(), getDocument: vi.fn(), request: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/api-client', async importOriginal => ({ ...await importOriginal<object>(), request: api.request }))
afterEach(() => { vi.unstubAllEnvs(); vi.resetAllMocks() })

const document = { id: 'doc', slugId: 'current-0123456789ab', title: 'Document', content: 'Saved body', contentState: '{"type":"doc"}', creator: makeBootstrap().viewer, revisions: [], projectIds: [], teamIds: [], subscriberIds: [], favorite: false, createdAt: '2026-11-12', updatedAt: '2026-11-12' } satisfies FlowDocument
function fixture(core = makeBootstrap()) {
  const manifest: SyncManifest = { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, core, authzEpoch: 'epoch', head: '1', capabilities: ['sync-v2'], collections: { document: { revision: '1', complete: false } } }
  const page = vi.fn(), batch = vi.fn(), changes = vi.fn()
  const client = new SyncClient(manifest, core.workspace.urlKey, { page, batch, changes })
  return { client, manifest, page, batch, changes }
}
const row = (id: string, completeness: SyncRecord['completeness'] = 'summary'): SyncRecord => ({ key: { kind: 'document', id }, version: '1', completeness, value: { ...document, id, ...(completeness === 'summary' ? { content: undefined } : {}) } })

describe('scale-p20 resource windows', () => {
  it('restarts initialization with fresh authority after a directory epoch changes', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
    let manifests = 0
    let staleSignal: AbortSignal | undefined
    api.request.mockImplementation(async (path: string, options: { signal?: AbortSignal }) => {
      if (path.startsWith('/api/sync/manifest')) return { ...fixture().manifest, authzEpoch: `epoch-${++manifests}`, capabilities: ['resource-windows'] }
      if (path.startsWith('/api/resource-windows/') && manifests === 1) {
        staleSignal = options.signal
        throw new ApiError('Sync access changed; reload the manifest', 403)
      }
      if (path.startsWith('/api/sync/changes')) return { next: '1', head: '1', authzEpoch: `epoch-${manifests}`, transactions: [], hasMore: false, resyncRequired: false }
      return { items: [], next: '', hasMore: false, revision: '1', authzEpoch: `epoch-${manifests}` }
    })
    const legacy = vi.fn()
    const data = await fetchResourceSurfaceBootstrap('workspace', 'document-detail', legacy)
    expect(manifests).toBe(2)
    expect(staleSignal?.aborted).toBe(true)
    expect(data.sync?.authzEpoch).toBe('epoch-2')
    expect(legacy).not.toHaveBeenCalled()
  })

  it.each([401, 403])('never falls back to legacy data after persistent authority denial %s', async status => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
    api.request.mockRejectedValue(new ApiError('Sync access changed; reload the manifest', status))
    const legacy = vi.fn()
    await expect(fetchResourceSurfaceBootstrap('workspace', 'document-detail', legacy)).rejects.toMatchObject({ status })
    expect(api.request).toHaveBeenCalledTimes(status === 401 ? 1 : 3)
    expect(legacy).not.toHaveBeenCalled()
  })

  it('keeps every new surface off unless both its own flag and sync are enabled', () => {
    for (const surface of ['documents', 'trash', 'people', 'pickers'] as const) expect(resourceWindowEnabled(surface)).toBe(false)
    vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
    expect(resourceWindowEnabled('documents')).toBe(false)
    vi.stubEnv('VITE_SYNC_V2', 'true')
    expect(resourceWindowEnabled('documents')).toBe(true)
    expect(resourceWindowEnabled('people')).toBe(false)
    expect(resourceSyncSurface({ kind: 'team-documents', workspaceSlug: 'workspace', teamKey: 'ENG' })).toBe('document-directory')
    expect(resourceSyncSurface({ kind: 'workspace-members', workspaceSlug: 'workspace' })).toBeUndefined()
  })

  it('distinguishes explicit selection, loaded keys, and a complete query selection', () => {
    const windows = new QueryWindowStore()
    const query = { kind: 'document' }
    windows.apply(query, { items: [row('one')], hasMore: true, next: 'two', revision: '1', authzEpoch: 'epoch' })
    expect(resourceSelection(windows.get(query), ['one', 'unloaded'])).toEqual({ loadedIds: ['one'], selectedIds: ['one', 'unloaded'], completeIds: undefined, allSelected: false })
    windows.apply({ ...query, cursor: 'two' }, { items: [row('two')], hasMore: false, next: '', revision: '1', authzEpoch: 'epoch' })
    expect(resourceSelection(windows.get(query), ['one', 'two']).allSelected).toBe(true)
    windows.invalidate('document')
    expect(resourceSelection(windows.get(query)).completeIds).toBeUndefined()
  })

  it('loads bounded summary pages before supplying complete local counts and filters', async () => {
    const { client, page } = fixture()
    page.mockResolvedValueOnce({ items: [row('one')], hasMore: true, next: 'cursor', revision: '1', authzEpoch: 'epoch' })
      .mockResolvedValueOnce({ items: [row('two')], hasMore: false, next: '', revision: '1', authzEpoch: 'epoch' })
    const data = await loadResourceSummaries(client.adapt(), { kind: 'document' })
    expect(data.documents.map(item => item.id)).toEqual(['one', 'two'])
    expect(page.mock.calls.map(([query]) => query)).toEqual([{ kind: 'document', limit: 100, maxBytes: 262144, cursor: undefined }, { kind: 'document', limit: 100, maxBytes: 262144, cursor: 'cursor' }])
    expect(resourceSelection(client.windows.get({ kind: 'document' })).completeIds).toEqual(['one', 'two'])
    client.dispose()
  })

  it('does not silently drop an omitted row from a complete directory', async () => {
    const { client, page } = fixture()
    page.mockResolvedValue({ items: [{ key: { kind: 'document', id: 'large' }, version: '1', completeness: 'omitted' }], hasMore: false, next: '', revision: '1', authzEpoch: 'epoch' })
    await expect(loadResourceSummaries(client.adapt(), { kind: 'document' })).rejects.toThrow('legacy adapter')
    client.dispose()
  })

  it('stops paging when the caller leaves the surface', async () => {
    const { client, page } = fixture()
    const controller = new AbortController()
    page.mockImplementation(async () => {
      controller.abort()
      return { items: [row('one')], hasMore: true, next: 'more', revision: '1', authzEpoch: 'epoch' }
    })
    await expect(loadResourceSummaries(client.adapt(), { kind: 'document' }, controller.signal)).rejects.toThrow()
    expect(page).toHaveBeenCalledTimes(1)
    client.dispose()
  })

  it('keeps directory summaries when the detail LRU evicts bodies', () => {
    const { client } = fixture()
    client.surface = 'document-directory'
    const summaries = Array.from({ length: 150 }, (_, index) => row(`cache-${index}`))
    client.install(summaries)
    const body = 'x'.repeat(128 * 1024)
    client.install(summaries.map(item => ({ ...item, version: '2', completeness: 'detail', value: { ...document, id: item.key.id, content: body } })))
    const data = client.adapt()
    expect(data.documents).toHaveLength(150)
    expect(data.documents.filter(item => Object.hasOwn(item, 'content')).length).toBeLessThan(150)
    expect(summaries.some(item => client.get(item.key)?.completeness === 'omitted')).toBe(false)
    client.dispose()
  })

  it('distinguishes directory membership from entities loaded for another reference', async () => {
    const { client, page } = fixture()
    client.surface = 'document-directory'
    page.mockResolvedValue({ items: [row('in-window')], hasMore: false, next: '', revision: '1', authzEpoch: 'epoch' })
    const initial = await loadResourceSummaries(client.adapt(), { kind: 'document' })
    client.install([row('reference', 'detail')])
    const data = client.adapt(initial)
    expect(data.documents).toHaveLength(2)
    expect(resourceWindowDocuments(data, data.documents).map(item => item.id)).toEqual(['in-window'])
    expect(client.get({ kind: 'document', id: 'reference' })?.completeness).toBe('detail')
    client.dispose()
  })

  it('refuses to treat a missing or summary body as an empty editable body', () => {
    const { client } = fixture()
    client.install([row(document.id)])
    expect(documentBodyLoaded(client.adapt(), { ...document, content: '' })).toBe(false)
    client.install([{ ...row(document.id, 'detail'), version: '2' }])
    expect(documentBodyLoaded(client.adapt(), document)).toBe(true)
    client.install([{ ...row(document.id), version: '3' }])
    expect(documentBodyLoaded(client.adapt(), document)).toBe(true)
    expect(client.getDetail({ kind: 'document', id: document.id })?.content).toBe(document.content)
    client.dispose()
  })

  it('hydrates a summary for copy, duplicate and template actions through the shared loader', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
    const { client, batch } = fixture()
    client.install([row(document.id)])
    batch.mockResolvedValue({ items: [row(document.id, 'detail')], authzEpoch: 'epoch' })
    const data = client.adapt()
    const results = await Promise.all([resourceDocumentForAction(data, data.documents[0]), resourceDocumentForAction(data, data.documents[0])])
    expect(results.map(item => item.content)).toEqual(['Saved body', 'Saved body'])
    expect(batch).toHaveBeenCalledTimes(1)
    expect(batch.mock.calls[0][0]).toEqual([{ kind: 'document', id: document.id }])
    client.dispose()
  })

  it('uses the complete detail endpoint when a legal body is omitted from a bounded batch', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
    const { client, batch } = fixture()
    client.install([row(document.id)])
    batch.mockResolvedValue({ items: [{ key: { kind: 'document', id: document.id }, version: '1', completeness: 'omitted' }], authzEpoch: 'epoch' })
    const body = 'x'.repeat(300000)
    api.getDocument.mockResolvedValue({ ...document, content: body })
    const data = client.adapt()
    expect((await resourceDocumentForAction(data, data.documents[0])).content).toBe(body)
    expect(api.getDocument.mock.calls[0][2]).toBe('workspace')
    expect(client.disposed).toBe(false)
    expect(client.get({ kind: 'document', id: document.id })?.completeness).toBe('summary')
    expect(client.adapt(data).documents).toHaveLength(1)
    client.dispose()
  })

  it('falls back against old servers without inventing an empty directory', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true')
    const legacy = vi.fn().mockResolvedValue(makeBootstrap())
    const { manifest } = fixture()
    api.fetchSyncManifest.mockResolvedValue({ ...manifest, capabilities: [] })
    expect(await fetchResourceSurfaceBootstrap('workspace', 'document-directory', legacy)).toBe(await legacy.mock.results[0].value)
    expect(api.fetchSyncRecords).not.toHaveBeenCalled()
  })

  it('requires complete relationship sets before using the new directory', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true')
    const { manifest } = fixture(makeBootstrap({ projects: undefined }))
    api.fetchSyncManifest.mockResolvedValue(manifest)
    api.fetchSyncChanges.mockResolvedValue({ next: '1', head: '1', authzEpoch: 'epoch', transactions: [], hasMore: false, resyncRequired: false })
    const legacy = vi.fn().mockResolvedValue(makeBootstrap())
    await fetchResourceSurfaceBootstrap('workspace', 'document-directory', legacy)
    expect(legacy).toHaveBeenCalledTimes(1)
    expect(api.fetchSyncRecords).not.toHaveBeenCalled()
  })

  it('reads only the targeted trash entry for a deleted document without clearing its body', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_TRASH_WINDOWS', 'true')
    const { client } = fixture()
    const deleted = { ...document, deletedAt: '2026-11-12' }
    const entry = { id: 'trash', resourceType: 'document', resourceId: document.id, title: document.title, payload: deleted, deletedBy: document.creator, deletedAt: '2026-11-12', expiresAt: '2026-12-12', links: { favorites: ['keep'] } }
    api.getDocument.mockResolvedValue(deleted)
    api.request.mockResolvedValueOnce(deleted).mockResolvedValueOnce(entry)
    const result = await loadResourceDocument(client.adapt(), 'old-0123456789ab', new AbortController().signal)
    expect(result.document.content).toBe('Saved body')
    expect(result.entry).toEqual(entry)
    expect(api.fetchBootstrap).not.toHaveBeenCalled()
    expect(api.request.mock.calls[1][0]).toBe('/api/resource-windows/trash?resourceId=doc')
    expect(client.disposed).toBe(false)
    client.dispose()
  })

  it('recognizes a trash payload body even when the stored snapshot predates its deletedAt marker', () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
    const { client } = fixture()
    const entry = { id: 'trash', resourceType: 'document', resourceId: document.id, title: document.title, payload: document, deletedBy: document.creator, deletedAt: '2026-11-12', expiresAt: '2026-12-12' }
    const data = { ...client.adapt(), trash: [entry] }
    const trashed = resourceTrashedDocument(data.trash, document.id)!
    expect(trashed.document.deletedAt).toBe(entry.deletedAt)
    expect(documentBodyLoaded(data, trashed.document)).toBe(true)
    expect(documentBodyLoaded({ ...data, trash: [{ ...entry, payload: { id: document.id, title: document.title } }] }, trashed.document)).toBe(false)
    client.dispose()
  })

  it('hydrates a partial trash payload rather than leaving a deleted direct link loading', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_TRASH_WINDOWS', 'true')
    const { client } = fixture()
    const entry = { id: 'trash', resourceType: 'document', resourceId: document.id, title: document.title, payload: { id: document.id, slugId: document.slugId }, deletedBy: document.creator, deletedAt: '2026-11-12', expiresAt: '2026-12-12' }
    api.getDocument.mockResolvedValue({ ...document, deletedAt: entry.deletedAt })
    api.request.mockResolvedValueOnce({ ...document, deletedAt: entry.deletedAt }).mockResolvedValueOnce({ ...entry, payload: document })
    const result = await loadResourceDocument({ ...client.adapt(), trash: [entry] }, document.slugId, new AbortController().signal)
    expect(result.document.content).toBe(document.content)
    expect(api.fetchBootstrap).not.toHaveBeenCalled()
    client.dispose()
  })

  it('rejects a queued page after revocation, without publishing old rows', async () => {
    const { client, page } = fixture()
    let finish!: (value: unknown) => void
    page.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const loading = loadResourceSummaries(client.adapt(), { kind: 'document' })
    client.requestReset()
    finish({ items: [row('secret')], next: '', hasMore: false, revision: '1', authzEpoch: 'epoch' })
    await expect(loading).rejects.toThrow()
    expect(client.get({ kind: 'document', id: 'secret' })).toBeUndefined()
  })

  it('invalidates complete directory selections when a live row changes membership', async () => {
    const { client, page } = fixture()
    client.surface = 'document-directory'
    page.mockResolvedValue({ items: [row('one')], hasMore: false, next: '', revision: '1', authzEpoch: 'epoch' })
    await loadResourceSummaries(client.adapt(), { kind: 'document' })
    expect(resourceWindowStale(client)).toBe(false)
    client.applyChanges({ next: '2', head: '2', authzEpoch: 'epoch', transactions: [{ txId: '2', seq: '2', upserts: [{ ...row('two'), version: '2' }], removals: [], invalidations: [] }], hasMore: false, resyncRequired: false })
    expect(resourceWindowStale(client)).toBe(true)
    expect(resourceSelection(client.windows.get({ kind: 'document' })).completeIds).toBeUndefined()
    client.dispose()
  })
})

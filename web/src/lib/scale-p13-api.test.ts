import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchSyncBatch, fetchSyncChanges, fetchSyncManifest, fetchSyncRecords } from '@/lib/api'
import { modelRecordBatch, SyncWireVersionError } from '@/lib/scale-p13-wire'

afterEach(() => vi.unstubAllGlobals())
describe('scale-p13 HTTP contracts', () => {
  it('adapts safe domain-version strings without rounding ordered record versions', () => {
    const batch = { authzEpoch: 'epoch', items: [{ key: { kind: 'issues', id: 'i' }, version: '9007199254740993', data: { id: 'i', version: '12', contentVersion: '4' }, projection: 'summary' as const }] }
    expect(modelRecordBatch(batch, 'detail').items[0]).toMatchObject({ version: '9007199254740993', completeness: 'summary', value: { version: 12, contentVersion: 4 } })
    expect(() => modelRecordBatch({ ...batch, items: [{ ...batch.items[0], data: { id: 'i', version: '9007199254740993' } }] }, 'detail')).toThrow(SyncWireVersionError)
  })
  it('pins every request to its original workspace and forwards cancellation with string cursors', async () => {
    window.history.replaceState({}, '', '/other-workspace/inbox')
    const fetch = vi.fn(async () => new Response(JSON.stringify({ items: [], transactions: [] }), { status: 200 }))
    vi.stubGlobal('fetch', fetch)
    const signal = new AbortController().signal
    await fetchSyncManifest('original', signal)
    await fetchSyncRecords('original', { kind: 'document', cursor: 'opaque/cursor', filter: '{"team":"a"}', order: 'title', limit: 500, maxBytes: 999999 }, signal)
    await fetchSyncBatch('original', [{ kind: 'document', id: 'doc' }], 'detail', signal)
    await fetchSyncChanges('original', '9007199254740993', signal)
    for (const [, init] of fetch.mock.calls as unknown as [string, RequestInit][]) {
      expect(new Headers(init.headers).get('X-Workspace-Key')).toBe('original')
      expect(init.signal).toBe(signal)
    }
    const calls = fetch.mock.calls as unknown as [string, RequestInit][]
    expect(calls[0][0]).toBe('/api/sync/manifest')
    const query = new URL(calls[1][0], 'http://localhost').searchParams
    expect(query.get('cursor')).toBe('opaque/cursor')
    expect(query.get('limit')).toBe('100')
    expect(query.get('maxBytes')).toBe('262144')
    expect(query.get('kind')).toBe('documents')
    expect(JSON.parse(String(calls[2][1].body))).toEqual({ keys: [{ kind: 'documents', id: 'doc' }], projection: 'detail' })
    expect(new URL(calls[3][0], 'http://localhost').searchParams.get('after')).toBe('9007199254740993')
    await expect(fetchSyncBatch('original', Array.from({ length: 51 }, () => ({ kind: 'document', id: 'doc' })), 'detail')).rejects.toThrow()
    expect(fetch).toHaveBeenCalledTimes(4)
  })

  it('adapts canonical data projections, omissions, missing keys and Go key casing without treating missing as deleted', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ authzEpoch: 'epoch', items: [
      { key: { Kind: 'documents', ID: 'doc' }, version: '9007199254740993', data: { id: 'doc', content: 'Body' } },
      { key: { Kind: 'documents', ID: 'large' }, version: '2', omitted: true, detail: { Kind: 'documents', ID: 'large' } },
      { key: { Kind: 'documents', ID: 'missing' }, missing: true },
    ] }), { status: 200 })))
    const batch = await fetchSyncBatch('workspace', [{ kind: 'document', id: 'doc' }], 'detail')
    expect(batch.items).toEqual([
      { key: { kind: 'document', id: 'doc' }, version: '9007199254740993', value: { id: 'doc', content: 'Body' }, completeness: 'detail' },
      { key: { kind: 'document', id: 'large' }, version: '2', completeness: 'omitted' },
    ])
  })
})

import { expect, it, vi } from 'vitest'
import { SyncClient } from '@/lib/sync-client'
import { loadResourceSummaries } from '@/lib/resource-window-adapters'
import { makeBootstrap } from '@/test/fixtures'
import type { FlowDocument, SyncManifest, SyncRecord } from '@/types/flow'

it('scale-p20 measures retained document DTO bytes with a fixed 1000-document fixture', async () => {
  const documents = Array.from({ length: 1000 }, (_, index) => ({
    id: `doc-${index}`, slugId: `document-${index}`, title: `Document ${index}`,
    content: 'x'.repeat(8192), contentState: 'x'.repeat(8192),
    revisions: Array.from({ length: 3 }, (_, revision) => ({ id: `rev-${revision}`, content: 'x'.repeat(8192) })),
    creator: { id: 'viewer' }, teamIds: ['team'], projectIds: [], subscriberIds: [], createdAt: '2026-11-12', updatedAt: '2026-11-12',
  }))
  const before = new TextEncoder().encode(JSON.stringify(documents)).byteLength
  expect(before).toBe(41282671)
  const summaries: SyncRecord[] = documents.map(({ content: _body, contentState: _state, revisions: _history, ...summary }) => ({ key: { kind: 'document', id: summary.id }, version: '1', completeness: 'summary', value: summary }))
  const core = makeBootstrap({ documents: [] })
  const manifest: SyncManifest = { schema: 2, core, workspaceId: core.workspace.id, viewerId: core.viewer.id, authzEpoch: 'epoch', head: '1', capabilities: ['sync-v2'], collections: { document: { revision: '1', complete: false } } }
  const page = vi.fn(async (query: { cursor?: string }) => {
    const start = Number(query.cursor ?? 0), end = start + 100
    return { items: summaries.slice(start, end), next: end < summaries.length ? String(end) : '', hasMore: end < summaries.length, revision: '1', authzEpoch: 'epoch' }
  })
  const batch = vi.fn(async () => ({ items: [{ key: { kind: 'document', id: documents[0].id }, version: '1', completeness: 'detail' as const, value: documents[0] }], authzEpoch: 'epoch' }))
  const client = new SyncClient(manifest, 'workspace', { page, batch, changes: vi.fn() })
  const data = await loadResourceSummaries(client.adapt(), { kind: 'document' })
  await client.readDetail({ kind: 'document', id: documents[0].id })
  const loaded = client.adapt(data)
  const after = new TextEncoder().encode(JSON.stringify(loaded.documents)).byteLength
  expect(after).toBe(234760)
  expect(loaded.documents).toHaveLength(1000)
  expect(loaded.documents.filter(item => Object.hasOwn(item, 'content'))).toHaveLength(1)
  expect((loaded.documents[0] as FlowDocument).content).toHaveLength(8192)
  console.info(JSON.stringify({ measurement: 'scale-p20-document-dto', beforeBytes: before, afterBytes: after, summaryPages: page.mock.calls.length, detailBatches: batch.mock.calls.length }))
  client.dispose()
})

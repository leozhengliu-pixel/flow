import { act, renderHook, waitFor } from '@testing-library/react'
import { useEffect, useState } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { useRouteDocument } from '@/hooks/use-route-document'
import { documentBodyLoaded } from '@/lib/resource-window-adapters'
import { SyncClient } from '@/lib/sync-client'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, FlowDocument, SyncManifest } from '@/types/flow'

const api = vi.hoisted(() => ({ request: vi.fn(), getDocument: vi.fn() }))
vi.mock('@/lib/api-client', async original => ({ ...await original<object>(), request: api.request }))
vi.mock('@/lib/api', async original => ({ ...await original<object>(), getDocument: api.getDocument }))
afterEach(() => { vi.unstubAllEnvs(); vi.resetAllMocks() })

it('scale-p20 keeps a targeted thread read alive when the document body finishes first', async () => {
  vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
  const base = makeBootstrap({ documents: [] })
  const doc = { id: 'doc', slugId: 'doc-slug', title: 'Document', content: 'Loaded body', creator: base.viewer, revisions: [] } as unknown as FlowDocument
  const manifest: SyncManifest = { schema: 2, workspaceId: base.workspace.id, viewerId: base.viewer.id, authzEpoch: 'epoch', head: '1', capabilities: ['sync-v2'], core: base, collections: { document: { revision: '1', complete: false } } }
  const client = new SyncClient(manifest, base.workspace.urlKey, { page: vi.fn(), changes: vi.fn(), batch: vi.fn().mockResolvedValue({ items: [{ key: { kind: 'document', id: doc.id }, version: '2', completeness: 'detail', value: doc }], authzEpoch: 'epoch' }) })
  client.install([{ key: { kind: 'document', id: doc.id }, version: '1', completeness: 'summary', value: { id: doc.id, slugId: doc.slugId } }])
  let finish!: (value: unknown) => void
  api.request.mockImplementation(() => new Promise(resolve => { finish = resolve }))
  const view = renderHook(() => {
    const [data, setData] = useState<BootstrapData | null>(client.adapt())
    useEffect(() => client.subscribe(() => { if (!client.disposed) setData(current => client.adapt(current)) }), [])
    useRouteDocument({ data, setData, slugId: doc.slugId, found: Boolean(data?.documents[0] && documentBodyLoaded(data, data.documents[0])) })
    return data
  })
  await waitFor(() => expect(view.result.current?.documents[0].content).toBe('Loaded body'))
  expect(api.request).toHaveBeenCalledTimes(1)
  expect(api.request.mock.calls[0][0]).toBe('/api/documents/doc/comments')
  expect((api.request.mock.calls[0][1].signal as AbortSignal).aborted).toBe(false)
  await act(async () => { finish([{ id: 'comment', body: 'Saved thread' }]) })
  await waitFor(() => expect(view.result.current?.comments.doc[0].body).toBe('Saved thread'))
  view.unmount(); client.dispose()
})

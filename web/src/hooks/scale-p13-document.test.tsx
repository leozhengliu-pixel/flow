import { render, renderHook, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SyncClient } from '@/lib/sync-client'
import { ApiError } from '@/lib/api-client'
import { useRouteDocument } from '@/hooks/use-route-document'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, FlowDocument, RecordKey, SyncManifest, SyncRecordBatch } from '@/types/flow'

const api = vi.hoisted(() => ({ getDocument: vi.fn() }))
vi.mock('@/lib/api', () => api)

const document = { id: 'doc-new', slugId: 'agent-document-0123456789ab', title: 'Agent document', content: 'Saved body', creator: makeBootstrap().viewer, projectIds: [], teamIds: [], subscriberIds: [], favorite: false, createdAt: '2026-11-11', updatedAt: '2026-11-11', revisions: [] } satisfies FlowDocument
function fixture() {
  const core = makeBootstrap({ documents: [] })
  const manifest: SyncManifest = { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, core, authzEpoch: '1', head: '100', capabilities: ['sync-v2'], collections: {} }
  const batch = vi.fn(async (_keys: RecordKey[], _signal: AbortSignal): Promise<SyncRecordBatch> => ({ items: [{ key: { kind: 'document', id: document.id }, version: '10', completeness: 'detail', value: { ...document } }], authzEpoch: '1' }))
  const client = new SyncClient(manifest, 'workspace', { batch, page: vi.fn(), changes: vi.fn() })
  return { client, batch }
}

function Route({ initial, client, slug = document.slugId }: { initial: BootstrapData; client?: SyncClient; slug?: string }) {
  const [data, setData] = useState<BootstrapData | null>(initial)
  const candidate = data?.documents.find(item => item.slugId === slug)
  const found = Boolean(candidate && (!client || client.get({ kind: 'document', id: candidate.id })?.completeness === 'detail'))
  const state = useRouteDocument({ data, slugId: slug, found, setData })
  return <p>{found ? `open:${candidate!.content}` : state}</p>
}

afterEach(() => api.getDocument.mockReset())

describe('scale-p13 document detail routes', () => {
  it('accepts an omitted legacy document collection while its complete baseline is pending', () => {
    const initial = makeBootstrap({ resourceDetailsOmitted: true, documents: undefined })
    const view = renderHook(() => useRouteDocument({ data: initial, slugId: document.slugId, found: false, setData: vi.fn() }))
    expect(view.result.current).toBeUndefined()
    expect(api.getDocument).not.toHaveBeenCalled()
  })

  it('loads a summary body even when resourceDetailsOmitted is set', async () => {
    const { client, batch } = fixture()
    client.install([{ key: { kind: 'document', id: document.id }, version: '9', completeness: 'summary', value: { id: document.id, slugId: document.slugId, title: document.title } }])
    render(<Route initial={{ ...client.adapt(), resourceDetailsOmitted: true }} client={client} />)
    expect(await screen.findByText('open:Saved body')).toBeVisible()
    expect(batch).toHaveBeenCalledTimes(1)
    expect(api.getDocument).not.toHaveBeenCalled()
  })

  it('resolves an Agent-created or earlier-slug document missing from the manifest', async () => {
    const { client } = fixture()
    api.getDocument.mockResolvedValue(document)
    render(<Route initial={client.adapt()} client={client} slug="earlier-slug-0123456789ab" />)
    // The document's canonical slug changes after resolution; the stored record is authoritative.
    await waitFor(() => expect(client.get({ kind: 'document', id: document.id })?.completeness).toBe('detail'))
    expect(api.getDocument.mock.calls[0][0]).toBe('earlier-slug-0123456789ab')
    expect(api.getDocument.mock.calls[0][2]).toBe('workspace')
  })

  it('keeps the legacy Agent-link fetch working with sync disabled', async () => {
    api.getDocument.mockResolvedValue(document)
    render(<Route initial={makeBootstrap({ documents: [] })} />)
    expect(await screen.findByText('open:Saved body')).toBeVisible()
  })

  it('does not publish a resolver result after the authorization context expires', async () => {
    const { client, batch } = fixture()
    let finish!: (value: FlowDocument) => void
    api.getDocument.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    render(<Route initial={client.adapt()} client={client} />)
    await waitFor(() => expect(api.getDocument).toHaveBeenCalled())
    const signal = api.getDocument.mock.calls[0][1] as AbortSignal
    client.dispose()
    expect(signal.aborted).toBe(true)
    finish(document)
    await waitFor(() => expect(batch).not.toHaveBeenCalled())
    expect(screen.queryByText('open:Saved body')).not.toBeInTheDocument()
  })

  it('ignores a state updater queued before an epoch reset but executed afterwards', async () => {
    const { client } = fixture()
    api.getDocument.mockResolvedValue(document)
    const initial = client.adapt()
    const setData = vi.fn()
    renderHook(() => useRouteDocument({ data: initial, slugId: document.slugId, found: false, setData }))
    await waitFor(() => expect(setData).toHaveBeenCalled())
    const apply = setData.mock.calls.at(-1)![0] as (data: BootstrapData) => BootstrapData
    client.dispose()
    expect(apply(initial)).toBe(initial)
  })

  it('clears a known document when its earlier-slug resolver loses access', async () => {
    const { client } = fixture()
    client.install([{ key: { kind: 'document', id: document.id }, version: '1', completeness: 'detail', value: { ...document } }])
    api.getDocument.mockRejectedValue(new ApiError('Forbidden', 403))
    render(<Route initial={client.adapt()} client={client} slug="earlier-title-0123456789ab" />)
    await waitFor(() => expect(client.disposed).toBe(true))
    expect(client.get({ kind: 'document', id: document.id })).toBeUndefined()
    expect(client.legacyRequired).toBe(false)
  })

  it('shows an unknown forbidden document as missing without a reset loop', async () => {
    const { client } = fixture()
    api.getDocument.mockRejectedValue(new ApiError('Forbidden', 403))
    render(<Route initial={client.adapt()} client={client} />)
    expect(await screen.findByText('missing')).toBeVisible()
    expect(client.disposed).toBe(false)
  })
})

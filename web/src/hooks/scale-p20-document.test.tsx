import { act, render, renderHook, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useState, type ReactNode } from 'react'
import { useRouteDocument } from '@/hooks/use-route-document'
import { useLoadedRevisions } from '@/components/documents/use-loaded-revisions'
import { DocumentHistoryDialog } from '@/components/documents/document-history-dialog'
import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeBootstrap, teammate } from '@/test/fixtures'
import { SyncClient } from '@/lib/sync-client'
import type { BootstrapData, DocumentRevision, FlowDocument, SyncManifest } from '@/types/flow'

const api = vi.hoisted(() => ({ getDocument: vi.fn(), fetchBootstrap: vi.fn(), fetchDocumentRevisions: vi.fn(), request: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/api-client', async importOriginal => ({ ...await importOriginal<object>(), request: api.request }))
afterEach(() => { vi.unstubAllEnvs(); vi.resetAllMocks() })
const base = makeBootstrap()
const document = { id: 'doc', slugId: 'doc-0123456789ab', title: 'Title', content: 'Body', creator: base.viewer, revisions: [], projectIds: [], teamIds: [], subscriberIds: [], favorite: false, createdAt: '2026-11-12', updatedAt: '2026-11-12' } satisfies FlowDocument

describe('scale-p20 document and revision scope', () => {
  it('ends missing detail loading when the batch does not return a record', async () => {
    const manifest: SyncManifest = { schema: 2, workspaceId: base.workspace.id, viewerId: base.viewer.id, core: base, head: '1', authzEpoch: '1', capabilities: ['sync-v2'], collections: {} }
    const client = new SyncClient(manifest, 'workspace', { batch: vi.fn().mockResolvedValue({ items: [], authzEpoch: '1' }), page: vi.fn(), changes: vi.fn() })
    api.getDocument.mockResolvedValue(document)
    const view = renderHook(() => {
      const [data, setData] = useState<BootstrapData | null>(client.adapt())
      return useRouteDocument({ data, slugId: document.slugId, found: false, setData })
    })
    await waitFor(() => expect(view.result.current).toBe('missing'))
    client.dispose()
  })

  it('does not reuse history bodies after a workspace or viewer change, or accept an aborted history response', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
    let data = makeBootstrap()
    const revision = { id: 'rev', documentId: document.id, title: 'Old', content: '', createdAt: '2026-11-11', author: base.viewer, _offloaded: { content: { key: 'revision-body', size: 100 } } } satisfies DocumentRevision
    const source = { ...document, revisions: [revision] }
    let finish!: (value: unknown) => void
    api.request.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = ({ children }: { children: ReactNode }) => <WorkspaceStoreProvider data={data} session={null} account={null}>{children}</WorkspaceStoreProvider>
    const view = renderHook(() => useLoadedRevisions(source, true), { wrapper })
    await waitFor(() => expect(api.request).toHaveBeenCalledTimes(1))
    const oldSignal = api.request.mock.calls[0][1].signal as AbortSignal
    api.request.mockResolvedValue({ nodes: [{ ...revision, _offloaded: undefined, content: 'Current viewer history' }] })
    data = makeBootstrap({ viewer: { ...data.viewer, id: 'next-viewer' } })
    view.rerender()
    expect(oldSignal.aborted).toBe(true)
    await waitFor(() => expect(view.result.current.revisions[0].content).toBe('Current viewer history'))
    await act(async () => { finish({ nodes: [{ ...revision, _offloaded: undefined, content: 'Revoked history' }] }) })
    expect(view.result.current.revisions[0].content).toBe('Current viewer history')
    view.unmount()
  })

  it('loads omitted revision lists only when history is enabled', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
    api.request.mockResolvedValue({ nodes: [{ id: 'history', content: 'Earlier body' }] })
    const { revisions: _revisions, ...summary } = document
    const wrapper = ({ children }: { children: ReactNode }) => <WorkspaceStoreProvider data={base} session={null} account={null}>{children}</WorkspaceStoreProvider>
    const view = renderHook(({ enabled }) => useLoadedRevisions(summary as unknown as FlowDocument, enabled), { wrapper, initialProps: { enabled: false } })
    expect(api.request).not.toHaveBeenCalled()
    view.rerender({ enabled: true })
    await waitFor(() => expect(view.result.current.revisions[0].content).toBe('Earlier body'))
    expect(api.request.mock.calls[0][1].headers).toEqual({ 'X-Workspace-Key': 'workspace' })
  })

  it('shows fetched history when the document initially omitted its revision list', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
    api.request.mockResolvedValue({ nodes: [{ id: 'old', documentId: document.id, title: 'Earlier', content: 'Earlier body', author: base.viewer, createdAt: '2026-11-11T00:00:00Z' }] })
    const { revisions: _revisions, ...summary } = document
    render(<I18nProvider><WorkspaceStoreProvider data={base} session={null} account={null}><DocumentHistoryDialog document={summary as unknown as FlowDocument} open onOpenChange={vi.fn()} onRestore={vi.fn()}/></WorkspaceStoreProvider></I18nProvider>)
    await waitFor(() => expect(screen.getAllByRole('option')).toHaveLength(2))
  })

  it('keeps current actor redaction while reusing immutable revision bodies', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
    const revision = { id: 'old', documentId: document.id, title: 'Earlier', content: '', author: teammate, createdAt: '2026-11-11T00:00:00Z', _offloaded: { content: { key: 'body', size: 100 } } } satisfies DocumentRevision
    const source = { ...document, revisions: [revision] }
    api.request.mockResolvedValue({ nodes: [{ ...revision, _offloaded: undefined, content: 'Earlier body', botActor: { name: 'Old attribution' } }] })
    const wrapper = ({ children }: { children: ReactNode }) => <WorkspaceStoreProvider data={base} session={null} account={null}>{children}</WorkspaceStoreProvider>
    const view = renderHook(({ source }) => useLoadedRevisions(source, true), { wrapper, initialProps: { source } })
    await waitFor(() => expect(view.result.current.revisions[0].content).toBe('Earlier body'))
    view.rerender({ source: { ...source, revisions: [{ ...revision, author: { ...teammate, displayName: 'Deleted user', active: false } }] } })
    expect(view.result.current.revisions[0].author.displayName).toBe('Deleted user')
    expect(view.result.current.revisions[0].author.active).toBe(false)
    expect(view.result.current.revisions[0]).not.toHaveProperty('botActor')
    expect(api.request).toHaveBeenCalledTimes(1)
  })

  it('releases history bodies on close and fetches them again on reopening', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
    const revision = { id: 'old', documentId: document.id, title: 'Earlier', content: '', author: teammate, createdAt: '2026-11-11T00:00:00Z', _offloaded: { content: { key: 'body', size: 100 } } } satisfies DocumentRevision
    const source = { ...document, revisions: [revision] }
    api.request.mockResolvedValue({ nodes: [{ ...revision, _offloaded: undefined, content: 'Earlier body' }] })
    const wrapper = ({ children }: { children: ReactNode }) => <WorkspaceStoreProvider data={base} session={null} account={null}>{children}</WorkspaceStoreProvider>
    const view = renderHook(({ enabled }) => useLoadedRevisions(source, enabled), { wrapper, initialProps: { enabled: true } })
    await waitFor(() => expect(view.result.current.revisions[0].content).toBe('Earlier body'))
    view.rerender({ enabled: false })
    expect(view.result.current.revisions[0].content).toBe('')
    view.rerender({ enabled: true })
    await waitFor(() => expect(api.request).toHaveBeenCalledTimes(2))
  })

  it('loads newly offloaded revisions while the history view stays open', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
    const revision = { id: 'old', documentId: document.id, title: 'Earlier', content: '', author: teammate, createdAt: '2026-11-11T00:00:00Z', _offloaded: { content: { key: 'body', size: 100 } } } satisfies DocumentRevision
    const source = { ...document, revisions: [revision] }
    api.request.mockResolvedValueOnce({ nodes: [{ ...revision, _offloaded: undefined, content: 'Earlier body' }] })
    const wrapper = ({ children }: { children: ReactNode }) => <WorkspaceStoreProvider data={base} session={null} account={null}>{children}</WorkspaceStoreProvider>
    const view = renderHook(({ source }) => useLoadedRevisions(source, true), { wrapper, initialProps: { source } })
    await waitFor(() => expect(view.result.current.revisions[0].content).toBe('Earlier body'))
    const next = { ...revision, id: 'new' }
    api.request.mockResolvedValueOnce({ nodes: [{ ...revision, _offloaded: undefined, content: 'Earlier body' }, { ...next, _offloaded: undefined, content: 'Newly offloaded body' }] })
    view.rerender({ source: { ...source, revisions: [revision, next] } })
    await waitFor(() => expect(view.result.current.revisions[1].content).toBe('Newly offloaded body'))
  })
})

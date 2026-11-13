import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { useLoadedRevisions } from '@/components/documents/use-loaded-revisions'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeBootstrap } from '@/test/fixtures'
import { ApiError } from '@/lib/api-client'
import type { DocumentRevision, FlowDocument } from '@/types/flow'

const api = vi.hoisted(() => ({ request: vi.fn(), fetchDocumentRevisions: vi.fn() }))
vi.mock('@/lib/api-client', async original => ({ ...await original<object>(), request: api.request }))
vi.mock('@/lib/api', async original => ({ ...await original<object>(), fetchDocumentRevisions: api.fetchDocumentRevisions }))
afterEach(() => { vi.unstubAllEnvs(); vi.resetAllMocks() })
const data = makeBootstrap()
const revisions: DocumentRevision[] = Array.from({ length: 100 }, (_, index) => ({ id: `version-${index}`, documentId: 'doc', title: `Version ${index}`, content: '', author: data.viewer, createdAt: '2026-11-11T12:00:00Z', _bodyOmitted: true }))
const document = { id: 'doc', title: 'Current', content: 'Current body', revisions } as FlowDocument
const wrapper = ({ children }: { children: ReactNode }) => <WorkspaceStoreProvider data={data} account={null} session={null}>{children}</WorkspaceStoreProvider>
const enable = () => { vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true') }
const body = (id: string) => ({ ...revisions.find(revision => revision.id === id)!, content: `Body for ${id}`, _bodyOmitted: undefined })

it('scale-p20 reads one selected version and only its comparison neighbor, keeping 100 headers', async () => {
  enable()
  api.request.mockImplementation(async (url: string) => body(url.split('/').at(-1)!))
  const view = renderHook(({ selectedId, highlight }) => useLoadedRevisions(document, true, { selectedId, highlight }), { wrapper, initialProps: { selectedId: 'version-50', highlight: false } })
  await waitFor(() => expect(view.result.current.revisions[50].content).toBe('Body for version-50'))
  expect(api.request.mock.calls.map(([url]) => url)).toEqual(['/api/documents/doc/history/version-50'])
  expect(view.result.current.revisions).toHaveLength(100)
  expect(view.result.current.revisions.filter(revision => !revision._bodyOmitted)).toHaveLength(1)
  view.rerender({ selectedId: 'version-70', highlight: true })
  await waitFor(() => expect(view.result.current.revisions[71].content).toBe('Body for version-71'))
  expect(api.request.mock.calls.slice(1).map(([url]) => url)).toEqual(['/api/documents/doc/history/version-70', '/api/documents/doc/history/version-71'])
  expect(view.result.current.revisions.filter(revision => !revision._bodyOmitted)).toHaveLength(2)
  expect(view.result.current.revisions[50]._bodyOmitted).toBe(true)
})

it('scale-p20 cancels superseded version reads and releases all bodies on close', async () => {
  enable()
  let finish!: (value: unknown) => void
  api.request.mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValue(body('version-2'))
  const view = renderHook(({ selectedId, enabled }) => useLoadedRevisions(document, enabled, { selectedId, highlight: false }), { wrapper, initialProps: { selectedId: 'version-1', enabled: true } })
  await waitFor(() => expect(api.request).toHaveBeenCalledTimes(1))
  const signal = api.request.mock.calls[0][1].signal as AbortSignal
  view.rerender({ selectedId: 'version-2', enabled: true })
  expect(signal.aborted).toBe(true)
  await waitFor(() => expect(view.result.current.revisions[2].content).toBe('Body for version-2'))
  await act(async () => { finish(body('version-1')) })
  expect(view.result.current.revisions[1]._bodyOmitted).toBe(true)
  view.rerender({ selectedId: 'version-2', enabled: false })
  expect(view.result.current.revisions.every(revision => revision._bodyOmitted)).toBe(true)
})

it('scale-p20 falls back once to old history transport without treating a denied version as absent', async () => {
  enable()
  api.request.mockRejectedValue(new ApiError('old replica', 404))
  api.fetchDocumentRevisions.mockResolvedValue([body('version-50')])
  const old = renderHook(() => useLoadedRevisions(document, true, { selectedId: 'version-50', highlight: false }), { wrapper })
  await waitFor(() => expect(old.result.current.revisions[50].content).toBe('Body for version-50'))
  expect(api.fetchDocumentRevisions).toHaveBeenCalledTimes(1)
  old.unmount()
  api.fetchDocumentRevisions.mockClear()
  api.request.mockRejectedValue(new ApiError('revoked', 403))
  const revoked = renderHook(() => useLoadedRevisions(document, true, { selectedId: 'version-50', highlight: false }), { wrapper })
  await waitFor(() => expect(api.request).toHaveBeenCalledTimes(2))
  expect(revoked.result.current.revisions[50]._bodyOmitted).toBe(true)
  expect(api.fetchDocumentRevisions).not.toHaveBeenCalled()
})

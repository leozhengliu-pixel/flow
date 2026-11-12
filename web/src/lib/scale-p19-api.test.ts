import { afterEach, expect, it, vi } from 'vitest'
import { listProjectUpdates } from './api'

afterEach(() => vi.unstubAllGlobals())

it('scale-p19 scopes update previews to their manifest workspace while legacy callers use the current route', async () => {
  const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => [] })
  vi.stubGlobal('fetch', fetchMock)
  window.history.replaceState({}, '', '/current-workspace/projects/all')
  const controller = new AbortController()
  await listProjectUpdates('project/1', controller.signal, 'manifest-workspace')
  await listProjectUpdates('project/1', controller.signal)
  expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(['/api/projects/project%2F1/updates', '/api/projects/project%2F1/updates'])
  const [scoped, legacy] = fetchMock.mock.calls.map(([, init]) => init as RequestInit)
  expect(new Headers(scoped.headers).get('X-Workspace-Key')).toBe('manifest-workspace')
  expect(new Headers(legacy.headers).get('X-Workspace-Key')).toBe('current-workspace')
  expect(scoped.signal).toBe(controller.signal)
})

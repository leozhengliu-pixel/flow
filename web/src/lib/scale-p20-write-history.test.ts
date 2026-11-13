import { afterEach, expect, it, vi } from 'vitest'
import { updateDocument } from '@/lib/api'

const api = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('@/lib/api-client', async original => ({ ...await original<object>(), request: api.request }))
afterEach(() => { vi.unstubAllEnvs(); vi.resetAllMocks() })

it('scale-p20 preserves the flag-off PATCH and opts into headers without filling an omitted body', async () => {
  api.request.mockResolvedValue({})
  const input = { title: 'Renamed' }
  await updateDocument('doc', input)
  const legacy = api.request.mock.calls[0]
  expect(legacy[0]).toBe('/api/documents/doc')
  expect(JSON.parse(legacy[1].body)).toEqual(input)
  vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_DOCUMENT_WINDOWS', 'true')
  await updateDocument('doc', input)
  expect(api.request.mock.calls[1][0]).toBe('/api/documents/doc?history=summary')
  expect(api.request.mock.calls[1][1]).toEqual(legacy[1])
})

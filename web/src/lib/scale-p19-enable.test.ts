import { afterEach, describe, expect, it, vi } from 'vitest'
import { readScaleP19Updates, readScaleP19Window, validateScaleP19Page } from './scale-p19-window-api'
import { scaleP19Groups } from '@/components/projects-page/scale-p19-view'
import { project } from '@/test/fixtures'
import type { ProjectDataGroup, ProjectPageItem } from '@/components/projects-page/projects-data-view'

const api = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('@/lib/api-client', async original => ({ ...await original<typeof import('@/lib/api-client')>(), ...api }))
afterEach(() => vi.resetAllMocks())
const page = () => ({ schema: 1 as const, revision: '9', versions: { [project.id]: '1' }, authzEpoch: 'epoch', items: [project], total: 23210, offset: 0, hasMore: true, nextCursor: 'bound-query-cursor' })

describe('scale-p19 enablement wire contract', () => {
  it('sends the complete query and explicit workspace without requesting a legacy directory or batch', async () => {
    api.request.mockResolvedValue(page())
    await readScaleP19Window('workspace-a', 'projects', { schema: 1, display: { ordering: 'Name', grouping: 'Status', layout: 'timeline' }, filters: [{ field: 'health', operator: 'is', values: ['on-track'] }] }, undefined, new AbortController().signal)
    const [path, init] = api.request.mock.calls[0]
    const url = new URL(path, 'http://example.test')
    expect(url.pathname).toBe('/api/sync/records')
    expect(JSON.parse(url.searchParams.get('window')!)).toMatchObject({ schema: 1, display: { ordering: 'Name', layout: 'timeline' } })
    expect(init.headers).toEqual({ 'X-Workspace-Key': 'workspace-a' })
    expect(api.request).toHaveBeenCalledTimes(1)
  })
  it.each([[], { items: [], total: 0, hasMore: false }, { ...page(), hasMore: true, nextCursor: undefined }, { ...page(), revision: 9 }, { ...page(), items: [{ body: 'x'.repeat(256 * 1024) }] }])('rejects old or unbounded responses for compatibility recovery', value => {
    expect(() => validateScaleP19Page(value as never)).toThrow()
  })
  it('reassembles oversized UTF-8 update values from bounded authenticated fragments', async () => {
    const update = { id: 'update', projectId: project.id, body: '项目'.repeat(50_000), comments: [{ id: 'comment', body: 'kept' }], reactions: { like: ['actor'] } }
    const raw = Buffer.from(JSON.stringify(update))
    const requests: string[] = []
    api.request.mockImplementation(async (path: string) => {
      requests.push(path)
      const url = new URL(path, 'http://example.test')
      if (!url.searchParams.has('update')) return { ...page(), items: [{ id: 'update' }], details: { update: true }, total: 1, hasMore: false, nextCursor: undefined }
      const offset = Number(url.searchParams.get('cursor') ?? 0)
      const end = Math.min(offset + 32768, raw.length)
      return { schema: 1, revision: '9', authzEpoch: 'epoch', encoding: 'base64-json', data: raw.subarray(offset, end).toString('base64'), offset, hasMore: end < raw.length, nextCursor: end < raw.length ? String(end) : undefined }
    })
    const result = await readScaleP19Updates('workspace', project.id, undefined, new AbortController().signal)
    expect(result.items).toEqual([update])
    expect(requests.length).toBeGreaterThan(3)
    expect(requests.every(path => new URL(path, 'http://example.test').searchParams.get('window') === '1')).toBe(true)
  })
  it('rejects a fragment from a different epoch before publishing a partial update', async () => {
    api.request.mockResolvedValueOnce({ ...page(), items: [{ id: 'update' }], details: { update: true }, total: 1, hasMore: false })
      .mockResolvedValueOnce({ schema: 1, revision: '9', authzEpoch: 'revoked', encoding: 'base64-json', data: 'e30=', offset: 0, hasMore: false })
    await expect(readScaleP19Updates('workspace', project.id, undefined, new AbortController().signal)).rejects.toMatchObject({ status: 409 })
  })
  it('uses the same negotiated update contract for initiatives', async () => {
    api.request.mockResolvedValue({ ...page(), items: [], total: 0, hasMore: false })
    await readScaleP19Updates('workspace', 'initiative', undefined, new AbortController().signal, 'initiatives')
    expect(api.request.mock.calls[0][0]).toContain('/api/initiatives/initiative/updates?window=1')
  })
  it('retains server ordering across ties and displays global group counts', () => {
    const first = { id: 'a' } as ProjectPageItem, second = { id: 'b' } as ProjectPageItem
    const groups: ProjectDataGroup[] = [{ id: 'status-started', name: 'Started', projects: [second, first] }]
    const result = scaleP19Groups(groups, [first, second], { groups: [{ id: 'status-started', name: 'Started', total: 23210 }] })
    expect(result[0].projects.map(item => item.id)).toEqual(['a', 'b'])
    expect(result[0].windowTotal).toBe(23210)
    expect(groups[0].projects).toEqual([second, first])
  })
})

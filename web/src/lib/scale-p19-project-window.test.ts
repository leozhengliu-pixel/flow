import { describe, expect, it, vi } from 'vitest'
import { ScaleP19ProjectWindow, SCALE_P19_WINDOW_BYTES } from './scale-p19-project-window'
import { project } from '@/test/fixtures'
import type { ProjectQueryInput, ProjectQueryPage } from '@/lib/api'

function directory(total = 23210) {
  return vi.fn(async (query: ProjectQueryInput): Promise<ProjectQueryPage> => {
    const offset = Number(query.cursor ?? 0)
    return {
      items: Array.from({ length: Math.min(query.limit ?? 100, total - offset) }, (_, index) => ({ ...project, id: `p-${offset + index}`, name: `Project ${offset + index}`, summary: 'x'.repeat(1024) })),
      total, hasMore: offset + 100 < total, nextCursor: offset + 100 < total ? String(offset + 100) : undefined,
    }
  })
}

describe('scale-p19 project query windows', () => {
  it('bounds 23,210 projects and 100 backward/forward navigations without losing cursors', async () => {
    const read = directory()
    const window = new ScaleP19ProjectWindow('workspace', read)
    const query = { archived: 'all' as const }
    for (let page = 0; page < 233; page++) {
      const result = await window.read(query, page)
      expect(result.items.length).toBeLessThanOrEqual(100)
      expect(result.total).toBe(23210)
      expect(window.retainedProjects).toBeLessThanOrEqual(400)
      expect(window.retainedBytes).toBeLessThanOrEqual(SCALE_P19_WINDOW_BYTES)
    }
    const warmed = window.retainedBytes
    for (let page = 0; page < 100; page++) await window.read(query, page % 2 ? 232 : 0)
    expect(window.retainedBytes).toBeLessThanOrEqual(warmed + 4096)
    expect((await window.read(query, 232)).items[0].id).toBe('p-23200')
    expect((await window.read(query, 232)).completeness).toBe('complete')
    window.dispose()
    expect(window.retainedProjects).toBe(0)
  })

  it('uses hasMore and server totals for a short, empty or full page', async () => {
    for (const items of [[], [project], Array.from({ length: 100 }, (_, i) => ({ ...project, id: `p-${i}` }))]) {
      const read = vi.fn().mockResolvedValueOnce({ items, total: 907, hasMore: true, nextCursor: 'scanned-hidden-rows' }).mockResolvedValue({ items: [], total: 907, hasMore: false })
      const window = new ScaleP19ProjectWindow('scope', read)
      expect((await window.read({})).completeness).toBe('partial')
      await window.read({}, 1)
      expect(read.mock.calls[1][0]).toMatchObject({ cursor: 'scanned-hidden-rows', includeTotal: true, limit: 100 })
      expect(read.mock.calls[1][2]).toBe('scope')
    }
    const complete = new ScaleP19ProjectWindow('scope', vi.fn().mockResolvedValue({ items: Array(100).fill(project), total: 100, hasMore: false }))
    expect((await complete.read({})).completeness).toBe('complete')
  })

  it('binds cursors to filters and aborts late requests when the query changes', async () => {
    let finish!: (page: ProjectQueryPage) => void
    const old = new Promise<ProjectQueryPage>(resolve => { finish = resolve })
    const read = vi.fn().mockReturnValueOnce(old).mockResolvedValue({ items: [], total: 0, hasMore: false })
    const window = new ScaleP19ProjectWindow('scope', read)
    const first = window.read({ q: 'old' })
    const rejected = expect(first).rejects.toMatchObject({ name: 'AbortError' })
    await window.read({ q: 'new' })
    finish({ items: [project], total: 999, hasMore: true, nextCursor: 'old-cursor' })
    await rejected
    expect(read.mock.calls[0][1].aborted).toBe(true)
    expect(window.retainedProjects).toBe(0)
    await expect(window.read({ q: 'new' }, 1)).rejects.toThrow('Unknown project page')
  })

  it('cancels a pending next page when navigating to a cached previous page', async () => {
    let finish!: (page: ProjectQueryPage) => void
    const read = directory(300)
    const window = new ScaleP19ProjectWindow('scope', read)
    await window.read({})
    read.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const pending = window.read({}, 1)
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    await window.read({}, 0)
    finish({ items: [project], total: 300, hasMore: true, nextCursor: '200' })
    await rejected
    expect(window.retainedProjects).toBe(100)
  })

  it('keeps bulk selection as IDs and labels after page bodies are evicted', async () => {
    const window = new ScaleP19ProjectWindow('scope', directory(1000))
    const first = await window.read({})
    window.select(first.items.map(item => item.id), first.items)
    for (let page = 1; page < 10; page++) await window.read({}, page)
    expect(window.selectedProjects).toHaveLength(100)
    expect(Object.keys(window.selectedProjects[0]).sort()).toEqual(['id', 'name'])
    window.select(['p-0'], [])
    expect(window.selectedProjects).toEqual([{ id: 'p-0', name: 'Project 0' }])
    window.dispose()
    expect(window.selectedProjects).toEqual([])
  })

  it('does not carry selected row models into a different filter result', async () => {
    const window = new ScaleP19ProjectWindow('scope', directory(500))
    const first = await window.read({ q: 'first' })
    window.select(['p-0'], first.items)
    await window.read({ q: 'first' }, 1)
    expect(window.selectedProjects).toHaveLength(1)
    await window.read({ q: 'different' })
    expect(window.selectedProjects).toEqual([])
    window.dispose()
  })

  it('rejects unbounded pages, unavailable totals and non-progressing cursors for legacy fallback', async () => {
    for (const page of [
      { items: [], total: -1, hasMore: false },
      { items: [], total: 1000, hasMore: true },
      { items: Array(101).fill(project), total: 101, hasMore: false },
      { items: [{ ...project, summary: 'x'.repeat(4 * 1024 * 1024) }], total: 1, hasMore: false },
    ]) await expect(new ScaleP19ProjectWindow('scope', vi.fn().mockResolvedValue(page)).read({})).rejects.toMatchObject({ status: 501 })
  })
})

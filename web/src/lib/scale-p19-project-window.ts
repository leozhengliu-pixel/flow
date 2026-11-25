import { listProjectRecords, type ProjectQueryInput, type ProjectQueryPage } from '@/lib/api'
import { ApiError } from '@/lib/api-client'
import { cacheStringBytes } from '@/lib/scale-p08'
import type { Project } from '@/types/flow'
import { releaseScaleP19Bytes, retainScaleP19Bytes } from '@/lib/scale-p19-budget'
import type { ScaleP19ServerQuery } from '@/lib/scale-p19-window-api'

export const SCALE_P19_WINDOW_BYTES = 8 * 1024 * 1024
export const SCALE_P19_PAGE_SIZE = 100

export type ScaleP19ProjectQuery = Omit<ProjectQueryInput, 'cursor' | 'limit' | 'includeTotal'> & { window?: ScaleP19ServerQuery }
export type ScaleP19ProjectPage = ProjectQueryPage & { index: number; offset: number; completeness: 'partial' | 'complete' }
type ReadPage = (query: ProjectQueryInput & { window?: ScaleP19ServerQuery }, signal: AbortSignal, workspace: string) => Promise<ProjectQueryPage>
type Lifecycle = { commit: (page: ProjectQueryPage) => void; evict: (page: ProjectQueryPage) => void }

/** Page bodies are evictable; the much smaller cursor trail keeps backward navigation possible. */
export class ScaleP19ProjectWindow {
  private controller = new AbortController()
  private pages = new Map<number, ScaleP19ProjectPage>()
  private cursors = new Map<number, string | undefined>([[0, undefined]])
  private offsets = new Map<number, number>([[0, 0]])
  private queryKey = ''
  private selectionKey = ''
  private clock?: { now: string; localMonth: string }
  private generation = 0
  private closed = false
  private bytes = 0
  private selected = new Map<string, Pick<Project, 'id' | 'name'>>()
  readonly workspace: string
  private readonly readPage: ReadPage
  private readonly maxBytes: number
  private readonly lifecycle?: Lifecycle

  constructor(workspace: string, readPage: ReadPage = listProjectRecords, maxBytes = SCALE_P19_WINDOW_BYTES, lifecycle?: Lifecycle) {
    this.workspace = workspace
    this.readPage = readPage
    this.maxBytes = maxBytes
    this.lifecycle = lifecycle
  }

  get retainedBytes() { return this.bytes + cacheStringBytes(JSON.stringify([...this.cursors])) + cacheStringBytes(JSON.stringify([...this.offsets])) + this.cursors.size * 128 + cacheStringBytes(JSON.stringify([...this.selected])) + this.selected.size * 64 }
  get retainedProjects() { return [...this.pages.values()].reduce((sum, page) => sum + page.items.length, 0) }
  get selectedProjects() { return [...this.selected.values()] }

  cancel() { this.controller.abort(); this.generation++ }

  select(ids: string[], projects: Pick<Project, 'id' | 'name'>[]) {
    const wanted = new Set(ids)
    const selected = new Map([...this.selected].filter(([id]) => wanted.has(id)))
    for (const project of projects) if (wanted.has(project.id)) selected.set(project.id, { id: project.id, name: project.name })
    // Selection is IDs and labels only, never an implicit full-directory model.
    if (cacheStringBytes(JSON.stringify([...selected])) + selected.size * 64 > this.maxBytes / 2) throw new Error('Project selection exceeds its byte budget')
    const previous = this.selected
    this.selected = selected
    if (!this.retain()) { this.selected = previous; throw new Error('Project selection exceeds the shared directory budget') }
  }

  async read(query: ScaleP19ProjectQuery, index = 0): Promise<ScaleP19ProjectPage> {
    if (this.closed) throw new DOMException('Window disposed', 'AbortError')
    const key = JSON.stringify(query)
    const selectionKey = JSON.stringify([query.q, query.teamId, query.archived, query.filter])
    if (selectionKey !== this.selectionKey) { this.selected.clear(); this.selectionKey = selectionKey }
    if (key !== this.queryKey) {
      this.invalidate()
      this.queryKey = key
    }
    if (!this.cursors.has(index)) throw new Error('Unknown project page')
    this.cancel()
    const cached = this.pages.get(index)
    if (cached) {
      this.pages.delete(index); this.pages.set(index, cached)
      return cached
    }
    const controller = this.controller = new AbortController()
    const generation = ++this.generation
    if (!this.clock) { const now = new Date(); this.clock = { now: now.toISOString(), localMonth: `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}` } }
    const request = { ...query, ...(query.window ? { window: { ...this.clock, ...query.window } } : {}), cursor: this.cursors.get(index), limit: SCALE_P19_PAGE_SIZE, includeTotal: true }
    const page = await this.readPage(request, controller.signal, this.workspace)
    if (this.closed || controller.signal.aborted || generation !== this.generation) throw new DOMException('Window superseded', 'AbortError')
    if (!Array.isArray(page.items) || page.items.length > SCALE_P19_PAGE_SIZE || !Number.isSafeInteger(page.total) || page.total < 0 || typeof page.hasMore !== 'boolean' || page.hasMore && (!page.nextCursor || page.nextCursor === this.cursors.get(index))) throw new ApiError('Project directory does not support query windows', 501)
    const result: ScaleP19ProjectPage = { ...page, index, offset: this.offsets.get(index)!, completeness: page.hasMore ? 'partial' : 'complete' }
    const size = cacheStringBytes(JSON.stringify(result))
    if (size > this.maxBytes / 2 || new TextEncoder().encode(JSON.stringify(page)).byteLength > 256 * 1024) throw new ApiError('Project page exceeds its byte budget', 501)
    this.pages.set(index, result)
    this.bytes += size
    if (page.hasMore) { this.cursors.set(index + 1, page.nextCursor); this.offsets.set(index + 1, result.offset + page.items.length) }
    else for (const cursor of this.cursors.keys()) if (cursor > index) { this.cursors.delete(cursor); this.offsets.delete(cursor) }
    while (this.pages.size > 4 || this.bytes > this.maxBytes / 2) {
      const oldest = this.pages.keys().next().value!
      const evicted = this.pages.get(oldest)!
      this.bytes -= cacheStringBytes(JSON.stringify(evicted))
      this.pages.delete(oldest)
      this.lifecycle?.evict(evicted)
    }
    if (this.retainedBytes > this.maxBytes || !this.retain()) { this.invalidate(); throw new ApiError('Project window exceeds its byte budget', 501) }
    this.lifecycle?.commit(page)
    return result
  }

  refresh() {
    this.cancel()
    for (const page of this.pages.values()) this.lifecycle?.evict(page)
    this.pages.clear()
    this.bytes = 0
    this.retain()
  }

  invalidate() {
    this.refresh()
    this.clock = undefined
    this.cursors = new Map([[0, undefined]])
    this.offsets = new Map([[0, 0]])
    this.retain()
  }

  private retain() {
    if (!this.pages.size && !this.selected.size && this.cursors.size === 1) { releaseScaleP19Bytes(this); return true }
    return retainScaleP19Bytes(this, this.retainedBytes)
  }

  dispose() { this.invalidate(); this.selected.clear(); releaseScaleP19Bytes(this); this.closed = true }
}

import { recordKey } from '@/lib/record-loader'
import { cacheStringBytes } from '@/lib/scale-p08'
import { modelRecordKind } from '@/lib/scale-p13-wire'
import type { SyncPageRequest, SyncRecordPage } from '@/types/flow'

export interface QueryWindow {
  query: SyncPageRequest
  keys: string[]
  completeness: 'omitted' | 'partial' | 'complete' | 'stale'
  revision: string
  next: string
  loading: boolean
  error?: unknown
}

const windowKey = ({ kind, filter = '', order = '', limit = 100, maxBytes = 256 * 1024 }: SyncPageRequest) => JSON.stringify([modelRecordKind(kind), filter, order, limit, maxBytes])

/** Completeness belongs to one filter/order window, never to its array length. */
export class QueryWindowStore {
  private windows = new Map<string, QueryWindow>()
  private readonly maxWindows: number
  constructor(maxWindows = 64) { this.maxWindows = Math.max(1, maxWindows) }

  get(query: SyncPageRequest): QueryWindow | undefined { return this.windows.get(windowKey(query)) }

  begin(query: SyncPageRequest): QueryWindow {
    const previous = this.get(query)
    const next: QueryWindow = { query: { ...query }, keys: [], completeness: 'omitted', revision: '', next: '', ...previous, loading: true, error: undefined }
    this.put(query, next)
    return next
  }

  apply(query: SyncPageRequest, page: SyncRecordPage): QueryWindow {
    const previous = this.get(query)
    if (page.hasMore && !page.next) throw new Error('Record page has no continuation')
    if (query.cursor && (!previous || previous.next !== query.cursor || previous.revision !== page.revision || previous.completeness === 'stale')) {
      this.invalidate(query.kind)
      throw new Error('Record window changed while paging')
    }
    const keys = [...new Set([...(query.cursor ? previous!.keys : []), ...page.items.filter(item => item.completeness !== 'deleted').map(item => recordKey(item.key))])]
    if (keys.length > 10_000) throw new Error('Record window exceeds key budget')
    const next: QueryWindow = { query: { ...query, cursor: undefined }, keys, completeness: page.hasMore ? 'partial' : 'complete', revision: page.revision, next: page.next, loading: false }
    this.put(query, next)
    return next
  }

  fail(query: SyncPageRequest, error: unknown) {
    const previous = this.get(query)
    if (previous) this.put(query, { ...previous, loading: false, error })
  }

  invalidate(kind?: string) {
    for (const [key, window] of this.windows) if (!kind || modelRecordKind(window.query.kind) === modelRecordKind(kind)) this.windows.set(key, { ...window, completeness: 'stale', loading: false })
  }

  clear() { this.windows.clear() }

  private put(query: SyncPageRequest, window: QueryWindow) {
    const size = (value: QueryWindow) => cacheStringBytes(JSON.stringify([value.query, value.keys]))
    if (size(window) > 8 * 1024 * 1024) throw new Error('Record window exceeds byte budget')
    const key = windowKey(query)
    this.windows.delete(key)
    this.windows.set(key, window)
    while (this.windows.size > this.maxWindows) this.windows.delete(this.windows.keys().next().value!)
    let bytes = [...this.windows.values()].reduce((sum, value) => sum + size(value), 0)
    while (bytes > 8 * 1024 * 1024) {
      const key = this.windows.keys().next().value!
      bytes -= size(this.windows.get(key)!)
      this.windows.delete(key)
    }
  }
}

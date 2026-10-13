import type { PageInfo } from './generated/models.js'

export type { PageInfo }

/** Pagination arguments shared by every list. */
export interface PageArgs {
  /** Page size counting forward (1-250, default 50). */
  first?: number
  /** Continue after this cursor (`pageInfo.endCursor`). */
  after?: string
  /** Page size counting backward. */
  last?: number
  /** Continue backward from this cursor (`pageInfo.startCursor`). */
  before?: string
}

/** One request for a page; the fetcher adds the connection's own filters. */
export interface PageRequest {
  first?: number
  after?: string
  last?: number
  before?: string
}

export interface Page<T> {
  nodes: T[]
  pageInfo: PageInfo
  totalCount?: number
}

export type PageFetcher<T> = (request: PageRequest) => Promise<Page<T>>

/**
 * A page of results with the means to fetch its neighbours, like the
 * connections of the reference app SDK.
 *
 * ```ts
 * const issues = await client.issues({ first: 50 })
 * for (const issue of issues.nodes) …
 * if (issues.pageInfo.hasNextPage) await issues.fetchNext() // appends to issues.nodes
 *
 * for await (const issue of await client.issues({ first: 100 })) …  // every page
 * ```
 */
export class Connection<T> implements AsyncIterable<T> {
  /** The nodes fetched so far, in server order. `fetchNext` appends, `fetchPrevious` prepends. */
  nodes: T[]
  pageInfo: PageInfo
  /** Total number of matching nodes when the endpoint reports it (issues do). */
  totalCount?: number
  private readonly fetchPage: PageFetcher<T>
  private readonly pageSize: number

  constructor(page: Page<T>, fetchPage: PageFetcher<T>, pageSize: number = page.nodes.length || 50) {
    this.nodes = page.nodes
    this.pageInfo = page.pageInfo
    this.totalCount = page.totalCount
    this.fetchPage = fetchPage
    this.pageSize = pageSize
  }

  /** Fetches the next page and appends its nodes to `nodes`. No-op without a next page. */
  async fetchNext(): Promise<this> {
    const cursor = this.pageInfo.endCursor
    if (!this.pageInfo.hasNextPage || !cursor) return this
    const page = await this.fetchPage({ first: this.pageSize, after: cursor })
    this.nodes = [...this.nodes, ...page.nodes]
    this.pageInfo = { ...this.pageInfo, hasNextPage: page.pageInfo.hasNextPage, endCursor: page.pageInfo.endCursor ?? cursor }
    if (page.totalCount !== undefined) this.totalCount = page.totalCount
    return this
  }

  /** Fetches the previous page and prepends its nodes to `nodes`. No-op without a previous page. */
  async fetchPrevious(): Promise<this> {
    const cursor = this.pageInfo.startCursor
    if (!this.pageInfo.hasPreviousPage || !cursor) return this
    const page = await this.fetchPage({ last: this.pageSize, before: cursor })
    this.nodes = [...page.nodes, ...this.nodes]
    this.pageInfo = { ...this.pageInfo, hasPreviousPage: page.pageInfo.hasPreviousPage, startCursor: page.pageInfo.startCursor ?? cursor }
    return this
  }

  /**
   * Yields this page's nodes, then the nodes of every following page, fetching
   * lazily. The connection itself is not modified.
   */
  async *[Symbol.asyncIterator](): AsyncGenerator<T, void, undefined> {
    for await (const page of this.pages()) yield* page
  }

  /** Yields the nodes of this page, then one array per following page. */
  async *pages(): AsyncGenerator<T[], void, undefined> {
    yield [...this.nodes]
    let { hasNextPage, endCursor } = this.pageInfo
    while (hasNextPage && endCursor) {
      const page = await this.fetchPage({ first: this.pageSize, after: endCursor })
      if (page.nodes.length > 0) yield page.nodes
      const next = page.pageInfo.endCursor
      if (!next || next === endCursor) return
      hasNextPage = page.pageInfo.hasNextPage
      endCursor = next
    }
  }

  /** Collects this page and every following page into one array. `limit` stops early. */
  async all(options: { limit?: number } = {}): Promise<T[]> {
    const result: T[] = []
    for await (const node of this) {
      result.push(node)
      if (options.limit !== undefined && result.length >= options.limit) break
    }
    return result
  }
}

/** Result shape of cursor-paged endpoints (`items`, `hasMore`, `nextCursor`). */
export interface CursorPage<T> {
  items: T[]
  hasMore: boolean
  nextCursor?: string | null
  total?: number
}

/**
 * Adapts a forward-only cursor page to a Connection. `fetch(cursor)` loads
 * the page that starts after `cursor`. `fetchPrevious` is a no-op for these
 * endpoints.
 */
export async function cursorConnection<T>(fetch: (cursor: string | undefined, limit: number) => Promise<CursorPage<T>>, options: { after?: string; limit: number }): Promise<Connection<T>> {
  const toPage = (page: CursorPage<T>, after: string | undefined): Page<T> => ({
    nodes: page.items,
    totalCount: page.total,
    pageInfo: {
      hasNextPage: Boolean(page.hasMore && page.nextCursor),
      hasPreviousPage: false,
      startCursor: after ?? null,
      endCursor: page.nextCursor || null,
    },
  })
  const first = toPage(await fetch(options.after, options.limit), options.after)
  return new Connection<T>(
    first,
    async request => {
      if (!request.after) return { nodes: [], pageInfo: { hasNextPage: false, hasPreviousPage: false } }
      return toPage(await fetch(request.after, request.first ?? options.limit), request.after)
    },
    options.limit,
  )
}

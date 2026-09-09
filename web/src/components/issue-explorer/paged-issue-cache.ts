import type { Issue } from '@/types/flow'

export type IssuePageBlock = { items: Issue[]; nextCursor?: string; hasMore: boolean }

// Keep entity payloads bounded; page cursors are small navigation bookmarks.
export class PagedIssueCache {
  private pages = new Map<string, IssuePageBlock>()
  private sizes = new Map<string, number>()
  private bytes = 0
  private starts = new Map<string, number[]>()
  readonly cursors = new Map<string, string | undefined>()
  readonly capacity: number
  readonly maxBytes: number
  constructor(capacity = 40, maxBytes = 16 * 1024 * 1024) { this.capacity = capacity; this.maxBytes = maxBytes }
  key(group: string, page: number) { return JSON.stringify([group, page]) }
  start(group: string, page: number) { return this.starts.get(group)?.[page] ?? 0 }
  pageAt(group: string, index: number) {
    const starts = this.starts.get(group) ?? [0]
    let lo = 0, hi = starts.length - 1
    while (lo < hi) { const middle = Math.ceil((lo + hi) / 2); if (starts[middle] <= index) lo = middle; else hi = middle - 1 }
    return lo
  }
  get(group: string, page: number) {
    const key = this.key(group, page), value = this.pages.get(key)
    if (value) { this.pages.delete(key); this.pages.set(key, value) }
    return value
  }
  put(group: string, page: number, value: IssuePageBlock, cursor?: string) {
    const key = this.key(group, page)
    const starts = this.starts.get(group) ?? [0]
    starts[page + 1] = starts[page] + value.items.length
    this.starts.set(group, starts)
    value = { ...value, items: value.items.map(listIssue) }
    this.bytes -= this.sizes.get(key) ?? 0
    const size = JSON.stringify(value).length * 2 + value.items.length * 512
    this.sizes.set(key, size); this.bytes += size
    this.pages.delete(key); this.pages.set(key, value)
    this.cursors.set(key, cursor)
    if (value.hasMore) this.cursors.set(this.key(group, page + 1), value.nextCursor)
    while (this.pages.size > this.capacity || this.bytes > this.maxBytes && this.pages.size > 1) {
      const oldest = this.pages.keys().next().value!
      this.bytes -= this.sizes.get(oldest) ?? 0
      this.sizes.delete(oldest); this.pages.delete(oldest)
    }
  }
  hasCursor(group: string, page: number) { return page === 0 || this.cursors.has(this.key(group, page)) }
  update(issue: Issue): Issue | undefined {
    issue = listIssue(issue)
    let previous: Issue | undefined
    for (const [key, page] of this.pages) {
      const index = page.items.findIndex(item => item.id === issue.id)
      if (index >= 0) {
        previous = page.items[index]; page.items = page.items.map((item, i) => i === index ? issue : item)
        this.bytes -= this.sizes.get(key) ?? 0
        const size = JSON.stringify(page).length * 2 + page.items.length * 512
        this.sizes.set(key, size); this.bytes += size
      }
    }
    while (this.bytes > this.maxBytes && this.pages.size > 1) { const key = this.pages.keys().next().value!; this.bytes -= this.sizes.get(key) ?? 0; this.sizes.delete(key); this.pages.delete(key) }
    return previous
  }
  get retainedEntities() { return [...this.pages.values()].reduce((sum, page) => sum + page.items.length, 0) }
  get retainedBytes() { return this.bytes }
  records() { return [...new Map([...this.pages.values()].flatMap(page => page.items).map(issue => [issue.id, issue])).values()] }
}

function listIssue(issue: Issue): Issue {
  return { ...issue, isSummary: true, description: '', descriptionState: undefined, documentContent: undefined, reactions: {}, subscriberIds: [] }
}

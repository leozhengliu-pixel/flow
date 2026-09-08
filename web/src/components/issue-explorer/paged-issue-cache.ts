import type { Issue } from '@/types/flow'

export type IssuePageBlock = { items: Issue[]; nextCursor?: string; hasMore: boolean }

// Keep entity payloads bounded; page cursors are small navigation bookmarks.
export class PagedIssueCache {
  private pages = new Map<string, IssuePageBlock>()
  readonly cursors = new Map<string, string | undefined>()
  readonly capacity: number
  constructor(capacity = 40) { this.capacity = capacity }
  key(group: string, page: number) { return JSON.stringify([group, page]) }
  get(group: string, page: number) {
    const key = this.key(group, page), value = this.pages.get(key)
    if (value) { this.pages.delete(key); this.pages.set(key, value) }
    return value
  }
  put(group: string, page: number, value: IssuePageBlock, cursor?: string) {
    const key = this.key(group, page)
    this.pages.delete(key); this.pages.set(key, value)
    this.cursors.set(key, cursor)
    if (value.hasMore) this.cursors.set(this.key(group, page + 1), value.nextCursor)
    while (this.pages.size > this.capacity) this.pages.delete(this.pages.keys().next().value!)
  }
  hasCursor(group: string, page: number) { return page === 0 || this.cursors.has(this.key(group, page)) }
  update(issue: Issue): Issue | undefined {
    let previous: Issue | undefined
    for (const page of this.pages.values()) {
      const index = page.items.findIndex(item => item.id === issue.id)
      if (index >= 0) { previous = page.items[index]; page.items = page.items.map((item, i) => i === index ? issue : item) }
    }
    return previous
  }
  get retainedEntities() { return [...this.pages.values()].reduce((sum, page) => sum + page.items.length, 0) }
  records() { return [...new Map([...this.pages.values()].flatMap(page => page.items).map(issue => [issue.id, issue])).values()] }
}

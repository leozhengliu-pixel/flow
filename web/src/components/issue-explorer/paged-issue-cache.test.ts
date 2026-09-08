import { describe, expect, it } from 'vitest'
import { makeIssue } from '@/test/fixtures'
import { PagedIssueCache } from './paged-issue-cache'

describe('bounded issue page cache', () => {
  it('retains at most forty pages after traversing one million entities', () => {
    const cache = new PagedIssueCache()
    for (let page = 0; page < 10000; page++) {
      cache.put('started', page, { items: Array.from({ length: 100 }, (_, offset) => makeIssue({ id: `${page}-${offset}` })), hasMore: true, nextCursor: `cursor-${page + 1}` }, page ? `cursor-${page}` : undefined)
    }
    expect(cache.retainedEntities).toBe(4000)
    expect(cache.get('started', 0)).toBeUndefined()
    expect(cache.hasCursor('started', 0)).toBe(true)
    expect(cache.cursors.get(cache.key('started', 400))).toBe('cursor-400')
    expect(cache.get('started', 9999)?.items.length).toBe(100)
  })

  it('updates only cached entities and retains bookmarks when evicting old pages', () => {
    const cache = new PagedIssueCache(2)
    cache.put('a', 0, { items: [makeIssue({ id: 'one' })], hasMore: true, nextCursor: 'next' })
    cache.put('b', 0, { items: [makeIssue({ id: 'two' })], hasMore: false })
    const before = cache.get('a', 0)!.items
    cache.update(makeIssue({ id: 'one', title: 'Updated' }))
    expect(cache.get('a', 0)!.items).not.toBe(before)
    expect(cache.get('a', 0)!.items[0].title).toBe('Updated')
    cache.put('a', 1, { items: [makeIssue({ id: 'three' })], hasMore: false }, 'next')
    expect(cache.get('b', 0)).toBeUndefined()
    expect(cache.retainedEntities).toBe(2)
  })
})

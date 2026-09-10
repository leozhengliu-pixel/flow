import { describe, expect, it } from 'vitest'
import { makeIssue } from '@/test/fixtures'
import { PagedIssueCache } from './paged-issue-cache'

describe('bounded issue page cache', () => {
  it('navigates in group page order rather than LRU access order and stops at evicted gaps', () => {
    const cache = new PagedIssueCache(4)
    for (const [group, page, id] of [['a',0,'one'],['a',1,'two'],['a',2,'three'],['b',0,'other']] as const) cache.put(group,page,{items:[makeIssue({id})],hasMore:false})
    cache.get('a',0)
    expect(cache.sequence('a','two')).toEqual(['one','two','three'])
    cache.put('b',1,{items:[makeIssue({id:'another'})],hasMore:false})
    expect(cache.sequence('a','three')).toEqual(['three'])
    expect(cache.sequence('a','one')).toEqual(['one'])
  })
  it('locates variable-sized pages and excludes document bodies from cached list rows', () => {
    const cache = new PagedIssueCache(40, 6000)
    cache.put('a', 0, { items: [makeIssue({ description: 'x'.repeat(100000) }), makeIssue({ id: 'second' })], hasMore: true, nextCursor: 'next' })
    cache.put('a', 1, { items: [makeIssue({ id: 'third' })], hasMore: false }, 'next')
    expect(cache.pageAt('a', 2)).toBe(1)
    expect(cache.start('a', 1)).toBe(2)
    expect(cache.get('a', 1)?.items[0].isSummary).toBe(true)
    expect(cache.get('a', 1)?.items[0].description).toBe('')
    expect(cache.retainedBytes).toBeLessThanOrEqual(6000)
  })
  it('retains at most forty pages after traversing one million entities', () => {
    const cache = new PagedIssueCache()
    const rows = Array.from({ length: 100 }, (_, offset) => makeIssue({ id: `row-${offset}`, isSummary: true }))
    for (let page = 0; page < 10000; page++) {
      cache.put('started', page, { items: rows, hasMore: true, nextCursor: `cursor-${page + 1}` }, page ? `cursor-${page}` : undefined)
    }
    expect(cache.retainedEntities).toBe(4000)
    expect(cache.get('started', 0)).toBeUndefined()
    expect(cache.hasCursor('started', 0)).toBe(true)
    expect(cache.cursors.get(cache.key('started', 400))).toBe('cursor-400')
    expect(cache.get('started', 9999)?.items.length).toBe(100)
  }, 20000)

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

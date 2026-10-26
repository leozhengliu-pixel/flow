import { afterEach, describe, expect, it } from 'vitest'
import { clearNavigationCache } from '@/lib/navigation-cache'
import { makeIssue } from '@/test/fixtures'
import {
  ISSUE_LIST_CACHE_BUDGET_BYTES,
  clearPagedIssueListCache,
  issueListState,
  pagedIssueListCacheStats,
  pagedIssueListSignature,
  retainIssueList,
  trimPagedIssueListCache,
} from './paged-issue-list-cache'
import { ISSUE_QUERY_INVALIDATED } from './paged-issue-invalidation'

const query = { groupBy: 'status' as const }
const MB = 1024 * 1024
const scope = (viewerId: string, workspaceKey = 'acme') => ({ viewerId, workspaceKey })
const open = (viewerId: string, workspaceId = 'ws-1', workspaceKey = 'acme', listQuery: typeof query & Record<string, unknown> = query) =>
  issueListState(pagedIssueListSignature(viewerId, workspaceId, listQuery), scope(viewerId, workspaceKey))
/** A page whose retained size is about `megabytes` (two bytes per character). */
const fill = (state: ReturnType<typeof open>, megabytes: number, id = 'issue') => {
  state.cache.put('all', 0, { items: [makeIssue({ id, title: 'x'.repeat(Math.floor(megabytes * MB / 2)) })], hasMore: false })
}

afterEach(() => clearPagedIssueListCache())

describe('paged issue list cache keys', () => {
  it('never serves one viewer the rows cached for another in the same workspace and query', () => {
    const first = open('viewer-a')
    fill(first, 0.01, 'a-only')
    const second = open('viewer-b')
    expect(second).not.toBe(first)
    expect(second.cache.records()).toEqual([])
    expect(second.cache.contains('a-only')).toBe(false)
    expect(open('viewer-a')).toBe(first)
    expect(pagedIssueListSignature('viewer-a', 'ws-1', query)).not.toBe(pagedIssueListSignature('viewer-b', 'ws-1', query))
  })
})

describe('clearing cached lists with the navigation cache', () => {
  it('drops every viewer on logout', () => {
    const a = open('viewer-a'), b = open('viewer-b')
    clearNavigationCache()
    expect(pagedIssueListCacheStats().entries).toBe(0)
    expect(open('viewer-a')).not.toBe(a)
    expect(open('viewer-b')).not.toBe(b)
  })

  it('drops only the previous viewer on an account switch', () => {
    const a = open('viewer-a'), b = open('viewer-b')
    clearNavigationCache('viewer-a')
    expect(open('viewer-b')).toBe(b)
    expect(open('viewer-a')).not.toBe(a)
  })

  it('drops only that workspace when access to it is lost', () => {
    const acme = open('viewer-a', 'ws-1', 'acme'), other = open('viewer-a', 'ws-2', 'other')
    clearNavigationCache('viewer-a', 'acme')
    expect(open('viewer-a', 'ws-2', 'other')).toBe(other)
    expect(open('viewer-a', 'ws-1', 'acme')).not.toBe(acme)
  })
})

describe('forced permission invalidation', () => {
  const invalidate = (detail: { workspaceKey: string; force?: boolean }) => window.dispatchEvent(new CustomEvent(ISSUE_QUERY_INVALIDATED, { detail }))

  it('drops the workspace\'s lists nobody is showing, and leaves a mounted list to refresh itself', () => {
    const hidden = open('viewer-a', 'ws-1', 'acme', { ...query, teamId: 'a' })
    const shown = open('viewer-a', 'ws-1', 'acme', { ...query, teamId: 'b' })
    const elsewhere = open('viewer-a', 'ws-2', 'other')
    const release = retainIssueList(shown)
    invalidate({ workspaceKey: 'acme', force: true })
    expect(open('viewer-a', 'ws-1', 'acme', { ...query, teamId: 'b' })).toBe(shown)
    expect(open('viewer-a', 'ws-2', 'other')).toBe(elsewhere)
    expect(open('viewer-a', 'ws-1', 'acme', { ...query, teamId: 'a' })).not.toBe(hidden)
    release()
    invalidate({ workspaceKey: 'acme', force: true })
    expect(open('viewer-a', 'ws-1', 'acme', { ...query, teamId: 'b' })).not.toBe(shown)
  })

  it('keeps cached lists for an ordinary invalidation', () => {
    const state = open('viewer-a')
    invalidate({ workspaceKey: 'acme' })
    expect(open('viewer-a')).toBe(state)
  })
})

describe('total byte budget', () => {
  it('evicts least recently used lists until the total fits', () => {
    const lists = [1, 2, 3, 4, 5].map(index => {
      const state = open('viewer-a', `ws-${index}`, `key-${index}`)
      fill(state, 7, `issue-${index}`)
      trimPagedIssueListCache()
      return state
    })
    const { bytes, entries } = pagedIssueListCacheStats()
    expect(bytes).toBeLessThanOrEqual(ISSUE_LIST_CACHE_BUDGET_BYTES)
    expect(entries).toBeLessThan(5)
    // The newest lists stay warm, the oldest were dropped.
    expect(open('viewer-a', 'ws-5', 'key-5')).toBe(lists[4])
    expect(open('viewer-a', 'ws-1', 'key-1')).not.toBe(lists[0])
  })

  it('revisiting a list protects it from eviction', () => {
    const first = open('viewer-a', 'ws-1', 'key-1')
    fill(first, 7)
    const second = open('viewer-a', 'ws-2', 'key-2')
    fill(second, 7)
    expect(open('viewer-a', 'ws-1', 'key-1')).toBe(first)
    const third = open('viewer-a', 'ws-3', 'key-3')
    fill(third, 7)
    const fourth = open('viewer-a', 'ws-4', 'key-4')
    fill(fourth, 7)
    trimPagedIssueListCache()
    expect(open('viewer-a', 'ws-2', 'key-2')).not.toBe(second)
    expect(open('viewer-a', 'ws-4', 'key-4')).toBe(fourth)
  })

  it('does not cache a list larger than the whole budget', () => {
    const huge = open('viewer-a')
    fill(huge, ISSUE_LIST_CACHE_BUDGET_BYTES / MB + 2)
    trimPagedIssueListCache()
    expect(pagedIssueListCacheStats().entries).toBe(0)
    expect(open('viewer-a')).not.toBe(huge)
  })

  it('still caps the number of lists', () => {
    for (let index = 0; index < 12; index += 1) open('viewer-a', `ws-${index}`, `key-${index}`)
    expect(pagedIssueListCacheStats().entries).toBe(8)
  })
})

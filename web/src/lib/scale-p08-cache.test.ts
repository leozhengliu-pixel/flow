import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clearNavigationCache, hydrateWorkspaceNavigation, isNavigationHydrated, readNavigationCache, toNavigationSnapshot, writeNavigationCache, writeNavigationCacheWhenIdle } from './navigation-cache'
import { loadRealtimeCursor, saveRealtimeCursor } from './realtime-cache'
import { mergeIssueRecords, retainOpenIssueDetail } from './issue-detail-cache'
import { makeBootstrap, makeIssue, project, viewer } from '@/test/fixtures'

describe('scale-p08 bounded viewer caches', () => {
  beforeEach(() => { vi.stubEnv('VITE_BOUNDED_BROWSER_CACHE', 'true'); vi.stubEnv('VITE_APPLIED_REALTIME_CURSOR', 'true'); localStorage.clear(); clearNavigationCache() })
  afterEach(() => { vi.unstubAllEnvs(); vi.useRealTimers() })

  it('isolates cursor records by viewer, workspace and schema and clears them with a session', async () => {
    await saveRealtimeCursor('acme', '41', 'u1', 2)
    await saveRealtimeCursor('acme', '89', 'u2', 2)
    await saveRealtimeCursor('acme', '99', 'u1', 3)
    expect(loadRealtimeCursor('acme', 'u1', 2)).toBe('41')
    expect(loadRealtimeCursor('acme', 'u2', 2)).toBe('89')
    expect(loadRealtimeCursor('acme', 'u1', 3)).toBe('99')
    expect(loadRealtimeCursor('other', 'u1', 2)).toBeUndefined()
    expect(loadRealtimeCursor('acme')).toBeUndefined()
    clearNavigationCache('u1', 'acme')
    expect(loadRealtimeCursor('acme', 'u1', 2)).toBeUndefined()
    expect(loadRealtimeCursor('acme', 'u2', 2)).toBe('89')
  })

  it('persists only navigation summaries and marks every read as an incomplete baseline', () => {
    const data = makeBootstrap({ projects: [{ ...project, description: 'secret-body', comments: [{ body: 'secret-comment' }] as never, descriptionRevisions: [{ description: 'secret-history' }] as never }] })
    writeNavigationCache(viewer.id, 'workspace', data)
    const cached = readNavigationCache(viewer.id, 'workspace')!
    expect(isNavigationHydrated(cached)).toBe(true)
    expect(cached.resourceDetailsOmitted).toBe(true)
    expect(JSON.stringify(cached)).not.toContain('secret-')
    expect(toNavigationSnapshot(data).issues[0].description).toBeUndefined()
    const live = makeBootstrap({ issueCollectionPaged: true })
    expect(hydrateWorkspaceNavigation({ current: live, cached, viewerId: viewer.id, requestedWorkspaceKey: 'workspace' })).toBe(live)
  })

  it('bounds all stored navigation records across workspaces and rejects oversized records', () => {
    for (let i = 0; i < 12; i++) {
      const workspace = { ...makeBootstrap().workspace, id: `w-${i}`, urlKey: `w-${i}`, name: 'x'.repeat(160 * 1024) }
      writeNavigationCache(viewer.id, workspace.urlKey, makeBootstrap({ workspace }))
    }
    const bytes = Array.from({ length: localStorage.length }, (_, i) => localStorage.key(i)!)
      .filter(key => key.startsWith('flow:navigation:')).reduce((total, key) => total + new TextEncoder().encode(localStorage.getItem(key)!).length, 0)
    expect(bytes).toBeLessThanOrEqual(2 * 1024 * 1024)
    expect(readNavigationCache(viewer.id, 'w-0')).toBeUndefined()
    expect(readNavigationCache(viewer.id, 'w-11')).toBeDefined()
    writeNavigationCache(viewer.id, 'workspace', makeBootstrap({ workspace: { ...makeBootstrap().workspace, name: 'x'.repeat(3 * 1024 * 1024) } }))
    expect(readNavigationCache(viewer.id, 'workspace')).toBeUndefined()
  })

  it('cancels pending navigation writes when access is revoked', async () => {
    vi.useFakeTimers()
    writeNavigationCacheWhenIdle(viewer.id, 'workspace', makeBootstrap())
    clearNavigationCache(viewer.id, 'workspace')
    await vi.runAllTimersAsync()
    expect(readNavigationCache(viewer.id, 'workspace')).toBeUndefined()
  })

  it('evicts least recently merged issue bodies by bytes while keeping summary rows and editable content intact', () => {
    const issues = Array.from({ length: 80 }, (_, i) => makeIssue({ id: `i-${i}`, description: 'x'.repeat(512 * 1024), isSummary: false }))
    const bounded = mergeIssueRecords([], issues)
    const bytes = bounded.filter(issue => !issue.isSummary).reduce((total, issue) => total + new TextEncoder().encode(JSON.stringify(issue)).length, 0)
    expect(bytes).toBeLessThanOrEqual(32 * 1024 * 1024)
    expect(bounded).toHaveLength(80)
    expect(bounded.at(-1)?.isSummary).toBe(true)
    const recent = mergeIssueRecords(bounded, [issues.at(-1)!])
    expect(recent[0]).toBe(issues.at(-1))
    const summary = makeIssue({ id: recent[0].id, description: '', isSummary: true, version: 2 })
    const merged = mergeIssueRecords(recent, [summary])
    expect(merged[0]).toMatchObject({ isSummary: false, needsDetailRefresh: true, description: issues.at(-1)!.description })
    expect(issues.every(issue => !issue.isSummary)).toBe(true)
  })

  it('reserves the open editor before retaining other details', () => {
    const open = makeIssue({ id: 'open', description: 'x'.repeat(10 * 1024 * 1024), isSummary: false })
    const release = retainOpenIssueDetail(open.id)
    try {
      const newer = makeIssue({ id: 'newer', description: 'y'.repeat(10 * 1024 * 1024), isSummary: false })
      const merged = mergeIssueRecords([open], [newer])
      expect(merged.find(issue => issue.id === open.id)).toBe(open)
      expect(merged.find(issue => issue.id === newer.id)?.isSummary).toBe(true)
    } finally { release() }
  })

  it('preserves authoritative counts and content flags when a summary omits actors and body', () => {
    const hints = { hasContent: false, reactionCounts: { eyes: 7 } }
    const incoming = makeIssue({ isSummary: true, reactions: {}, ...hints })
    const [summary] = mergeIssueRecords([], [incoming])
    expect(summary).toMatchObject(hints)
    expect(summary.description).toBeUndefined()
  })
})

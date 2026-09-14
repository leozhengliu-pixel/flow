import { beforeEach, describe, expect, it } from 'vitest'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import { mergeWorkspaceDirectory } from '@/lib/issue-detail-cache'
import {
  clearNavigationCache,
  hydrateWorkspaceNavigation,
  readNavigationCache,
  toNavigationSnapshot,
  workspaceBootstrapPhase,
  writeNavigationCache,
} from './navigation-cache'

describe('navigation cache', () => {
  beforeEach(() => {
    localStorage.clear()
    clearNavigationCache()
  })

  it('reuses authorized navigation for the same viewer and workspace', () => {
    const data = makeBootstrap({
      issues: [makeIssue({ description: 'Secret body', isSummary: false })],
    })
    writeNavigationCache(viewer.id, 'workspace', data)
    expect(readNavigationCache(viewer.id, 'workspace')?.workspace.urlKey).toBe('workspace')
    expect(readNavigationCache(viewer.id, 'workspace')?.issues[0]?.description).toBeUndefined()
    expect(readNavigationCache(viewer.id, 'workspace')?.issues[0]?.isSummary).toBe(true)
  })

  it('does not leak another account or workspace', () => {
    writeNavigationCache(viewer.id, 'workspace', makeBootstrap())
    expect(readNavigationCache('user-2', 'workspace')).toBeUndefined()
    expect(readNavigationCache(viewer.id, 'other')).toBeUndefined()
    writeNavigationCache('user-2', 'workspace', makeBootstrap({ viewer: { ...viewer, id: 'user-2' } }))
    expect(readNavigationCache(viewer.id, 'workspace')?.viewer.id).toBe(viewer.id)
  })

  it('rejects writes that do not match the authorized viewer and workspace', () => {
    writeNavigationCache('user-2', 'workspace', makeBootstrap())
    writeNavigationCache(viewer.id, 'other', makeBootstrap())
    expect(readNavigationCache('user-2', 'workspace')).toBeUndefined()
    expect(readNavigationCache(viewer.id, 'other')).toBeUndefined()
  })

  it('rejects tampered cache entries that point at another viewer', () => {
    writeNavigationCache(viewer.id, 'workspace', makeBootstrap())
    const raw = JSON.parse(localStorage.getItem('flow:navigation:user-1:workspace')!)
    raw.data.viewer = { ...raw.data.viewer, id: 'intruder' }
    localStorage.setItem('flow:navigation:user-1:workspace', JSON.stringify(raw))
    expect(readNavigationCache(viewer.id, 'workspace')).toBeUndefined()
  })

  it('strips bulky issue bodies from cached snapshots', () => {
    const snapshot = toNavigationSnapshot(makeBootstrap({
      issues: [makeIssue({ description: 'Keep out of cache', descriptionState: 'doc', isSummary: false })],
      comments: { 'issue-1': [] },
      activities: { 'issue-1': [] },
    }))
    expect(snapshot.issues[0]?.description).toBeUndefined()
    expect(snapshot.comments).toEqual({})
    expect(snapshot.activities).toEqual({})
  })

  it('hydrates cached navigation without replacing a full independently loaded issue', () => {
    const cached = toNavigationSnapshot(makeBootstrap({
      issues: [makeIssue({ description: 'Cached summary', isSummary: true })],
    }))
    const preview = makeIssue({ description: 'Authorized body', isSummary: false })
    const hydrated = hydrateWorkspaceNavigation({
      current: null,
      cached,
      requestedWorkspaceKey: 'workspace',
      viewerId: viewer.id,
      preview: { viewerId: viewer.id, key: 'workspace:TST-1', issue: preview },
    })
    expect(hydrated?.issues[0]).toMatchObject({ description: 'Authorized body', isSummary: false })
    expect(hydrateWorkspaceNavigation({
      current: makeBootstrap(),
      cached,
      requestedWorkspaceKey: 'workspace',
      viewerId: viewer.id,
    })?.issues[0]?.description).toBe('Issue body')
    expect(hydrateWorkspaceNavigation({
      current: null,
      cached,
      requestedWorkspaceKey: 'workspace',
      viewerId: viewer.id,
      preview: { viewerId: viewer.id, key: 'workspace:TST-1', issue: makeIssue({ isSummary: true }) },
    })?.issues[0]?.isSummary).toBe(true)
  })

  it('keeps an in-flight bootstrap after cache hydration looks loaded', () => {
    expect(workspaceBootstrapPhase(undefined, 'workspace', undefined, 'user-1:workspace')).toBe('continue')
    expect(workspaceBootstrapPhase('workspace', 'workspace', 'user-1:workspace', 'user-1:workspace')).toBe('continue')
    expect(workspaceBootstrapPhase('workspace', 'workspace', undefined, 'user-1:workspace')).toBe('skip')
    expect(workspaceBootstrapPhase('workspace', 'other', 'user-1:workspace', 'user-1:other')).toBe('continue')
  })

  it('caches the merged workspace snapshot instead of the raw bootstrap payload', () => {
    const current = makeBootstrap({
      issueCollectionPaged: true,
      issues: [makeIssue({ id: 'issue-1', identifier: 'TST-1', description: 'Loaded body', isSummary: false, version: 4 })],
    })
    const incoming = makeBootstrap({
      issueCollectionPaged: true,
      issues: [makeIssue({ id: 'issue-1', identifier: 'TST-1', isSummary: true, version: 4 })],
    })
    const merged = mergeWorkspaceDirectory(current, incoming)
    writeNavigationCache(viewer.id, 'workspace', merged)
    expect(merged.issues[0]).toMatchObject({ id: 'issue-1', description: 'Loaded body', isSummary: false, version: 4 })
    expect(readNavigationCache(viewer.id, 'workspace')?.issues[0]).toMatchObject({ id: 'issue-1', isSummary: true })
    expect(readNavigationCache(viewer.id, 'workspace')?.issues[0]?.description).toBeUndefined()
  })
})

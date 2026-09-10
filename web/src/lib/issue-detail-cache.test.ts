import { describe, expect, it } from 'vitest'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import type { ActivityEvent, Comment, Issue } from '@/types/flow'
import { mergeIssueRecords, mergeWorkspaceDirectory, requiresIssueVisibilityCheck } from './issue-detail-cache'

const comment: Comment = { id: 'comment', version: 1, body: 'Loaded discussion', reactions: {}, createdAt: '2026-09-10T00:00:00Z', user: viewer }
const activity: ActivityEvent = { id: 'activity', type: 'issue.updated', createdAt: '2026-09-10T00:00:00Z', actor: viewer, metadata: {} }
const fullIssue = (overrides: Partial<Issue> = {}) => makeIssue({ version: 5, description: 'Editable body', descriptionState: 'state', documentContent: { id: 'document-content', version: 5, content: 'Editable body', contentState: 'state', contentData: { type: 'doc', content: [] }, updatedAt: '2026-09-10T00:00:00Z' }, reactions: { heart: ['viewer'] }, subscriberIds: ['viewer'], ...overrides })
const summary = (overrides: Partial<Issue> = {}) => makeIssue({ version: 5, isSummary: true, description: '', descriptionState: undefined, documentContent: undefined, reactions: {}, subscriberIds: [], ...overrides })

describe('bounded issue detail cache', () => {
  it('does not replace newer details with an older summary or older full response', () => {
    const current = fullIssue({ version: 10 })
    expect(mergeIssueRecords([current], [summary({ version: 9, title: 'Outdated' })])[0]).toBe(current)
    expect(mergeIssueRecords([current], [fullIssue({ version: 9, description: 'Old body' })])[0]).toBe(current)
  })

  it('preserves every omitted detail field when merging an equal-version summary', () => {
    const current = fullIssue()
    const merged = mergeIssueRecords([current], [summary()])[0]
    expect(merged).toMatchObject({ isSummary: false, description: 'Editable body', descriptionState: 'state', reactions: { heart: ['viewer'] }, subscriberIds: ['viewer'], needsDetailRefresh: false })
    expect(merged.documentContent).toBe(current.documentContent)
  })

  it('keeps the document when a newer summary arrives and marks it for authoritative refresh', () => {
    const current = fullIssue()
    const merged = mergeIssueRecords([current], [summary({ version: 6, title: 'New title' })])[0]
    expect(merged).toMatchObject({ title: 'New title', version: 6, description: 'Editable body', isSummary: false, needsDetailRefresh: true })
    expect(mergeIssueRecords([merged], [summary({ version: 6 })])[0].needsDetailRefresh).toBe(true)
    const refreshed = mergeIssueRecords([merged], [fullIssue({ version: 6, description: 'Authoritative body' })])[0]
    expect(refreshed.description).toBe('Authoritative body')
    expect(refreshed.needsDetailRefresh).toBeFalsy()
  })

  it('deduplicates repeated IDs and keeps at most 2000 records', () => {
    const current = Array.from({ length: 2500 }, (_, index) => fullIssue({ id: `old-${index}` }))
    const incoming = Array.from({ length: 2500 }, (_, index) => summary({ id: `new-${index}` }))
    const merged = mergeIssueRecords(current, [fullIssue({ id: 'duplicate', version: 10 }), summary({ id: 'duplicate', version: 9 }), ...incoming])
    expect(merged).toHaveLength(2000)
    expect(new Set(merged.map(issue => issue.id)).size).toBe(2000)
    expect(merged[0]).toMatchObject({ id: 'duplicate', version: 10, description: 'Editable body' })
    expect(mergeIssueRecords(current, incoming, 9000)).toHaveLength(2000)
    expect(mergeIssueRecords(current, incoming, 0)).toEqual([])
  })
})

describe('directory refresh preserves independently loaded issue content', () => {
  const current = () => makeBootstrap({ issueCollectionPaged: true, issueCollectionRevision: 7, issues: [fullIssue()], comments: { 'issue-1': [comment] }, activities: { 'issue-1': [activity] }, issueHistoryCursors: { 'issue-1': { commentsCursor: 'comments-next', activitiesCursor: 'activities-next' } } })

  it('retains details, history and cursors while accepting new directory fields', () => {
    const before = current()
    const next = makeBootstrap({ issueCollectionPaged: true, issues: [summary()], users: [], comments: { 'issue-1': [], 'document-1': [comment] }, activities: {}, issueHistoryCursors: {} })
    const merged = mergeWorkspaceDirectory(before, next)
    expect(merged.users).toEqual([])
    expect(merged.issues[0].description).toBe('Editable body')
    expect(merged.comments['issue-1']).toBe(before.comments['issue-1'])
    expect(merged.comments['document-1']).toEqual([comment])
    expect(merged.activities['issue-1']).toBe(before.activities['issue-1'])
    expect(merged.issueHistoryCursors).toEqual(before.issueHistoryCursors)
    expect(merged.issueCollectionRevision).toBe(7)
    expect(next.comments['issue-1']).toEqual([])
  })

  it('removes revoked issue records and history, including stale incoming summaries', () => {
    const before = current()
    const next = makeBootstrap({ issueCollectionPaged: true, issues: [summary()], comments: { 'issue-1': [comment], 'document-1': [comment] }, activities: { 'issue-1': [activity] }, issueHistoryCursors: before.issueHistoryCursors })
    const merged = mergeWorkspaceDirectory(before, next, new Set())
    expect(merged.issues).toEqual([])
    expect(merged.comments).toEqual({ 'document-1': [comment] })
    expect(merged.activities).toEqual({})
    expect(merged.issueHistoryCursors).toEqual({})
    expect(before.issues).toHaveLength(1)
    expect(next.issues).toHaveLength(1)
  })

  it('revalidates retained details and redacts references whose access was revoked', () => {
    const detail = fullIssue({ project: { id: 'private-project', name: 'Private project', color: '#777777' }, projectMilestoneId: 'private-milestone', parentId: 'revoked', subIssueIds: ['revoked', 'not-checked'], relations: [{ id: 'hidden-relation', type: 'related', issueId: 'issue-1', relatedIssueId: 'revoked' }, { id: 'other-relation', type: 'related', issueId: 'issue-1', relatedIssueId: 'not-checked' }] })
    const before = makeBootstrap({ issueCollectionPaged: true, issues: [detail, fullIssue({ id: 'revoked' })] })
    const next = makeBootstrap({ issueCollectionPaged: true, issues: [], labels: [], projects: [] })
    const merged = mergeWorkspaceDirectory(before, next, new Set(['issue-1']))
    expect(merged.issues).toHaveLength(1)
    expect(merged.issues[0]).toMatchObject({ needsDetailRefresh: true, description: 'Editable body', labels: [], subIssueIds: ['not-checked'], relations: [{ id: 'other-relation' }] })
    expect(merged.issues[0].project).toBeUndefined()
    expect(merged.issues[0].projectMilestoneId).toBeUndefined()
    expect(merged.issues[0].parentId).toBeUndefined()
    expect(before.issues[0].project?.id).toBe('private-project')
    expect(mergeIssueRecords(merged.issues, [fullIssue()])[0].needsDetailRefresh).toBeFalsy()
  })

  it('does not mark a normal directory name update as stale detail', () => {
    const before = current()
    const next = makeBootstrap({ issueCollectionPaged: true, issues: [], teams: [{ ...before.teams[0], name: 'New team name' }] })
    const merged = mergeWorkspaceDirectory(before, next)
    expect(merged.issues[0]).toBe(before.issues[0])
    expect(merged.issues[0].needsDetailRefresh).toBeFalsy()
    expect(merged.teams[0].name).toBe('New team name')
  })

  it('never carries content across workspaces, identities or the nonpaged contract', () => {
    const before = current()
    for (const next of [
      makeBootstrap({ issueCollectionPaged: true, issues: [], workspace: { ...before.workspace, id: 'another-workspace' } }),
      makeBootstrap({ issueCollectionPaged: true, issues: [], viewer: { ...viewer, id: 'another-viewer' } }),
      makeBootstrap({ issueCollectionPaged: false, issues: [] }),
    ]) expect(mergeWorkspaceDirectory(before, next)).toBe(next)
    expect(mergeWorkspaceDirectory(null, before)).toBe(before)
  })

  it('prunes evicted issue history along with the bounded record cache', () => {
    const next = makeBootstrap({ issueCollectionPaged: true, issues: Array.from({ length: 2000 }, (_, index) => summary({ id: `new-${index}` })) })
    const merged = mergeWorkspaceDirectory(current(), next)
    expect(merged.issues).toHaveLength(2000)
    expect(merged.comments).not.toHaveProperty('issue-1')
    expect(merged.activities).not.toHaveProperty('issue-1')
    expect(merged.issueHistoryCursors).not.toHaveProperty('issue-1')
  })

  it('requests visibility checks for the emitted sharing and membership event names', () => {
    for (const type of ['resync', 'workspace.resync_required', 'team.settings_updated', 'team_member.updated', 'workspace_member.suspended', 'issue.permissions_updated', 'issue.permission_updated', 'issue.permission_deleted', 'issue.shared', 'issue.unshared']) expect(requiresIssueVisibilityCheck(type), type).toBe(true)
    for (const type of ['issue.updated', 'comment.created', 'notification.updated', 'project.updated', 'favorite.added']) expect(requiresIssueVisibilityCheck(type), type).toBe(false)
  })
})

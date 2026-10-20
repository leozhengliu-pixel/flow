import { describe, expect, it } from 'vitest'
import { teammate, viewer } from '@/test/fixtures'
import type { CodeReview } from '@/types/flow'
import { resolveReviewShortcut } from './pull-request-review-shortcuts'
import { reviewChecksSummary, reviewCodeThreads, reviewFileGroups, reviewPrimaryAction, splitReviewPath, splitSuggestionBody } from './pull-request-review-model'

const base = { id: 'r', slugId: 'r', provider: 'github', externalId: 'r', number: 1, title: 'r', description: '', status: 'open', repositoryOwner: 'o', repositoryName: 'n', url: '', author: teammate, reviewerIds: [viewer.id], teamReviewers: [], issueIds: [], baseBranch: 'main', headBranch: 'h', branchState: 'upToDate', additions: 0, deletions: 0, commitCount: 1, checks: [], files: [], events: [], favorite: false, draft: false, quickToReview: false, createdAt: '', updatedAt: '' } as CodeReview
const key = (key: string, extra: Partial<KeyboardEvent> = {}) => ({ key, code: /^[a-z]$/i.test(key) ? `Key${key.toUpperCase()}` : '', shiftKey: false, altKey: false, metaKey: false, ctrlKey: false, ...extra })

describe('pull request review model', () => {
  it('chooses the primary action for authors and reviewers', () => {
    expect(reviewPrimaryAction({ ...base, author: viewer, reviewerIds: [], draft: true }, viewer.id).kind).toBe('readyForReview')
    expect(reviewPrimaryAction({ ...base, author: viewer, draft: true, reviewerIds: [teammate.id] }, viewer.id).kind).toBe('markReady')
    expect(reviewPrimaryAction({ ...base, author: viewer, reviewerIds: [] }, viewer.id).kind).toBe('requestReview')
    expect(reviewPrimaryAction(base, viewer.id)).toMatchObject({ kind: 'submitReview', primary: true })
    expect(reviewPrimaryAction({ ...base, reviewerIds: [], events: [{ id: 'e', type: 'approved', actor: viewer, createdAt: '' }] }, viewer.id).kind).toBe('reviewAgain')
    expect(reviewPrimaryAction({ ...base, status: 'merged' }, viewer.id).kind).toBe('none')
    expect(reviewPrimaryAction({ ...base, status: 'approved', checks: [{ id: 'c', name: 'ci', status: 'passed' }] }, viewer.id).mergeable).toBe(true)
  })
  it('summarizes checks like the provider', () => {
    expect(reviewChecksSummary([])).toBeNull()
    expect(reviewChecksSummary([{ id: '1', name: 'test', status: 'passed' }])).toMatchObject({ tone: 'success', text: 'All passed' })
    expect(reviewChecksSummary([{ id: '1', name: 'lint', status: 'failed' }, { id: '2', name: 'build', status: 'pending' }])).toMatchObject({ tone: 'danger', text: 'lint · 1 pending' })
    expect(reviewChecksSummary([{ id: '1', name: 'a', status: 'failed' }, { id: '2', name: 'b', status: 'failed' }, { id: '3', name: 'c', status: 'failed' }, { id: '4', name: 'd', status: 'failed' }])?.text).toBe('a, b, and 2 more')
    expect(reviewChecksSummary([{ id: '1', name: 'build', status: 'running' }])?.text).toBe('build pending')
    expect(reviewChecksSummary([{ id: '1', name: 'a', status: 'passed' }, { id: '2', name: 'b', status: 'skipped' }])?.text).toBe('1 passed · 1 skipped')
  })
  it('groups files into implementation and tests and splits paths', () => {
    const groups = reviewFileGroups([{ path: 'src/math.js', status: 'modified', additions: 2, deletions: 1, patch: '' }, { path: 'src/math.test.js', status: 'modified', additions: 3, deletions: 0, patch: '' }, { path: 'tests/e2e/a.ts', status: 'added', additions: 1, deletions: 0, patch: '' }])
    expect(groups.map(group => [group.id, group.files.length, group.additions, group.deletions])).toEqual([['implementation', 1, 2, 1], ['tests', 2, 4, 0]])
    expect(splitReviewPath('src/math.js')).toEqual({ name: 'math.js', directory: 'src' })
    expect(splitReviewPath('README.md')).toEqual({ name: 'README.md', directory: '' })
  })
  it('parses suggestions and code threads', () => {
    expect(splitSuggestionBody('Try this\n\n```suggestion\nconst a = 1;\n```\nthanks')).toEqual([{ kind: 'text', text: 'Try this' }, { kind: 'suggestion', lines: ['const a = 1;'] }, { kind: 'text', text: 'thanks' }])
    const threads = reviewCodeThreads({ ...base, events: [
      { id: 'a', type: 'commented', body: 'x', path: 'f', line: 2, actor: teammate, createdAt: '' },
      { id: 'b', type: 'commented', body: 'y', path: 'f', line: 2, parentId: 'a', actor: viewer, createdAt: '' },
      { id: 'c', type: 'commented', body: 'z', path: 'f', line: 5, resolved: true, actor: teammate, createdAt: '' },
      { id: 'd', type: 'commented', body: 'general', actor: teammate, createdAt: '' },
    ] })
    expect(threads.get('f:2')?.map(thread => [thread.root.id, thread.replies.map(reply => reply.id)])).toEqual([['a', ['b']]])
    expect(threads.get('f:5')?.[0].resolved).toBe(true)
    expect(threads.size).toBe(2)
  })
  it('resolves reference review shortcuts, including sequences', () => {
    expect(resolveReviewShortcut(key('r'), null)).toEqual({ prefix: 'r' })
    expect(resolveReviewShortcut(key('s'), 'r')).toEqual({ handler: 'submitReview', prefix: null })
    expect(resolveReviewShortcut(key('a'), 'r')).toEqual({ handler: 'approve', prefix: null })
    expect(resolveReviewShortcut(key('g'), 'o')).toEqual({ handler: 'openInProvider', prefix: null })
    expect(resolveReviewShortcut(key('.'), null)).toEqual({ handler: 'toggleReviewLayout', prefix: null })
    expect(resolveReviewShortcut(key('t'), null)).toEqual({ handler: 'focusFileFilter', prefix: null })
    expect(resolveReviewShortcut(key('V', { shiftKey: true }), null)).toEqual({ handler: 'openDisplayOptions', prefix: null })
    expect(resolveReviewShortcut(key('R', { shiftKey: true }), null)).toEqual({ handler: 'selectReviewers', prefix: null })
    expect(resolveReviewShortcut(key('t', { altKey: true, shiftKey: true }), null)).toEqual({ handler: 'toggleAllFiles', prefix: null })
    expect(resolveReviewShortcut(key('a', { altKey: true }), null)).toEqual({ handler: 'markAllFilesReviewed', prefix: null })
    expect(resolveReviewShortcut(key('s', { metaKey: true }), 'r')).toEqual({ prefix: null })
  })
})

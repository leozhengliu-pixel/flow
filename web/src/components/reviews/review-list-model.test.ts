import { describe, expect, it } from 'vitest'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { CodeReview } from '@/types/flow'
import { changeReviewGrouping, compareReviews, defaultReviewDisplay, groupReviews, matchesReviewFilters, parseReviewDisplay, parseReviewFilters, reviewBaseItems } from './review-list-model'

function item(id: string, changes: Partial<CodeReview> = {}): CodeReview {
  return { id, slugId: id, provider: 'github', externalId: id, number: 1, title: id, description: '', status: 'open', repositoryOwner: 'org', repositoryName: 'repo', url: '', author: teammate, reviewerIds: [viewer.id], teamReviewers: [], issueIds: [], baseBranch: 'main', headBranch: 'feature', branchState: 'upToDate', additions: 0, deletions: 0, commitCount: 1, checks: [], files: [], events: [], favorite: false, draft: false, quickToReview: false, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-02T00:00:00Z', ...changes }
}
describe('review list contracts', () => {
  it('uses separate view defaults and switches away from importance outside Focus', () => {
    expect(defaultReviewDisplay('for-you')).toMatchObject({ grouping: 'focus', ordering: 'importance', closed: 'day', properties: ['opened', 'quick'] })
    expect(defaultReviewDisplay('created')).toMatchObject({ grouping: 'status', ordering: 'opened', closed: 'week' })
    const next = changeReviewGrouping(defaultReviewDisplay('for-you'), 'author')
    expect(next).toMatchObject({ grouping: 'author', ordering: 'opened', descending: true })
    expect(changeReviewGrouping(next, 'focus')).toMatchObject({ ordering: 'importance', descending: false })
  })
  it('includes own and direct reviews and only includes the viewer team requests when enabled', () => {
    const data = makeBootstrap({ reviews: [item('direct'), item('own', { author: viewer, reviewerIds: [] }), item('team', { reviewerIds: [], teamReviewers: ['team-1'] }), item('other', { reviewerIds: [], teamReviewers: ['other-team'] })], teamMembers: [{ teamId: 'team-1', userId: viewer.id, role: 'member', joinedAt: '' }] })
    const defaults = defaultReviewDisplay('for-you')
    expect(reviewBaseItems(data, 'for-you', defaults).map(r => r.id)).toEqual(['direct', 'own', 'team'])
    expect(reviewBaseItems(data, 'for-you', { ...defaults, showTeams: false }).map(r => r.id)).toEqual(['direct', 'own'])
    expect(reviewBaseItems(data, 'created', defaultReviewDisplay('created')).map(r => r.id)).toEqual(['own'])
  })
  it('filters closed reviews by closure date instead of later updates and keeps open reviews', () => {
    const now = Date.parse('2026-09-10T12:00:00Z'), display = defaultReviewDisplay('for-you')
    const data = makeBootstrap({ reviews: [item('open'), item('recent', { status: 'closed', closedAt: '2026-09-10T00:00:00Z' }), item('old', { status: 'merged', mergedAt: '2026-08-01T00:00:00Z', updatedAt: '2026-09-10T10:00:00Z' }), item('draft', { draft: true })] })
    expect(reviewBaseItems(data, 'for-you', { ...display, showDrafts: false }, now).map(r => r.id)).toEqual(['open', 'recent'])
    expect(reviewBaseItems(data, 'for-you', { ...display, closed: 'none', showDrafts: false }, now).map(r => r.id)).toEqual(['open'])
    expect(reviewBaseItems(data, 'for-you', { ...display, closed: 'all' }, now)).toHaveLength(4)
  })
  it('supports multiple values, exclusions, false booleans and draft status', () => {
    expect(matchesReviewFilters(item('a'), [{ id: 'status', fieldLabel: 'Status', operator: 'is', values: [{ id: 'open', label: 'Open' }, { id: 'approved', label: 'Approved' }] }])).toBe(true)
    expect(matchesReviewFilters(item('a', { draft: true }), [{ id: 'status', fieldLabel: 'Status', operator: 'isNot', values: [{ id: 'draft', label: 'Draft' }] }])).toBe(false)
    expect(matchesReviewFilters(item('a', { issueIds: ['issue'] }), [{ id: 'missingIssue', fieldLabel: 'Missing issue', operator: 'is', values: [{ id: 'false', label: 'No' }] }])).toBe(true)
  })
  it('sorts importance using review state, supports both date directions and has stable ties', () => {
    const reviews = [item('new', { updatedAt: '2026-09-10T00:00:00Z' }), item('approved', { status: 'approved' }), item('draft', { draft: true })]
    expect([...reviews].sort((a, b) => compareReviews(a, b, defaultReviewDisplay('for-you'))).map(r => r.id)).toEqual(['approved', 'new', 'draft'])
    const display = { ...defaultReviewDisplay('created'), ordering: 'updated' as const, descending: true }
    expect([...reviews].sort((a, b) => compareReviews(a, b, display))[0].id).toBe('new')
    expect([...reviews].sort((a, b) => compareReviews(a, b, { ...display, descending: false }))[0].id).toBe('approved')
  })
  it('groups independently by repository, status, author and focus', () => {
    const reviews = [item('a'), item('b', { author: viewer, status: 'approved', repositoryName: 'other' })]
    expect(groupReviews(reviews, defaultReviewDisplay('for-you'), viewer.id).map(g => g.id)).toEqual(['ready', 'requested'])
    expect(groupReviews(reviews, { ...defaultReviewDisplay('created'), grouping: 'author' }, viewer.id).map(g => g.id)).toEqual([viewer.id, teammate.id])
    expect(groupReviews(reviews, { ...defaultReviewDisplay('created'), grouping: 'repository' }, viewer.id)).toHaveLength(2)
    expect(groupReviews(reviews, { ...defaultReviewDisplay('created'), grouping: 'none' }, viewer.id)[0].items).toHaveLength(2)
  })
  it('validates persisted filters and preferences', () => {
    expect(parseReviewFilters('null')).toEqual([])
    expect(parseReviewFilters('[{"id":"__proto__","values":[]}]')).toEqual([])
    expect(parseReviewDisplay('{"grouping":"focus","ordering":"importance"}', 'created')).toMatchObject({ grouping: 'status', ordering: 'opened' })
    expect(parseReviewDisplay('{"properties":["id","nonexistent"]}', 'for-you').properties).toEqual(['id'])
  })
})

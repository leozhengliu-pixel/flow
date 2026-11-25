import { describe, expect, it } from 'vitest'
import { viewerCanEditIssue } from './issue-edit-access'

const issue = { creator: { id: 'creator' }, team: { id: 'team-a' }, permissions: undefined } as never
const data = (viewerRole: string, teamMembers: { teamId: string; userId: string }[]) => ({ viewer: { id: 'me' }, viewerRole, teamMembers }) as never

describe('viewerCanEditIssue', () => {
  it('lets admins, owners, the creator and team members edit', () => {
    expect(viewerCanEditIssue(issue, data('admin', [{ teamId: 'x', userId: 'someone' }]))).toBe(true)
    expect(viewerCanEditIssue(issue, data('owner', [{ teamId: 'x', userId: 'someone' }]))).toBe(true)
    expect(viewerCanEditIssue({ ...(issue as object), creator: { id: 'me' } } as never, data('member', [{ teamId: 'x', userId: 'someone' }]))).toBe(true)
    expect(viewerCanEditIssue(issue, data('guest', [{ teamId: 'team-a', userId: 'me' }]))).toBe(true)
  })
  it('keeps a workspace member outside the team to reading', () => {
    expect(viewerCanEditIssue(issue, data('member', [{ teamId: 'team-a', userId: 'someone' }]))).toBe(false)
  })
  it('honours explicit shares and stays open when team membership is unknown', () => {
    const shared = { ...(issue as object), permissions: [{ subjectType: 'user', subjectId: 'me', role: 'editor' }] } as never
    expect(viewerCanEditIssue(shared, data('member', [{ teamId: 'team-a', userId: 'someone' }]))).toBe(true)
    const viewerOnly = { ...(issue as object), permissions: [{ subjectType: 'user', subjectId: 'me', role: 'viewer' }] } as never
    expect(viewerCanEditIssue(viewerOnly, data('member', [{ teamId: 'team-a', userId: 'someone' }]))).toBe(false)
    expect(viewerCanEditIssue(issue, data('member', []))).toBe(true)
  })
})

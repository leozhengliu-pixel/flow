import { describe, expect, it } from 'vitest'

import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { DocumentPermission, FlowDocument } from '@/types/flow'
import { canCommentOnDocument, canEditDocument, documentViewerRole } from './document-role'

const base = { creator: teammate, teamIds: [] as string[], permissions: [] as DocumentPermission[] } as Pick<FlowDocument, 'creator' | 'teamIds' | 'permissions'>
const grant = (subjectType: string, subjectId: string, role: string) => ({ id: `${subjectType}:${subjectId}`, documentId: 'd', subjectType, subjectId, role, createdAt: '', updatedAt: '' }) as DocumentPermission

describe('documentViewerRole', () => {
  const member = makeBootstrap({ viewerRole: 'member', teamMembers: [{ teamId: 'team-1', userId: viewer.id, role: 'member' }] as never })
  it('mirrors the server: admins and creators own, grants decide, team members can read', () => {
    expect(documentViewerRole(makeBootstrap({ viewerRole: 'admin' }), base)).toBe('owner')
    expect(documentViewerRole(member, { ...base, creator: viewer })).toBe('owner')
    expect(documentViewerRole(member, { ...base, permissions: [grant('workspace', '', 'editor')] })).toBe('editor')
    expect(documentViewerRole(member, { ...base, permissions: [grant('user', teammate.id, 'owner'), grant('user', viewer.id, 'commenter')] })).toBe('commenter')
    expect(documentViewerRole(member, { ...base, teamIds: ['team-1'], permissions: [grant('team', 'team-1', 'editor')] })).toBe('editor')
    expect(documentViewerRole(member, { ...base, teamIds: ['team-1'], permissions: [grant('user', teammate.id, 'owner')] })).toBe('viewer')
    expect(documentViewerRole(member, { ...base, permissions: [grant('user', teammate.id, 'owner')] })).toBe('none')
    expect(documentViewerRole(member, base)).toBe('viewer')
  })
  it('maps roles to abilities', () => {
    expect([canEditDocument('editor'), canEditDocument('commenter'), canCommentOnDocument('commenter'), canCommentOnDocument('viewer')]).toEqual([true, false, true, false])
  })
})

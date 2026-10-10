import { describe, expect, it, vi } from 'vitest'

import type { BootstrapData, FlowDocument, User } from '@/types/flow'
import { documentDisplayTitle, documentFileName, documentMarkdown, documentOwner, documentParent, downloadDocumentMarkdown } from './document-actions'

const user = (id: string): User => ({ id, name: id, displayName: id.toUpperCase(), email: `${id}@x.test` }) as User
const base = (over: Partial<FlowDocument> = {}): FlowDocument => ({
  id: 'd1', slugId: 'doc-abc', title: 'Plan', content: 'Body', creator: user('a'), projectIds: [], teamIds: [], subscriberIds: [], favorite: false,
  createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z', revisions: [], ...over,
}) as FlowDocument

describe('document actions helpers', () => {
  it('falls back to Untitled for an empty title', () => {
    expect(documentDisplayTitle(base({ title: '  ' }), value => `T:${value}`)).toBe('T:Untitled')
    expect(documentDisplayTitle(base(), value => value)).toBe('Plan')
  })
  it('derives the owner from the owner permission, with No owner support', () => {
    const users = [user('a'), user('b')]
    expect(documentOwner(base(), users)?.id).toBe('a')
    expect(documentOwner(base({ permissions: [{ id: 'p', documentId: 'd1', subjectType: 'user', subjectId: 'b', role: 'owner' } as never] }), users)?.id).toBe('b')
    expect(documentOwner(base({ permissions: [{ id: 'p', documentId: 'd1', subjectType: 'user', subjectId: 'b', role: 'editor' } as never] }), users)).toBeUndefined()
  })
  it('picks a single parent: issue, initiative, project, then team', () => {
    const data = { initiatives: [{ id: 'i1', resources: [{ id: 'r', documentId: 'd1' }] }], projects: [{ id: 'p1' }], teams: [{ id: 't1' }] } as unknown as BootstrapData
    expect(documentParent(data, base({ issueId: 'x' }))?.type).toBe('issue')
    expect(documentParent(data, base())?.type).toBe('initiative')
    expect(documentParent({ ...data, initiatives: [] }, base({ projectIds: ['p1'], teamIds: ['t1'] }))?.type).toBe('project')
    expect(documentParent({ ...data, initiatives: [] }, base({ teamIds: ['t1'] }))?.type).toBe('team')
    expect(documentParent({ ...data, initiatives: [] }, base())).toBeUndefined()
  })
  it('builds Markdown and file names', () => {
    expect(documentMarkdown(base())).toBe('# Plan\n\nBody\n')
    expect(documentMarkdown(base({ title: '', content: '' }))).toBe('# Untitled\n')
    expect(documentFileName(base({ title: 'a/b: c?' }), 'md')).toBe('a b c.md')
  })
  it('downloads Markdown client side', () => {
    const click = vi.fn()
    const create = vi.fn(() => 'blob:x')
    const revoke = vi.fn()
    Object.assign(URL, { createObjectURL: create, revokeObjectURL: revoke })
    const spy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(click)
    expect(downloadDocumentMarkdown(base())).toBe('Plan.md')
    expect(click).toHaveBeenCalled()
    expect(create).toHaveBeenCalled()
    spy.mockRestore()
  })
})

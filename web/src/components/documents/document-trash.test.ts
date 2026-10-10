import { describe, expect, it } from 'vitest'

import type { TrashEntry, User } from '@/types/flow'
import { findTrashedDocument } from './document-trash'

const deletedBy = { id: 'u1', name: 'u', displayName: 'U', email: 'u@x.test', active: true } as User
const entry = (overrides: Partial<TrashEntry> = {}): TrashEntry => ({
  id: 'trash_1', resourceType: 'document', resourceId: 'document_1', title: 'Gone', deletedBy, deletedAt: '2026-10-01T00:00:00Z', expiresAt: '2026-10-31T00:00:00Z',
  payload: { id: 'document_1', slugId: 'gone-abc123', title: 'Gone', content: 'Body', creator: deletedBy, teamIds: ['t1'], updatedAt: '2026-09-30T00:00:00Z' },
  ...overrides,
})

describe('findTrashedDocument', () => {
  it('finds a trashed document by slug or id and fills in missing fields', () => {
    for (const key of ['gone-abc123', 'document_1']) {
      const found = findTrashedDocument([entry()], key)
      expect(found?.entry.id).toBe('trash_1')
      expect(found?.document).toMatchObject({ id: 'document_1', slugId: 'gone-abc123', title: 'Gone', content: 'Body', teamIds: ['t1'], projectIds: [], subscriberIds: [], revisions: [], favorite: false, updatedAt: '2026-09-30T00:00:00Z' })
    }
  })

  it('accepts a JSON string payload and ignores other resource types or missing payloads', () => {
    const asString = entry({ payload: JSON.stringify({ id: 'document_1', slugId: 's' }) })
    expect(findTrashedDocument([asString], 's')?.document.slugId).toBe('s')
    expect(findTrashedDocument([entry({ resourceType: 'project' })], 'document_1')).toBeUndefined()
    expect(findTrashedDocument([entry({ payload: undefined })], 'document_1')).toBeUndefined()
    expect(findTrashedDocument([entry()], 'other')).toBeUndefined()
    expect(findTrashedDocument(undefined, 'x')).toBeUndefined()
    expect(findTrashedDocument([entry()], undefined)).toBeUndefined()
  })
})

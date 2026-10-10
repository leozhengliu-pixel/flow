import { describe, expect, it } from 'vitest'

import type { DocumentRevision, User } from '@/types/flow'
import { AUTHOR_BLOCK_CAP, authorRuns, deriveBlockAuthors, topLevelBlocks } from './document-authors'

const user = (id: string) => ({ id, name: id, displayName: id.toUpperCase(), email: `${id}@x.test`, active: true }) as User
const [ann, bob, cy] = [user('ann'), user('bob'), user('cy')]
const para = (text: string) => ({ type: 'paragraph', content: [{ type: 'text', text }] })
const doc = (...texts: string[]) => ({ type: 'doc', content: texts.map(para) })
const revision = (id: string, author: User, minutes: number, texts: string[]) => ({
  id, documentId: 'd', title: 'T', content: '', author, createdAt: new Date(Date.UTC(2026, 0, 1, 0, minutes)).toISOString(), contentData: doc(...texts),
}) as DocumentRevision

describe('deriveBlockAuthors', () => {
  it('attributes everything to the creator when nothing was ever revised', () => {
    expect(deriveBlockAuthors({ creator: ann, revisions: [] }, doc('a', 'b'), ann).map(item => item.id)).toEqual(['ann', 'ann'])
  })

  it('attributes new and changed blocks to the revision author and keeps the rest', () => {
    const revisions = [revision('r1', ann, 1, ['one', 'two']), revision('r2', bob, 2, ['one', 'two', 'three']), revision('r3', cy, 3, ['one', 'TWO', 'three'])]
    const authors = deriveBlockAuthors({ creator: ann, revisions }, doc('one', 'TWO', 'three'), cy)
    expect(authors.map(item => item.id)).toEqual(['ann', 'cy', 'bob'])
  })

  it('attributes blocks that differ from the newest revision to the last editor', () => {
    const revisions = [revision('r1', ann, 1, ['one'])]
    const authors = deriveBlockAuthors({ creator: ann, revisions }, doc('one', 'fresh'), bob)
    expect(authors.map(item => item.id)).toEqual(['ann', 'bob'])
  })

  it('is independent of the revision order and handles inserted blocks in the middle', () => {
    const revisions = [revision('r2', bob, 2, ['a', 'new', 'b']), revision('r1', ann, 1, ['a', 'b'])]
    const authors = deriveBlockAuthors({ creator: ann, revisions }, doc('a', 'new', 'b'), bob)
    expect(authors.map(item => item.id)).toEqual(['ann', 'bob', 'ann'])
  })

  it('caps the number of blocks it looks at', () => {
    const many = doc(...Array.from({ length: AUTHOR_BLOCK_CAP + 50 }, (_, index) => `p${index}`))
    expect(topLevelBlocks(many)).toHaveLength(AUTHOR_BLOCK_CAP)
    expect(deriveBlockAuthors({ creator: ann, revisions: [] }, many, ann)).toHaveLength(AUTHOR_BLOCK_CAP)
  })

  it('returns nothing for an empty document', () => {
    expect(deriveBlockAuthors({ creator: ann, revisions: [] }, { type: 'doc', content: [] }, ann)).toEqual([])
    expect(deriveBlockAuthors({ creator: ann, revisions: [] }, undefined, ann)).toEqual([])
  })
})

describe('authorRuns', () => {
  it('starts a run at the first block and wherever the author changes', () => {
    expect(authorRuns([ann, ann, bob, bob, ann]).map(run => [run.index, run.author.id])).toEqual([[0, 'ann'], [2, 'bob'], [4, 'ann']])
    expect(authorRuns([])).toEqual([])
  })
})

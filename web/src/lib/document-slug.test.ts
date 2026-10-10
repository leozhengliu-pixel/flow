import { describe, expect, it } from 'vitest'

import { documentSlugSuffix, findDocumentBySlugSuffix } from './document-slug'

describe('document slug suffix', () => {
  it('reads the trailing hex segment', () => {
    expect(documentSlugSuffix('untitled-1a2b3c4d5e6f')).toBe('1a2b3c4d5e6f')
    expect(documentSlugSuffix('quarterly-plan-ab12cd')).toBe('ab12cd')
    expect(documentSlugSuffix('roadmap')).toBeUndefined()
    expect(documentSlugSuffix('roadmap-notes')).toBeUndefined()
  })

  it('resolves a link made before a rename through the stable suffix', () => {
    const documents = [{ slugId: 'launch-plan-1a2b3c4d5e6f' }, { slugId: 'other-ffeeddccbbaa' }]
    expect(findDocumentBySlugSuffix(documents, 'untitled-1a2b3c4d5e6f')).toBe(documents[0])
    expect(findDocumentBySlugSuffix(documents, 'new-document-ffeeddccbbaa')).toBe(documents[1])
    expect(findDocumentBySlugSuffix(documents, 'untitled-000000000000')).toBeUndefined()
    expect(findDocumentBySlugSuffix(documents, 'untitled')).toBeUndefined()
  })

  it('prefers a document that lists the slug as a previous one', () => {
    const documents = [{ slugId: 'a-111111' }, { slugId: 'b-222222', previousSlugIds: ['legacy-slug'] }]
    expect(findDocumentBySlugSuffix(documents, 'legacy-slug')).toBe(documents[1])
  })
})

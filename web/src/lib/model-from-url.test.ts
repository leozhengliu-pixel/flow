import { describe, expect, it } from 'vitest'
import { extractModelsFromMarkdown, getModelRefFromHref } from './model-from-url'

describe('getModelRefFromHref (LS-0738)', () => {
  it('parses Linear-style mention hrefs', () => {
    expect(getModelRefFromHref('Issue:abc-123')).toEqual({ type: 'issue', id: 'abc-123' })
    expect(getModelRefFromHref('Project:proj-1?href=%2Facme%2Fproject%2Fx')).toEqual({
      type: 'project',
      id: 'proj-1',
    })
  })

  it('parses Flow app paths', () => {
    expect(getModelRefFromHref('https://flow.local/acme/issue/ENG-12/title')).toEqual({
      type: 'issue',
      id: 'ENG-12',
    })
    expect(getModelRefFromHref('/acme/project/platform-migration/overview')).toEqual({
      type: 'project',
      id: 'platform-migration',
    })
    expect(getModelRefFromHref('/acme/document/spec')).toEqual({ type: 'document', id: 'spec' })
    expect(getModelRefFromHref('/acme/initiative/north-star/overview')).toEqual({
      type: 'initiative',
      id: 'north-star',
    })
    expect(getModelRefFromHref('/acme/profiles/user-9')).toEqual({ type: 'user', id: 'user-9' })
    expect(getModelRefFromHref('/acme/team/ENG/all')).toEqual({ type: 'team', id: 'ENG' })
  })

  it('returns null for unrelated hrefs', () => {
    expect(getModelRefFromHref('https://example.com/docs')).toBeNull()
    expect(getModelRefFromHref('streamdown:incomplete-link')).toBeNull()
  })
})

describe('extractModelsFromMarkdown (LS-0738)', () => {
  it('extracts unique refs from links and bare mention hrefs', () => {
    const md = [
      'See [ENG-12](https://app/acme/issue/ENG-12/ship) and [proj](/acme/project/platform/overview).',
      'Also Issue:deadbeef and https://app/acme/profiles/user-1',
    ].join('\n')
    expect(extractModelsFromMarkdown(md)).toEqual([
      { type: 'issue', id: 'ENG-12' },
      { type: 'project', id: 'platform' },
      { type: 'user', id: 'user-1' },
      { type: 'issue', id: 'deadbeef' },
    ])
  })

  it('returns empty for blank markdown', () => {
    expect(extractModelsFromMarkdown('')).toEqual([])
    expect(extractModelsFromMarkdown(undefined)).toEqual([])
  })
})

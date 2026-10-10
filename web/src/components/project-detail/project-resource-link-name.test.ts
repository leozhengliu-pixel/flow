import { describe, expect, it } from 'vitest'
import { hostOf, providerFor, resourceChipTitle, resourceDisplayTitle, resourceLinkName } from './project-resource-link-name'

describe('project resource links', () => {
  it('names untitled links after their site, like Linear', () => {
    expect(resourceLinkName('https://github.com/leozhengliu-pixel/flow')).toBe('GitHub')
    expect(resourceLinkName('https://www.example.com/spec')).toBe('Example')
    expect(resourceLinkName('https://acme.atlassian.net/browse/X-1')).toBe('Jira')
    expect(resourceLinkName('https://docs.google.com/document/d/1')).toBe('Google Docs')
    expect(resourceLinkName('not a url')).toBe('not a url')
  })

  it('keeps explicit titles and replaces the legacy URL-as-title', () => {
    expect(resourceDisplayTitle({ type: 'link', title: 'Spec', url: 'https://example.com/spec' })).toBe('Spec')
    expect(resourceDisplayTitle({ type: 'link', title: 'https://example.com/spec', url: 'https://example.com/spec' })).toBe('Example')
    expect(resourceDisplayTitle({ type: 'document', title: 'Notes', url: '/w/document/notes' })).toBe('Notes')
  })

  it('picks brand icons only for known services', () => {
    expect(providerFor(hostOf('https://github.com/a/b'))).toBe('github')
    expect(providerFor(hostOf('https://team.slack.com/archives/C1'))).toBe('slack')
    expect(providerFor(hostOf('https://example.com'))).toBeUndefined()
  })
})

describe('resourceChipTitle', () => {
  const t = (source: string) => (source === 'Untitled' ? '无标题' : source)
  const resource = (over: Partial<{ type: string; title: string; url: string }>) => ({ type: 'document', title: '', url: '/ws/document/a', ...over })

  it('prefers the live document title over the stored resource title', () => {
    expect(resourceChipTitle(resource({ title: 'Old name' }), { title: 'Live name' }, t)).toBe('Live name')
  })

  it('falls back to Untitled for an untitled document, whatever the stored title says', () => {
    expect(resourceChipTitle(resource({ title: 'Untitled document' }), { title: '  ' }, t)).toBe('无标题')
    expect(resourceChipTitle(resource({ title: 'Untitled document' }), undefined, t)).toBe('无标题')
    expect(resourceChipTitle(resource({ title: '' }), undefined, t)).toBe('无标题')
  })

  it('uses the stored title when the document is not loaded and links keep their site name', () => {
    expect(resourceChipTitle(resource({ title: 'Spec' }), undefined, t)).toBe('Spec')
    expect(resourceChipTitle({ type: 'link', title: '', url: 'https://github.com/a/b' }, undefined, t)).toBe('GitHub')
  })
})

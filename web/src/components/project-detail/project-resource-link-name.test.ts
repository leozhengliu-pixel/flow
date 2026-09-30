import { describe, expect, it } from 'vitest'
import { hostOf, providerFor, resourceDisplayTitle, resourceLinkName } from './project-resource-link-name'

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

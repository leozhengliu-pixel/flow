import { describe, expect, it } from 'vitest'
import { defaultLinkTitle, linkBrand } from './link-brand'

describe('link brands', () => {
  it('recognises GitHub, Slack and Figma links', () => {
    expect(linkBrand('https://github.com/facebook/react/issues/1')).toBe('github')
    expect(linkBrand('https://acme.slack.com/archives/C1/p1')).toBe('slack')
    expect(linkBrand('https://www.figma.com/design/x')).toBe('figma')
    expect(linkBrand('https://example.com')).toBeNull()
    expect(linkBrand('not a url')).toBeNull()
  })
  it('names a link like the server does', () => {
    expect(defaultLinkTitle('https://github.com/facebook/react/pull/42/files')).toBe('facebook/react#42')
    expect(defaultLinkTitle('https://github.com/facebook/react')).toBe('github.com')
    expect(defaultLinkTitle('https://www.figma.com/design/x/y')).toBe('Figma')
    expect(defaultLinkTitle('https://acme.slack.com/archives/C1/p1')).toBe('Message from Slack')
    expect(defaultLinkTitle('https://example.com/docs')).toBe('example.com')
  })
})

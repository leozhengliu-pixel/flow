import { describe, expect, it } from 'vitest'
import { figmaAuthUrl, parseFigmaCallbackState } from './figma-auth'

describe('figmaAuthUrl', () => {
  it('builds Figma authorize URL with required scopes', () => {
    const url = figmaAuthUrl({
      clientId: 'figma-client',
      redirectUri: 'http://localhost:5173/connect/figma/callback',
      state: { key: 'abc', urlKey: 'acme' },
    })
    const parsed = new URL(url)
    expect(parsed.origin + parsed.pathname).toBe('https://www.figma.com/oauth')
    expect(parsed.searchParams.get('client_id')).toBe('figma-client')
    expect(parsed.searchParams.get('scope')).toBe('file_content:read file_metadata:read')
    expect(JSON.parse(parsed.searchParams.get('state')!)).toEqual({ key: 'abc', urlKey: 'acme' })
  })
})

describe('parseFigmaCallbackState', () => {
  it('parses JSON state and ignores opaque server state', () => {
    expect(parseFigmaCallbackState('?state=' + encodeURIComponent(JSON.stringify({ key: 'k', urlKey: 'w' })))).toEqual({
      key: 'k',
      urlKey: 'w',
    })
    expect(parseFigmaCallbackState('?state=flow_oauth_state_abc')).toBeNull()
  })
})

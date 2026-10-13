import { createHash } from 'node:crypto'
import { describe, expect, it } from 'vitest'
import { FlowClient, FlowConfigurationError, FlowOAuthClient, FlowOAuthError, createPkcePair } from '../src/index.js'
import { API_URL, json, mockFetch } from './helpers.js'

const tokenBody = { access_token: 'flow_oauth_abc', token_type: 'Bearer', expires_in: 3600, scope: 'read write', refresh_token: 'flow_refresh_def', actor: 'user' }

function oauth(responder: Parameters<typeof mockFetch>[0], options: Record<string, unknown> = {}) {
  const mock = mockFetch(responder)
  return { ...mock, oauth: new FlowOAuthClient({ apiUrl: API_URL, clientId: 'flow_client_1', clientSecret: 's3cret', redirectUri: 'https://app.example.com/cb', fetch: mock.fetch, ...options }) }
}

describe('PKCE', () => {
  it('creates a verifier and its S256 challenge', async () => {
    const pair = await createPkcePair()
    expect(pair.codeVerifier).toMatch(/^[A-Za-z0-9_-]{43}$/)
    expect(pair.codeChallenge).toBe(createHash('sha256').update(pair.codeVerifier).digest('base64url'))
    expect(pair.codeChallengeMethod).toBe('S256')
    expect((await createPkcePair()).codeVerifier).not.toBe(pair.codeVerifier)
  })

  it('works without a global crypto (Node 18)', async () => {
    const original = Object.getOwnPropertyDescriptor(globalThis, 'crypto')
    Object.defineProperty(globalThis, 'crypto', { value: undefined, configurable: true })
    try {
      const pair = await createPkcePair()
      expect(pair.codeChallenge).toBe(createHash('sha256').update(pair.codeVerifier).digest('base64url'))
    } finally {
      if (original) Object.defineProperty(globalThis, 'crypto', original)
    }
  })
})

describe('authorization URL', () => {
  it('builds the authorize URL with PKCE', async () => {
    const { oauth: client } = oauth(() => json({}))
    const { url, state, codeVerifier, codeChallenge } = await client.startAuthorization({ scope: ['read', 'write', 'issues:create'], actor: 'app', prompt: 'consent' })
    const parsed = new URL(url)
    expect(parsed.origin + parsed.pathname).toBe(`${API_URL}/oauth/authorize`)
    expect(Object.fromEntries(parsed.searchParams)).toEqual({
      response_type: 'code',
      client_id: 'flow_client_1',
      redirect_uri: 'https://app.example.com/cb',
      scope: 'read,write,issues:create',
      state,
      actor: 'app',
      prompt: 'consent',
      code_challenge: codeChallenge,
      code_challenge_method: 'S256',
    })
    expect(state.length).toBeGreaterThanOrEqual(16)
    expect(codeChallenge).toBe(createHash('sha256').update(codeVerifier).digest('base64url'))
  })

  it('omits optional parameters', () => {
    const { oauth: client } = oauth(() => json({}))
    const url = new URL(client.authorizationUrl({ state: 'xyz' }))
    expect([...url.searchParams.keys()].sort()).toEqual(['client_id', 'redirect_uri', 'response_type', 'state'])
  })

  it('needs a redirect URI', () => {
    const { oauth: client } = oauth(() => json({}), { redirectUri: undefined })
    expect(() => client.authorizationUrl()).toThrow(FlowConfigurationError)
  })

  it('parses the callback and checks state', () => {
    const { oauth: client } = oauth(() => json({}))
    expect(client.parseCallback('https://app.example.com/cb?code=flow_code_1&state=abc', 'abc')).toEqual({ code: 'flow_code_1', state: 'abc' })
    expect(() => client.parseCallback('/cb?code=c&state=evil', 'abc')).toThrow(/state/)
    expect(() => client.parseCallback('/cb?error=access_denied&state=abc', 'abc')).toThrow(FlowOAuthError)
    expect(() => client.parseCallback(new URLSearchParams('state=abc'), 'abc')).toThrow(/authorization code/)
    try {
      client.parseCallback('/cb?error=access_denied')
    } catch (error) {
      expect((error as FlowOAuthError).oauthError).toBe('access_denied')
    }
  })
})

describe('token endpoint', () => {
  it('exchanges a code with client_secret_basic', async () => {
    const { oauth: client, calls } = oauth(() => json(tokenBody))
    const before = Date.now()
    const tokens = await client.exchangeCode({ code: 'flow_code_1', codeVerifier: 'verifier' })
    const call = calls[0]
    expect(call.method).toBe('POST')
    expect(call.url).toBe(`${API_URL}/oauth/token`)
    expect(call.headers.get('content-type')).toBe('application/x-www-form-urlencoded')
    expect(call.headers.get('authorization')).toBe(`Basic ${Buffer.from('flow_client_1:s3cret').toString('base64')}`)
    const form = new URLSearchParams(call.text)
    expect(Object.fromEntries(form)).toEqual({ grant_type: 'authorization_code', code: 'flow_code_1', redirect_uri: 'https://app.example.com/cb', code_verifier: 'verifier' })
    expect(tokens).toMatchObject({ accessToken: 'flow_oauth_abc', tokenType: 'Bearer', expiresIn: 3600, scope: ['read', 'write'], refreshToken: 'flow_refresh_def', actor: 'user' })
    expect(tokens.expiresAt!.getTime()).toBeGreaterThanOrEqual(before + 3_600_000)
    expect(tokens.raw.access_token).toBe('flow_oauth_abc')
  })

  it('supports client_secret_post', async () => {
    const { oauth: client, calls } = oauth(() => json(tokenBody), { clientAuthentication: 'post' })
    await client.exchangeCode({ code: 'c' })
    expect(calls[0].headers.get('authorization')).toBeNull()
    expect(Object.fromEntries(new URLSearchParams(calls[0].text))).toMatchObject({ client_id: 'flow_client_1', client_secret: 's3cret' })
  })

  it('public clients send only client_id', async () => {
    const { oauth: client, calls } = oauth(() => json(tokenBody), { clientSecret: undefined })
    await client.exchangeCode({ code: 'c', codeVerifier: 'v' })
    await client.refreshToken('flow_refresh_def')
    for (const call of calls) {
      expect(call.headers.get('authorization')).toBeNull()
      const form = Object.fromEntries(new URLSearchParams(call.text))
      expect(form.client_id).toBe('flow_client_1')
      expect(form.client_secret).toBeUndefined()
    }
  })

  it('refreshes tokens and returns the rotated refresh token', async () => {
    const { oauth: client, calls } = oauth(() => json({ ...tokenBody, refresh_token: 'flow_refresh_new' }))
    const tokens = await client.refreshToken('flow_refresh_def')
    expect(Object.fromEntries(new URLSearchParams(calls[0].text))).toEqual({ grant_type: 'refresh_token', refresh_token: 'flow_refresh_def' })
    expect(tokens.refreshToken).toBe('flow_refresh_new')
  })

  it('requests client credentials with space separated scopes', async () => {
    const { oauth: client, calls } = oauth(() => json({ access_token: 'flow_oauth_app', token_type: 'Bearer', expires_in: 2592000, scope: 'read write', actor: 'app' }))
    const tokens = await client.clientCredentials({ scope: ['read', 'write'] })
    expect(Object.fromEntries(new URLSearchParams(calls[0].text))).toEqual({ grant_type: 'client_credentials', scope: 'read write' })
    expect(tokens.actor).toBe('app')
    expect(tokens.refreshToken).toBeUndefined()
  })

  it('maps RFC 6749 errors', async () => {
    const { oauth: client } = oauth(() => json({ error: 'invalid_grant', error_description: 'The code has expired' }, { status: 400 }))
    const error = await client.exchangeCode({ code: 'old' }).catch(e => e)
    expect(error).toBeInstanceOf(FlowOAuthError)
    expect(error).toMatchObject({ type: 'OAUTH_ERROR', code: 'invalid_grant', oauthError: 'invalid_grant', errorDescription: 'The code has expired', status: 400 })
    expect(error.message).toContain('The code has expired')

    const denied = oauth(() => json({ error: 'invalid_client' }, { status: 401 }))
    await expect(denied.oauth.clientCredentials()).rejects.toMatchObject({ code: 'invalid_client', status: 401 })
  })

  it('wraps network failures', async () => {
    const client = new FlowOAuthClient({ apiUrl: API_URL, clientId: 'c', fetch: async () => { throw new TypeError('fetch failed') } })
    await expect(client.refreshToken('r')).rejects.toMatchObject({ type: 'NETWORK_ERROR' })
  })

  it('needs a secret for basic authentication', async () => {
    const { oauth: client } = oauth(() => json(tokenBody), { clientSecret: undefined, clientAuthentication: 'basic' })
    await expect(client.refreshToken('r')).rejects.toBeInstanceOf(FlowConfigurationError)
  })
})

describe('revocation and introspection', () => {
  it('revokes with a token type hint', async () => {
    const { oauth: client, calls } = oauth(() => new Response('{}', { status: 200 }))
    await client.revokeToken('flow_refresh_def', 'refresh_token')
    expect(calls[0].url).toBe(`${API_URL}/oauth/revoke`)
    expect(Object.fromEntries(new URLSearchParams(calls[0].text))).toEqual({ token: 'flow_refresh_def', token_type_hint: 'refresh_token' })
    expect(calls[0].headers.get('authorization')).toMatch(/^Basic /)
  })

  it('maps revoke errors', async () => {
    const { oauth: client } = oauth(() => json({ error: 'unsupported_token_type' }, { status: 400 }))
    await expect(client.revokeToken('t', 'access_token')).rejects.toMatchObject({ code: 'unsupported_token_type' })
  })

  it('introspects a token', async () => {
    const { oauth: client, calls } = oauth(() => json({ active: true, scope: 'read write', client_id: 'flow_client_1', actor: 'user', workspace: 'acme' }))
    const result = await client.introspectToken('flow_oauth_abc')
    expect(result).toMatchObject({ active: true, workspace: 'acme' })
    expect(calls[0].url).toBe(`${API_URL}/oauth/introspect`)
  })
})

describe('using tokens with the API client', () => {
  it('refreshes on demand through an access token function', async () => {
    let refreshes = 0
    const authMock = mockFetch(() => json({ ...tokenBody, access_token: `flow_oauth_${++refreshes}` }))
    const flowOAuth = new FlowOAuthClient({ apiUrl: API_URL, clientId: 'c', clientSecret: 's', fetch: authMock.fetch })
    const api = mockFetch(() => json({ user: { id: 'u' } }))
    const client = new FlowClient({ accessToken: async () => (await flowOAuth.refreshToken('r')).accessToken, apiUrl: API_URL, fetch: api.fetch })
    await client.viewer
    expect(api.calls[0].headers.get('authorization')).toBe('Bearer flow_oauth_1')
  })
})

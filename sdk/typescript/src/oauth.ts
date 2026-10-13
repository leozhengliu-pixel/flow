import { FlowConfigurationError, FlowOAuthError, NetworkFlowError, TimeoutFlowError, AbortFlowError, UnknownFlowError } from './errors.js'
import { randomBase64Url, sha256Base64Url } from './crypto.js'
import type { FetchLike } from './http.js'

export type OAuthActor = 'user' | 'app'

/** How the client authenticates at the token endpoint. */
export type ClientAuthentication = 'basic' | 'post' | 'none'

export interface FlowOAuthClientOptions {
  /** Origin of your Flow deployment, for example `https://flow.example.com`. Defaults to `FLOW_BASE_URL`. */
  apiUrl?: string
  clientId: string
  /** Required for confidential clients; omit for public (PKCE) clients. */
  clientSecret?: string
  /** Registered redirect URI, used as the default for the authorization request and the code exchange. */
  redirectUri?: string
  /**
   * `basic` (HTTP Basic, the default when a secret is set), `post`
   * (`client_secret` in the form) or `none` (`client_id` only; public clients).
   */
  clientAuthentication?: ClientAuthentication
  fetch?: FetchLike
  /** Timeout in milliseconds for each request. Default 30000. */
  timeout?: number
}

export interface PkcePair {
  codeVerifier: string
  codeChallenge: string
  codeChallengeMethod: 'S256'
}

export interface AuthorizationUrlOptions {
  /** Scopes such as `['read', 'write']`; `read` is always granted. */
  scope?: string | string[]
  /** Anti-CSRF value returned unchanged on the callback; reject a callback whose state differs. */
  state?: string
  /** `user` (default) acts as the person who authorizes, `app` installs the application for the workspace. */
  actor?: OAuthActor
  /** `consent` always shows the consent screen (lets people connect another workspace). */
  prompt?: 'consent'
  redirectUri?: string
  /** From `createPkcePair()`. Required for public clients, optional otherwise. */
  codeChallenge?: string
}

export interface StartedAuthorization extends PkcePair {
  /** Send the browser here. */
  url: string
  /** Keep with the session; compare with the callback's `state`. */
  state: string
}

export interface OAuthTokens {
  accessToken: string
  tokenType: string
  /** Lifetime in seconds. Access tokens last one hour. */
  expiresIn?: number
  /** When the access token expires, computed from `expiresIn` when the response arrived. */
  expiresAt?: Date
  scope: string[]
  /** Refresh tokens rotate: store the new one. Absent for client credentials. */
  refreshToken?: string
  actor?: OAuthActor
  /** The response as sent by the server. */
  raw: Record<string, unknown>
}

export interface TokenIntrospection {
  active: boolean
  scope?: string
  client_id?: string
  token_type?: string
  exp?: number
  iat?: number
  sub?: string
  actor?: OAuthActor
  workspace?: string
  app_user_id?: string
  [field: string]: unknown
}

export interface ExchangeCodeOptions {
  code: string
  redirectUri?: string
  /** The PKCE verifier from `createPkcePair()`. */
  codeVerifier?: string
}

function scopeList(scope: string | string[] | undefined): string[] {
  if (scope === undefined) return []
  return (Array.isArray(scope) ? scope : scope.split(/[\s,]+/)).filter(Boolean)
}

function basicAuth(clientId: string, clientSecret: string): string {
  const bytes = new TextEncoder().encode(`${encodeURIComponent(clientId)}:${encodeURIComponent(clientSecret)}`)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return `Basic ${btoa(binary)}`
}

/** Creates a PKCE verifier and its S256 challenge (RFC 7636). */
export async function createPkcePair(): Promise<PkcePair> {
  const codeVerifier = await randomBase64Url(32)
  return { codeVerifier, codeChallenge: await sha256Base64Url(codeVerifier), codeChallengeMethod: 'S256' }
}

/** Random `state` value for the authorization request. */
export function createOAuthState(): Promise<string> {
  return randomBase64Url(16)
}

/**
 * OAuth 2.0 helpers for Flow applications: authorization URL with PKCE, code
 * exchange, refresh, revocation, introspection and client credentials.
 *
 * ```ts
 * const oauth = new FlowOAuthClient({ apiUrl, clientId, clientSecret, redirectUri })
 * const { url, state, codeVerifier } = await oauth.startAuthorization({ scope: ['read', 'write'] })
 * // redirect the browser to url; on the callback:
 * const { code } = oauth.parseCallback(callbackUrl, state)
 * const tokens = await oauth.exchangeCode({ code, codeVerifier })
 * const client = new FlowClient({ accessToken: tokens.accessToken, apiUrl })
 * ```
 */
export class FlowOAuthClient {
  readonly apiUrl: string
  private readonly options: FlowOAuthClientOptions
  private readonly fetchImpl: FetchLike

  constructor(options: FlowOAuthClientOptions) {
    if (!options.clientId) throw new FlowConfigurationError('FlowOAuthClient needs a clientId')
    const raw = options.apiUrl ?? (typeof process !== 'undefined' ? process.env?.FLOW_BASE_URL : undefined)
    if (!raw) throw new FlowConfigurationError('FlowOAuthClient needs the URL of your Flow deployment: pass { apiUrl } or set FLOW_BASE_URL')
    let url: URL
    try {
      url = new URL(raw)
    } catch {
      throw new FlowConfigurationError(`Invalid Flow API URL: ${raw}`)
    }
    this.apiUrl = `${url.origin}${url.pathname.replace(/\/+$/, '').replace(/\/api$/, '')}`
    this.options = options
    const fetchImpl = options.fetch ?? (typeof fetch === 'function' ? ((input, init) => fetch(input, init)) as FetchLike : undefined)
    if (!fetchImpl) throw new FlowConfigurationError('No fetch implementation found: pass { fetch }')
    this.fetchImpl = fetchImpl
  }

  /** Creates a PKCE verifier and challenge. Same as the exported `createPkcePair`. */
  createPkcePair(): Promise<PkcePair> {
    return createPkcePair()
  }

  /** Builds the URL to send the browser to (`GET /oauth/authorize`). */
  authorizationUrl(options: AuthorizationUrlOptions = {}): string {
    const redirectUri = options.redirectUri ?? this.options.redirectUri
    if (!redirectUri) throw new FlowConfigurationError('authorizationUrl needs a redirectUri (pass it here or to the constructor)')
    const params = new URLSearchParams({ response_type: 'code', client_id: this.options.clientId, redirect_uri: redirectUri })
    const scope = scopeList(options.scope)
    if (scope.length) params.set('scope', scope.join(','))
    if (options.state) params.set('state', options.state)
    if (options.actor) params.set('actor', options.actor)
    if (options.prompt) params.set('prompt', options.prompt)
    if (options.codeChallenge) {
      params.set('code_challenge', options.codeChallenge)
      params.set('code_challenge_method', 'S256')
    }
    return `${this.apiUrl}/oauth/authorize?${params.toString()}`
  }

  /** Generates `state` and a PKCE pair and builds the authorization URL; keep `state` and `codeVerifier` for the callback. */
  async startAuthorization(options: Omit<AuthorizationUrlOptions, 'codeChallenge'> = {}): Promise<StartedAuthorization> {
    const pkce = await createPkcePair()
    const state = options.state ?? (await createOAuthState())
    const url = this.authorizationUrl({ ...options, state, codeChallenge: pkce.codeChallenge })
    return { url, state, ...pkce }
  }

  /**
   * Reads the callback URL (or its query string): returns the `code`, or
   * throws a `FlowOAuthError` for `error=access_denied` and for a `state`
   * that differs from `expectedState`.
   */
  parseCallback(callback: string | URL | URLSearchParams, expectedState?: string): { code: string; state?: string } {
    const params = callback instanceof URLSearchParams ? callback : new URL(String(callback), 'http://localhost').searchParams
    const state = params.get('state') ?? undefined
    const error = params.get('error')
    if (error) {
      throw new FlowOAuthError({ message: params.get('error_description') ?? error, type: 'OAUTH_ERROR', code: error, status: 400, body: Object.fromEntries(params) })
    }
    if (expectedState !== undefined && state !== expectedState) {
      throw new FlowOAuthError({ message: 'The OAuth state does not match: possible cross-site request forgery', type: 'OAUTH_ERROR', code: 'state_mismatch', status: 400 })
    }
    const code = params.get('code')
    if (!code) throw new FlowOAuthError({ message: 'The callback has no authorization code', type: 'OAUTH_ERROR', code: 'invalid_request', status: 400 })
    return { code, state }
  }

  /** `grant_type=authorization_code`: trades the callback `code` for tokens. */
  exchangeCode(options: ExchangeCodeOptions, requestOptions?: { signal?: AbortSignal }): Promise<OAuthTokens> {
    const redirectUri = options.redirectUri ?? this.options.redirectUri
    if (!redirectUri) throw new FlowConfigurationError('exchangeCode needs a redirectUri (pass it here or to the constructor)')
    const form: Record<string, string> = { grant_type: 'authorization_code', code: options.code, redirect_uri: redirectUri }
    if (options.codeVerifier) form.code_verifier = options.codeVerifier
    return this.token(form, requestOptions)
  }

  /** `grant_type=refresh_token`. The response carries a new refresh token: store it, the old one is replaced. */
  refreshToken(refreshToken: string, requestOptions?: { signal?: AbortSignal }): Promise<OAuthTokens> {
    return this.token({ grant_type: 'refresh_token', refresh_token: refreshToken }, requestOptions)
  }

  /** `grant_type=client_credentials`: an `actor=app` token for server-to-server use (needs the Client credentials option on the application). */
  clientCredentials(options: { scope?: string | string[] } = {}, requestOptions?: { signal?: AbortSignal }): Promise<OAuthTokens> {
    const scope = scopeList(options.scope)
    return this.token({ grant_type: 'client_credentials', ...(scope.length ? { scope: scope.join(' ') } : {}) }, requestOptions)
  }

  /** Revokes an access or refresh token (RFC 7009). Unknown or already revoked tokens succeed. Revoking a refresh token revokes its chain. */
  async revokeToken(token: string, tokenTypeHint?: 'access_token' | 'refresh_token', requestOptions?: { signal?: AbortSignal }): Promise<void> {
    await this.post('/oauth/revoke', { token, ...(tokenTypeHint ? { token_type_hint: tokenTypeHint } : {}) }, requestOptions)
  }

  /** Introspects a token issued to this client (RFC 7662; confidential clients). */
  async introspectToken(token: string, requestOptions?: { signal?: AbortSignal }): Promise<TokenIntrospection> {
    return (await this.post('/oauth/introspect', { token }, requestOptions)) as TokenIntrospection
  }

  private async token(form: Record<string, string>, requestOptions?: { signal?: AbortSignal }): Promise<OAuthTokens> {
    const body = (await this.post('/oauth/token', form, requestOptions)) as Record<string, unknown>
    if (typeof body.access_token !== 'string') {
      throw new UnknownFlowError({ message: 'The token response has no access_token', type: 'UNKNOWN', body })
    }
    const expiresIn = typeof body.expires_in === 'number' ? body.expires_in : undefined
    return {
      accessToken: body.access_token,
      tokenType: typeof body.token_type === 'string' ? body.token_type : 'Bearer',
      expiresIn,
      expiresAt: expiresIn === undefined ? undefined : new Date(Date.now() + expiresIn * 1000),
      scope: scopeList(typeof body.scope === 'string' ? body.scope : undefined),
      refreshToken: typeof body.refresh_token === 'string' ? body.refresh_token : undefined,
      actor: body.actor === 'app' || body.actor === 'user' ? body.actor : undefined,
      raw: body,
    }
  }

  private authentication(): ClientAuthentication {
    return this.options.clientAuthentication ?? (this.options.clientSecret ? 'basic' : 'none')
  }

  private async post(path: string, form: Record<string, string>, requestOptions?: { signal?: AbortSignal }): Promise<unknown> {
    const { clientId, clientSecret } = this.options
    const authentication = this.authentication()
    if ((authentication === 'basic' || authentication === 'post') && !clientSecret) {
      throw new FlowConfigurationError(`clientAuthentication "${authentication}" needs a clientSecret`)
    }
    const fields = { ...form }
    const headers: Record<string, string> = { 'Content-Type': 'application/x-www-form-urlencoded', Accept: 'application/json' }
    if (authentication === 'basic') headers.Authorization = basicAuth(clientId, clientSecret as string)
    else {
      fields.client_id = clientId
      if (authentication === 'post') fields.client_secret = clientSecret as string
    }
    const url = `${this.apiUrl}${path}`
    const controller = new AbortController()
    const timeout = this.options.timeout ?? 30_000
    let timedOut = false
    const onAbort = () => controller.abort(requestOptions?.signal?.reason)
    if (requestOptions?.signal?.aborted) controller.abort()
    else requestOptions?.signal?.addEventListener('abort', onAbort, { once: true })
    const timer = timeout > 0 ? setTimeout(() => ((timedOut = true), controller.abort()), timeout) : undefined
    try {
      let response: Response
      let text: string
      try {
        response = await this.fetchImpl(url, { method: 'POST', headers, body: new URLSearchParams(fields).toString(), signal: controller.signal })
        text = await response.text()
      } catch (error) {
        if (requestOptions?.signal?.aborted) throw new AbortFlowError({ message: 'The request was aborted', type: 'ABORTED', method: 'POST', url, cause: error })
        if (timedOut) throw new TimeoutFlowError({ message: `Request timed out after ${timeout} ms`, type: 'TIMEOUT', method: 'POST', url, cause: error })
        throw new NetworkFlowError({ message: `Network request failed: ${error instanceof Error ? error.message : String(error)}`, type: 'NETWORK_ERROR', method: 'POST', url, cause: error })
      }
      let body: unknown
      try {
        body = text.trim() ? JSON.parse(text) : undefined
      } catch {
        body = text
      }
      if (!response.ok) {
        const record = typeof body === 'object' && body !== null ? (body as Record<string, unknown>) : {}
        const code = typeof record.error === 'string' ? record.error : 'invalid_request'
        const description = typeof record.error_description === 'string' ? record.error_description : undefined
        throw new FlowOAuthError({
          message: description ? `${code}: ${description}` : code,
          type: 'OAUTH_ERROR',
          code,
          status: response.status,
          requestId: response.headers.get('X-Request-ID') ?? undefined,
          method: 'POST',
          url,
          body,
        })
      }
      return body ?? {}
    } finally {
      clearTimeout(timer)
      requestOptions?.signal?.removeEventListener('abort', onAbort)
    }
  }
}

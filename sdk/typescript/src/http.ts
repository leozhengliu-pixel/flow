import { AbortFlowError, FlowConfigurationError, FlowError, NetworkFlowError, TimeoutFlowError, UnknownFlowError, parseFlowError } from './errors.js'
import { parseRateLimit, type RateLimitInfo } from './rate-limit.js'
import { SDK_VERSION } from './version.js'

/** Any `fetch` implementation (Node 18+, browsers, undici, a test double). */
export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

/** Per-request options accepted by every SDK method that talks to the API. */
export interface RequestOptions {
  /** Cancels the request (and its retries). Aborting throws `AbortFlowError`. */
  signal?: AbortSignal
  /** Extra headers for this request; they override client headers. */
  headers?: Record<string, string>
  /** Timeout in milliseconds for each attempt; overrides the client option. `0` disables it. */
  timeout?: number
  /** Retries for this request; overrides the client's retry setting. `0` disables retrying. */
  retries?: number
}

/** Automatic retries with exponential backoff. Off unless enabled on the client. */
export interface RetryOptions {
  /** Maximum retries after the first attempt. Default 3. */
  retries?: number
  /** First backoff delay in milliseconds. Default 500. */
  baseDelayMs?: number
  /** Longest delay in milliseconds; a longer `Retry-After` is not waited for and the error is thrown. Default 30000. */
  maxDelayMs?: number
  /** Multiplier per retry. Default 2. */
  factor?: number
  /** Randomise each delay between 50% and 100% of its value. Default true. */
  jitter?: boolean
  /**
   * Also retry 5xx responses and network errors of non-idempotent requests
   * (POST, PATCH). HTTP 429 is always retried because the request was not
   * processed. Default false.
   */
  retryNonIdempotent?: boolean
  /** Sleep implementation, for tests. */
  sleep?: (ms: number, signal?: AbortSignal) => Promise<void>
  /** Random source in [0, 1), for tests. */
  random?: () => number
}

export interface TransportOptions {
  /** Defaults to the global `fetch`. */
  fetch?: FetchLike
  /** Headers sent with every request. */
  headers?: Record<string, string>
  /** Timeout in milliseconds for each attempt. Default 30000; `0` disables it. */
  timeout?: number
  /** `true` enables retries with defaults, a number sets the retry count, `false` disables. Default false. */
  retry?: boolean | number | RetryOptions
  /** `User-Agent` for Node.js; `false` omits it. Ignored in browsers. */
  userAgent?: string | false
}

export type QueryParams = object

export interface RequestSpec extends RequestOptions {
  method: string
  /** Path starting with `/api/…`, with `{name}` placeholders. */
  path: string
  pathParams?: Record<string, string>
  query?: QueryParams
  /** JSON request body. */
  body?: unknown
  /** What the caller wants: parsed JSON (default), nothing, text, or the `Response`. */
  expect?: 'json' | 'none' | 'text' | 'response'
  /** Absolute URL (or a path relative to the API URL) instead of `path`. */
  url?: string
  /** Send the client's credentials and default headers (default true). */
  auth?: boolean
  /** Non-JSON body, such as file bytes. */
  rawBody?: BodyInit
}

export interface ApiTransport {
  request<T = unknown>(spec: RequestSpec): Promise<T>
}

export interface HttpResult<T> {
  data: T
  status: number
  headers: Headers
  requestId?: string
  rateLimit?: RateLimitInfo
}

interface ResolvedRetry {
  retries: number
  baseDelayMs: number
  maxDelayMs: number
  factor: number
  jitter: boolean
  retryNonIdempotent: boolean
  sleep: (ms: number, signal?: AbortSignal) => Promise<void>
  random: () => number
}

export function abortableSleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(new AbortFlowError({ message: 'The request was aborted', type: 'ABORTED' }))
    const onAbort = () => {
      clearTimeout(timer)
      reject(new AbortFlowError({ message: 'The request was aborted', type: 'ABORTED' }))
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort)
      resolve()
    }, ms)
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

export function resolveRetry(retry: TransportOptions['retry']): ResolvedRetry {
  const options: RetryOptions = retry === true ? {} : typeof retry === 'number' ? { retries: retry } : retry === false || retry === undefined ? { retries: 0 } : retry
  return {
    retries: Math.max(0, options.retries ?? 3),
    baseDelayMs: options.baseDelayMs ?? 500,
    maxDelayMs: options.maxDelayMs ?? 30_000,
    factor: options.factor ?? 2,
    jitter: options.jitter ?? true,
    retryNonIdempotent: options.retryNonIdempotent ?? false,
    sleep: options.sleep ?? abortableSleep,
    random: options.random ?? Math.random,
  }
}

const idempotent = new Set(['GET', 'HEAD', 'PUT', 'DELETE', 'OPTIONS'])

function appendQuery(params: URLSearchParams, key: string, value: unknown): void {
  if (value === undefined || value === null) return
  if (Array.isArray(value)) {
    for (const item of value) appendQuery(params, key, item)
    return
  }
  if (value instanceof Date) params.append(key, value.toISOString())
  else if (typeof value === 'object') params.append(key, JSON.stringify(value))
  else params.append(key, String(value))
}

/** Builds `path?query`, substituting `{param}` placeholders. Exported for tests. */
export function buildPath(path: string, pathParams?: Record<string, string>, query?: QueryParams): string {
  const filled = path.replace(/\{([^}]+)\}/g, (_, name: string) => {
    const value = pathParams?.[name]
    if (value === undefined || value === '') throw new TypeError(`Missing path parameter "${name}" for ${path}`)
    return encodeURIComponent(value)
  })
  const params = new URLSearchParams()
  if (query) for (const [key, value] of Object.entries(query)) appendQuery(params, key, value)
  const search = params.toString()
  return search ? `${filled}?${search}` : filled
}

function isBrowser(): boolean {
  return typeof window !== 'undefined' || typeof document !== 'undefined'
}

interface LinkedSignal {
  signal: AbortSignal
  timedOut(): boolean
  clearTimer(): void
  cleanup(): void
}

function linkSignal(user: AbortSignal | undefined, timeoutMs: number): LinkedSignal {
  const controller = new AbortController()
  let timedOut = false
  const onAbort = () => controller.abort(user?.reason)
  if (user) {
    if (user.aborted) controller.abort(user.reason)
    else user.addEventListener('abort', onAbort, { once: true })
  }
  let timer: ReturnType<typeof setTimeout> | undefined
  if (timeoutMs > 0) {
    timer = setTimeout(() => {
      timedOut = true
      controller.abort(new Error('timeout'))
    }, timeoutMs)
  }
  return {
    signal: controller.signal,
    timedOut: () => timedOut,
    clearTimer: () => clearTimeout(timer),
    cleanup: () => {
      clearTimeout(timer)
      user?.removeEventListener('abort', onAbort)
    },
  }
}

export interface HttpClientConfig {
  /** API origin, for example `https://flow.example.com`. */
  baseUrl: string
  fetch: FetchLike
  /** Headers for each attempt (credentials are resolved lazily so token functions run per attempt). */
  headers: () => Promise<Record<string, string>> | Record<string, string>
  timeout?: number
  retry?: TransportOptions['retry']
  userAgent?: string | false
  onRateLimit?: (info: RateLimitInfo) => void
}

/** Request execution: URL building, headers, timeouts, retries and error mapping. */
export class HttpClient implements ApiTransport {
  readonly baseUrl: string
  /** Rate-limit state of the most recent response that carried it. */
  lastRateLimit?: RateLimitInfo
  private readonly fetchImpl: FetchLike
  private readonly retry: ResolvedRetry
  private readonly timeout: number

  constructor(private readonly config: HttpClientConfig) {
    this.baseUrl = config.baseUrl.replace(/\/+$/, '')
    this.fetchImpl = config.fetch
    this.retry = resolveRetry(config.retry)
    this.timeout = config.timeout ?? 30_000
  }

  async request<T = unknown>(spec: RequestSpec): Promise<T> {
    return (await this.requestWithMeta<T>(spec)).data
  }

  async requestWithMeta<T = unknown>(spec: RequestSpec): Promise<HttpResult<T>> {
    const method = spec.method.toUpperCase()
    const url = spec.url ? new URL(spec.url, `${this.baseUrl}/`).toString() : `${this.baseUrl}${buildPath(spec.path, spec.pathParams, spec.query)}`
    if (spec.url && spec.auth !== false && new URL(url).origin !== new URL(this.baseUrl).origin) {
      throw new FlowConfigurationError(`Refusing to send credentials to ${new URL(url).origin}, which is not the API origin ${new URL(this.baseUrl).origin}`)
    }
    const maxRetries = spec.retries !== undefined ? Math.max(0, spec.retries) : this.retry.retries
    for (let attempt = 0; ; attempt++) {
      let failure: FlowError
      try {
        return (await this.attempt(spec, method, url)) as HttpResult<T>
      } catch (error) {
        if (!(error instanceof FlowError)) throw error
        failure = error
      }
      if (attempt >= maxRetries || !this.shouldRetry(failure, method)) throw failure
      const delay = this.delayFor(failure, attempt)
      if (delay === undefined) throw failure
      await this.retry.sleep(delay, spec.signal)
    }
  }

  private shouldRetry(failure: FlowError, method: string): boolean {
    if (failure.type === 'ABORTED') return false
    if (failure.type === 'RATELIMITED') return true
    if (failure.type === 'INTERNAL_ERROR' || failure.type === 'UNAVAILABLE' || failure.type === 'NETWORK_ERROR' || failure.type === 'TIMEOUT') {
      return idempotent.has(method) || this.retry.retryNonIdempotent
    }
    return false
  }

  private delayFor(failure: FlowError, attempt: number): number | undefined {
    const { baseDelayMs, maxDelayMs, factor, jitter, random } = this.retry
    const retryAfter = failure.rateLimit?.retryAfter
    if (retryAfter !== undefined) {
      const wait = retryAfter * 1000
      return wait > maxDelayMs ? undefined : wait
    }
    const delay = Math.min(maxDelayMs, baseDelayMs * factor ** attempt)
    return jitter ? Math.round(delay * (0.5 + random() * 0.5)) : delay
  }

  private async buildHeaders(spec: RequestSpec, hasJsonBody: boolean): Promise<Headers> {
    const headers = new Headers()
    headers.set('Accept', 'application/json')
    if (spec.auth !== false) {
      const base = await this.config.headers()
      for (const [name, value] of Object.entries(base)) headers.set(name, value)
      const agent = this.config.userAgent
      if (agent !== false && !isBrowser() && !headers.has('User-Agent')) headers.set('User-Agent', agent ?? `flow-sdk-typescript/${SDK_VERSION}`)
    }
    if (hasJsonBody) headers.set('Content-Type', 'application/json')
    for (const [name, value] of Object.entries(spec.headers ?? {})) headers.set(name, value)
    return headers
  }

  private async attempt(spec: RequestSpec, method: string, url: string): Promise<HttpResult<unknown>> {
    if (spec.signal?.aborted) throw new AbortFlowError({ message: 'The request was aborted', type: 'ABORTED', method, url })
    const hasJsonBody = spec.body !== undefined && spec.rawBody === undefined
    const headers = await this.buildHeaders(spec, hasJsonBody)
    const linked = linkSignal(spec.signal, spec.timeout ?? this.timeout)
    const classify = (error: unknown): FlowError => {
      if (error instanceof FlowError) return error
      if (spec.signal?.aborted) return new AbortFlowError({ message: 'The request was aborted', type: 'ABORTED', method, url, cause: error })
      if (linked.timedOut()) return new TimeoutFlowError({ message: `Request timed out after ${spec.timeout ?? this.timeout} ms`, type: 'TIMEOUT', method, url, cause: error })
      const detail = error instanceof Error ? error.message : String(error)
      return new NetworkFlowError({ message: `Network request failed: ${detail}`, type: 'NETWORK_ERROR', method, url, cause: error })
    }
    try {
      let response: Response
      try {
        response = await this.fetchImpl(url, {
          method,
          headers,
          body: spec.rawBody ?? (hasJsonBody ? JSON.stringify(spec.body) : undefined),
          signal: linked.signal,
        })
      } catch (error) {
        throw classify(error)
      }
      const rateLimit = parseRateLimit(response.headers)
      if (rateLimit) {
        this.lastRateLimit = rateLimit
        this.config.onRateLimit?.(rateLimit)
      }
      const requestId = response.headers.get('X-Request-ID') ?? undefined
      if (response.ok && spec.expect === 'response') {
        linked.clearTimer()
        return { data: response, status: response.status, headers: response.headers, requestId, rateLimit }
      }
      let text: string
      try {
        text = await response.text()
      } catch (error) {
        throw classify(error)
      }
      if (!response.ok) {
        throw parseFlowError({ status: response.status, body: parseBody(text), headers: response.headers, method, url })
      }
      const base = { status: response.status, headers: response.headers, requestId, rateLimit }
      if (spec.expect === 'none') return { ...base, data: undefined }
      if (spec.expect === 'text') return { ...base, data: text }
      if (text.trim() === '') return { ...base, data: undefined }
      try {
        return { ...base, data: JSON.parse(text) }
      } catch (error) {
        throw new UnknownFlowError({ message: 'The server returned a response that is not valid JSON', type: 'UNKNOWN', status: response.status, requestId, method, url, body: text.slice(0, 500), cause: error })
      }
    } finally {
      linked.cleanup()
    }
  }
}

function parseBody(text: string): unknown {
  if (text.trim() === '') return undefined
  try {
    return JSON.parse(text)
  } catch {
    return text
  }
}

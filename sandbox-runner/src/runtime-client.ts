import { backoffDelay, sleep } from './backoff.js'
import { errorMessage, silentLogger, type Logger } from './log.js'
import type {
  BootstrapResponse,
  CredentialRequest,
  CredentialResponse,
  HeartbeatRequest,
  HeartbeatResponse,
  InboxResponse,
  RunnerEvent,
  SetupSecretsResponse,
  SignRequest,
  SignResponse,
  SnapshotCompleteRequest,
  TurnCompleteRequest,
} from './protocol.js'

// Client for Flow's runner-facing API (/internal/coding-runtime/*, §7.2).
// Every call authenticates with the sandbox token. Transient failures
// (network errors, timeouts, 408/425/429/5xx) are retried with jittered
// backoff; 401/403 mean the token was revoked and surface as AuthError.

export const RUNTIME_PREFIX = '/internal/coding-runtime'

export class RuntimeError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly retryable: boolean,
    readonly body = '',
  ) {
    super(message)
    this.name = 'RuntimeError'
  }
}

/** The sandbox token is no longer accepted: the sandbox is being torn down. */
export class AuthError extends RuntimeError {
  constructor(status: number, body = '') {
    super(`runtime API rejected the sandbox token (${status})`, status, false, body)
    this.name = 'AuthError'
  }
}

export interface RetryPolicy {
  /** Total attempts (Infinity retries until the deadline or the signal). */
  attempts: number
  /** Give up after this many milliseconds (measured from the first attempt). */
  deadlineMs?: number
  baseMs?: number
  maxMs?: number
}

export const RETRY_NONE: RetryPolicy = { attempts: 1 }
export const RETRY_DEFAULT: RetryPolicy = { attempts: 5, baseMs: 1000, maxMs: 15_000 }
/** For calls that must eventually land (turn complete, snapshot complete). */
export const RETRY_PERSISTENT: RetryPolicy = { attempts: Infinity, deadlineMs: 30 * 60_000, baseMs: 1000, maxMs: 30_000 }

export interface RequestOptions {
  query?: Record<string, string | number | undefined>
  json?: unknown
  body?: BodyInit
  contentType?: string
  timeoutMs?: number
  retry?: RetryPolicy
  signal?: AbortSignal
  /** Statuses treated as success without a body (e.g. 204, 404 for "none"). */
  acceptStatus?: number[]
}

export interface RuntimeClientOptions {
  baseUrl: string
  token: () => string
  fetch?: typeof fetch
  logger?: Logger
  timeoutMs?: number
  sleep?: (ms: number, signal?: AbortSignal) => Promise<void>
  random?: () => number
  now?: () => number
}

export function isRetryableStatus(status: number): boolean {
  return status === 408 || status === 425 || status === 429 || (status >= 500 && status !== 501)
}

export class RuntimeClient {
  readonly baseUrl: string
  private readonly token: () => string
  private readonly fetchImpl: typeof fetch
  private readonly logger: Logger
  private readonly timeoutMs: number
  private readonly sleepImpl: (ms: number, signal?: AbortSignal) => Promise<void>
  private readonly random: () => number
  private readonly now: () => number

  constructor(options: RuntimeClientOptions) {
    this.baseUrl = options.baseUrl.replace(/\/+$/, '')
    this.token = options.token
    this.fetchImpl = options.fetch ?? fetch
    this.logger = options.logger ?? silentLogger
    this.timeoutMs = options.timeoutMs ?? 30_000
    this.sleepImpl = options.sleep ?? sleep
    this.random = options.random ?? Math.random
    this.now = options.now ?? Date.now
  }

  url(path: string, query?: RequestOptions['query']): string {
    const full = path.startsWith('http://') || path.startsWith('https://') ? path : this.baseUrl + path
    const url = new URL(full)
    for (const [key, value] of Object.entries(query ?? {})) {
      if (value !== undefined) url.searchParams.set(key, String(value))
    }
    return url.toString()
  }

  /** Performs one request with retries and returns the raw Response (status 2xx or accepted). */
  async request(method: string, path: string, options: RequestOptions = {}): Promise<Response> {
    const retry = options.retry ?? RETRY_DEFAULT
    const started = this.now()
    let lastError: unknown
    for (let attempt = 0; attempt < retry.attempts; attempt++) {
      if (options.signal?.aborted) throw options.signal.reason ?? new Error('aborted')
      try {
        return await this.once(method, path, options)
      } catch (error) {
        lastError = error
        const retryable = error instanceof RuntimeError ? error.retryable : !(error instanceof AuthError) && !isAbort(error, options.signal)
        if (!retryable) throw error
        const delay = backoffDelay(attempt, retry.baseMs ?? 1000, retry.maxMs ?? 30_000, this.random)
        const elapsed = this.now() - started
        if (attempt + 1 >= retry.attempts || (retry.deadlineMs !== undefined && elapsed + delay > retry.deadlineMs)) break
        this.logger.warn('runtime request failed, retrying', { method, path, attempt: attempt + 1, delayMs: delay, error: errorMessage(error) })
        await this.sleepImpl(delay, options.signal)
      }
    }
    throw lastError
  }

  private async once(method: string, path: string, options: RequestOptions): Promise<Response> {
    const headers: Record<string, string> = { authorization: `Bearer ${this.token()}` }
    let body = options.body
    if (options.json !== undefined) {
      headers['content-type'] = 'application/json'
      body = JSON.stringify(options.json)
    } else if (options.contentType) {
      headers['content-type'] = options.contentType
    }
    const timeout = AbortSignal.timeout(options.timeoutMs ?? this.timeoutMs)
    const signal = options.signal ? AbortSignal.any([timeout, options.signal]) : timeout
    const init: RequestInit & { duplex?: 'half' } = { method, headers, body, signal }
    if (body && typeof (body as ReadableStream).getReader === 'function') init.duplex = 'half'
    let response: Response
    try {
      response = await this.fetchImpl(this.url(path, options.query), init)
    } catch (error) {
      if (isAbort(error, options.signal)) throw error
      throw new RuntimeError(`${method} ${path}: ${errorMessage(error)}`, 0, true)
    }
    if (response.ok || options.acceptStatus?.includes(response.status)) return response
    const text = await response.text().catch(() => '')
    if (response.status === 401 || response.status === 403) throw new AuthError(response.status, text)
    throw new RuntimeError(`${method} ${path}: HTTP ${response.status} ${text.slice(0, 300)}`.trim(), response.status, isRetryableStatus(response.status), text)
  }

  private async json<T>(method: string, path: string, options: RequestOptions = {}): Promise<T> {
    const response = await this.request(method, path, options)
    const text = await response.text()
    return (text ? JSON.parse(text) : {}) as T
  }

  /** GET /bootstrap. Returns null on 204 (a warm sandbox that is not bound yet). */
  async bootstrap(waitSeconds?: number, signal?: AbortSignal): Promise<BootstrapResponse | null> {
    const response = await this.request('GET', `${RUNTIME_PREFIX}/bootstrap`, {
      query: { wait: waitSeconds ? `${waitSeconds}s` : undefined },
      timeoutMs: ((waitSeconds ?? 0) + 30) * 1000,
      acceptStatus: [204],
      retry: RETRY_PERSISTENT,
      signal,
    })
    if (response.status === 204) return null
    return (await response.json()) as BootstrapResponse
  }

  setupSecrets(): Promise<SetupSecretsResponse> {
    return this.json('GET', `${RUNTIME_PREFIX}/setup-secrets`)
  }

  heartbeat(request: HeartbeatRequest, signal?: AbortSignal): Promise<HeartbeatResponse> {
    return this.json('POST', `${RUNTIME_PREFIX}/heartbeat`, { json: request, retry: RETRY_NONE, timeoutMs: 10_000, signal })
  }

  /** POST /events; one attempt, the EventBuffer owns retries. */
  async postEvents(events: RunnerEvent[], signal?: AbortSignal): Promise<void> {
    await this.request('POST', `${RUNTIME_PREFIX}/events`, { json: { events }, retry: RETRY_NONE, timeoutMs: 15_000, signal })
  }

  inbox(after: number, waitSeconds: number, signal?: AbortSignal): Promise<InboxResponse> {
    return this.json('GET', `${RUNTIME_PREFIX}/inbox`, {
      query: { after, wait: `${waitSeconds}s` },
      timeoutMs: (waitSeconds + 15) * 1000,
      retry: RETRY_NONE,
      signal,
    })
  }

  async completeTurn(turnId: string, request: TurnCompleteRequest, signal?: AbortSignal): Promise<void> {
    await this.request('POST', `${RUNTIME_PREFIX}/turns/${encodeURIComponent(turnId)}/complete`, {
      json: request,
      retry: RETRY_PERSISTENT,
      signal,
    })
  }

  credentials(request: CredentialRequest): Promise<CredentialResponse> {
    return this.json('POST', `${RUNTIME_PREFIX}/credentials`, { json: request, retry: { attempts: 4, baseMs: 500, maxMs: 5000 } })
  }

  sign(request: SignRequest): Promise<SignResponse> {
    return this.json('POST', `${RUNTIME_PREFIX}/sign`, { json: request, retry: { attempts: 4, baseMs: 500, maxMs: 5000 } })
  }

  /** GET /harness-state; null when Flow holds none (404). */
  async getHarnessState(): Promise<Uint8Array | null> {
    const response = await this.request('GET', `${RUNTIME_PREFIX}/harness-state`, { acceptStatus: [204, 404], timeoutMs: 120_000 })
    if (response.status === 204 || response.status === 404) return null
    return new Uint8Array(await response.arrayBuffer())
  }

  async putHarnessState(archive: Uint8Array): Promise<void> {
    await this.request('PUT', `${RUNTIME_PREFIX}/harness-state`, {
      body: archive as unknown as BodyInit,
      contentType: 'application/zstd',
      timeoutMs: 120_000,
    })
  }

  /** PUT the snapshot archive as a stream (one attempt; callers re-create the stream to retry). */
  async putSnapshotArchive(snapshotId: string, body: ReadableStream<Uint8Array>, uploadUrl?: string, signal?: AbortSignal): Promise<void> {
    await this.request('PUT', uploadUrl || `${RUNTIME_PREFIX}/snapshots/${encodeURIComponent(snapshotId)}/archive`, {
      body: body as unknown as BodyInit,
      contentType: 'application/zstd',
      timeoutMs: 2 * 60 * 60_000,
      retry: RETRY_NONE,
      signal,
    })
  }

  /** GET the snapshot archive; `archiveUrl` (FLOW_ARCHIVE_URL) overrides the default route. */
  async getSnapshotArchive(snapshotId: string, signal?: AbortSignal, archiveUrl?: string): Promise<ReadableStream<Uint8Array>> {
    const response = await this.request('GET', archiveUrl || `${RUNTIME_PREFIX}/snapshots/${encodeURIComponent(snapshotId)}/archive`, {
      timeoutMs: 2 * 60 * 60_000,
      signal,
    })
    if (!response.body) throw new RuntimeError('snapshot archive response has no body', response.status, false)
    return response.body
  }

  async postSnapshotLogs(snapshotId: string, lines: string[]): Promise<void> {
    await this.request('POST', `${RUNTIME_PREFIX}/snapshots/${encodeURIComponent(snapshotId)}/logs`, {
      json: { lines },
      retry: { attempts: 3, baseMs: 500, maxMs: 5000 },
    })
  }

  async completeSnapshot(snapshotId: string, request: SnapshotCompleteRequest): Promise<void> {
    await this.request('POST', `${RUNTIME_PREFIX}/snapshots/${encodeURIComponent(snapshotId)}/complete`, {
      json: request,
      retry: RETRY_PERSISTENT,
    })
  }
}

function isAbort(error: unknown, signal?: AbortSignal): boolean {
  return Boolean(signal?.aborted) && (error === signal?.reason || (error instanceof Error && error.name === 'AbortError'))
}

import { parseRateLimit, type HeadersLike, type RateLimitInfo } from './rate-limit.js'

/** The `type` values of Flow's error envelope. */
export type FlowApiErrorType =
  | 'INVALID_INPUT'
  | 'AUTHENTICATION_ERROR'
  | 'FORBIDDEN'
  | 'FEATURE_NOT_ACCESSIBLE'
  | 'NOT_FOUND'
  | 'CONFLICT'
  | 'RATELIMITED'
  | 'INTERNAL_ERROR'
  | 'UNAVAILABLE'

/** Client-side failure categories that never came from the server's envelope. */
export type FlowClientErrorType = 'NETWORK_ERROR' | 'TIMEOUT' | 'ABORTED' | 'OAUTH_ERROR' | 'UNKNOWN'

export type FlowErrorType = FlowApiErrorType | FlowClientErrorType

export interface FlowErrorInit {
  message: string
  type: FlowErrorType
  /** Machine code: the envelope's `code` (equal to `type` unless more specific), or the OAuth `error`. */
  code?: string
  /** HTTP status; 0 for failures without a response. */
  status?: number
  requestId?: string
  method?: string
  url?: string
  /** Parsed response body, or the text when it was not JSON. */
  body?: unknown
  rateLimit?: RateLimitInfo
  cause?: unknown
}

/**
 * Base class of every error thrown by the SDK. Switch on `type` (or use
 * `instanceof` with a subclass); `code`, `status` and `requestId` identify the
 * failure when you report it.
 */
export class FlowError extends Error {
  readonly type: FlowErrorType
  readonly code: string
  readonly status: number
  readonly requestId?: string
  readonly method?: string
  readonly url?: string
  readonly body?: unknown
  readonly rateLimit?: RateLimitInfo

  constructor(init: FlowErrorInit) {
    super(init.message, init.cause === undefined ? undefined : { cause: init.cause })
    this.name = new.target.name
    this.type = init.type
    this.code = init.code ?? init.type
    this.status = init.status ?? 0
    this.requestId = init.requestId
    this.method = init.method
    this.url = init.url
    this.body = init.body
    this.rateLimit = init.rateLimit
    Object.setPrototypeOf(this, new.target.prototype)
  }

  /** True for failures that are worth retrying with backoff: rate limits, 5xx and network trouble. */
  get retryable(): boolean {
    return this.type === 'RATELIMITED' || this.type === 'INTERNAL_ERROR' || this.type === 'UNAVAILABLE' || this.type === 'NETWORK_ERROR' || this.type === 'TIMEOUT'
  }
}

/** HTTP 400, 405, 413, 415, 422: the request was malformed or failed validation. */
export class InvalidInputFlowError extends FlowError {}
/** HTTP 401: missing, invalid or expired credentials. */
export class AuthenticationFlowError extends FlowError {}
/** HTTP 403: not allowed for this user, key or scope. */
export class ForbiddenFlowError extends FlowError {}
/** HTTP 403: the workspace has the feature turned off. */
export class FeatureNotAccessibleFlowError extends FlowError {}
/** HTTP 404, 410: the resource is missing or not visible to the caller. */
export class NotFoundFlowError extends FlowError {}

/** HTTP 409, 412: concurrent change or uniqueness conflict. */
export class ConflictFlowError extends FlowError {
  /** The current resource when the server reports `VERSION_CONFLICT`. */
  get current(): unknown {
    return typeof this.body === 'object' && this.body !== null ? (this.body as { current?: unknown }).current : undefined
  }
}

/** HTTP 429: a request, complexity or endpoint budget is exhausted. */
export class RatelimitedFlowError extends FlowError {
  /** Seconds to wait before retrying, from `Retry-After`. */
  get retryAfter(): number | undefined {
    return this.rateLimit?.retryAfter
  }
}

/** HTTP 500. */
export class InternalFlowError extends FlowError {}
/** HTTP 502, 503, 504. */
export class UnavailableFlowError extends FlowError {}
/** No response: DNS, connection reset, TLS and similar failures. */
export class NetworkFlowError extends FlowError {}
/** The per-attempt timeout elapsed. */
export class TimeoutFlowError extends NetworkFlowError {}
/** The caller's AbortSignal fired. Never retried. */
export class AbortFlowError extends FlowError {}
/** An RFC 6749 error from `/oauth/*`: see `oauthError` and `errorDescription`. */
export class FlowOAuthError extends FlowError {
  /** RFC 6749 error code such as `invalid_grant`. */
  get oauthError(): string {
    return this.code
  }
  get errorDescription(): string | undefined {
    return typeof this.body === 'object' && this.body !== null ? (this.body as { error_description?: string }).error_description : undefined
  }
}
/** Response that cannot be understood (a 2xx whose body is not JSON, for example). */
export class UnknownFlowError extends FlowError {}

const knownTypes: readonly string[] = [
  'INVALID_INPUT',
  'AUTHENTICATION_ERROR',
  'FORBIDDEN',
  'FEATURE_NOT_ACCESSIBLE',
  'NOT_FOUND',
  'CONFLICT',
  'RATELIMITED',
  'INTERNAL_ERROR',
  'UNAVAILABLE',
]

/** Error type implied by an HTTP status, for responses without Flow's envelope (proxies, gateways). */
export function errorTypeForStatus(status: number): FlowErrorType {
  if (status === 401) return 'AUTHENTICATION_ERROR'
  if (status === 403) return 'FORBIDDEN'
  if (status === 404 || status === 410) return 'NOT_FOUND'
  if (status === 409 || status === 412) return 'CONFLICT'
  if (status === 429) return 'RATELIMITED'
  if (status === 502 || status === 503 || status === 504) return 'UNAVAILABLE'
  if (status >= 500) return 'INTERNAL_ERROR'
  if (status >= 400) return 'INVALID_INPUT'
  return 'UNKNOWN'
}

const classes: Record<string, new (init: FlowErrorInit) => FlowError> = {
  INVALID_INPUT: InvalidInputFlowError,
  AUTHENTICATION_ERROR: AuthenticationFlowError,
  FORBIDDEN: ForbiddenFlowError,
  FEATURE_NOT_ACCESSIBLE: FeatureNotAccessibleFlowError,
  NOT_FOUND: NotFoundFlowError,
  CONFLICT: ConflictFlowError,
  RATELIMITED: RatelimitedFlowError,
  INTERNAL_ERROR: InternalFlowError,
  UNAVAILABLE: UnavailableFlowError,
}

export interface ParseFlowErrorInput {
  status: number
  /** Parsed JSON body, or text, or undefined when empty. */
  body?: unknown
  headers?: HeadersLike
  method?: string
  url?: string
}

/**
 * Turns an error response into the matching FlowError subclass. Understands
 * Flow's envelope `{error, code, type, requestId}`; falls back to the HTTP
 * status and the `X-Request-ID` header when the body is not an envelope.
 */
export function parseFlowError(input: ParseFlowErrorInput): FlowError {
  const { status, body, headers } = input
  const envelope = typeof body === 'object' && body !== null ? (body as Record<string, unknown>) : undefined
  const declared = typeof envelope?.type === 'string' ? envelope.type : undefined
  const type = (declared && knownTypes.includes(declared) ? declared : errorTypeForStatus(status)) as FlowErrorType
  const message =
    (typeof envelope?.error === 'string' && envelope.error) ||
    (typeof envelope?.message === 'string' && envelope.message) ||
    (typeof body === 'string' && body.trim().slice(0, 300)) ||
    `Flow API request failed with status ${status}`
  const requestId = (typeof envelope?.requestId === 'string' && envelope.requestId) || headers?.get('X-Request-ID') || undefined
  const init: FlowErrorInit = {
    message,
    type,
    code: typeof envelope?.code === 'string' ? envelope.code : undefined,
    status,
    requestId,
    method: input.method,
    url: input.url,
    body,
    rateLimit: headers ? parseRateLimit(headers) : undefined,
  }
  const ErrorClass = classes[type] ?? UnknownFlowError
  return new ErrorClass(init)
}

/** Narrowing helper: `catch (e) { if (isFlowError(e)) … }`. */
export function isFlowError(error: unknown): error is FlowError {
  return error instanceof FlowError
}

/** Thrown for invalid client construction (no credentials, no API URL). */
export class FlowConfigurationError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'FlowConfigurationError'
    Object.setPrototypeOf(this, new.target.prototype)
  }
}

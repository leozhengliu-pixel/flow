/**
 * Rate-limit state read from the `X-RateLimit-*`, `X-Complexity` and
 * `Retry-After` response headers. Fields are omitted when the server did not
 * send the header (browser sessions are exempt, for example).
 */
export interface RateLimitInfo {
  /** Requests allowed per hour for this credential. */
  requestsLimit?: number
  requestsRemaining?: number
  /** When the request bucket is full again. */
  requestsResetAt?: Date
  /** Complexity points this request cost (`X-Complexity`). */
  complexity?: number
  complexityLimit?: number
  complexityRemaining?: number
  complexityResetAt?: Date
  /** Name of the endpoint budget that applied, for example `fileUpload`. */
  endpointName?: string
  endpointRequestsLimit?: number
  endpointRequestsRemaining?: number
  endpointRequestsResetAt?: Date
  /** Seconds to wait before retrying (`Retry-After`, HTTP 429/503). */
  retryAfter?: number
}

export interface HeadersLike {
  get(name: string): string | null
}

function integer(headers: HeadersLike, name: string): number | undefined {
  const value = headers.get(name)
  if (value === null || value.trim() === '') return undefined
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : undefined
}

function epoch(headers: HeadersLike, name: string): Date | undefined {
  const parsed = integer(headers, name)
  return parsed === undefined ? undefined : new Date(parsed)
}

/** Seconds from a `Retry-After` value, which is a number of seconds or an HTTP date. */
export function parseRetryAfter(value: string | null | undefined, now: number = Date.now()): number | undefined {
  if (value === null || value === undefined || value.trim() === '') return undefined
  const seconds = Number(value)
  if (Number.isFinite(seconds)) return Math.max(0, seconds)
  const date = Date.parse(value)
  return Number.isNaN(date) ? undefined : Math.max(0, Math.ceil((date - now) / 1000))
}

/** Reads the rate-limit headers of a response; returns undefined when there are none. */
export function parseRateLimit(headers: HeadersLike, now: number = Date.now()): RateLimitInfo | undefined {
  const info: RateLimitInfo = {
    requestsLimit: integer(headers, 'X-RateLimit-Requests-Limit'),
    requestsRemaining: integer(headers, 'X-RateLimit-Requests-Remaining'),
    requestsResetAt: epoch(headers, 'X-RateLimit-Requests-Reset'),
    complexity: integer(headers, 'X-Complexity'),
    complexityLimit: integer(headers, 'X-RateLimit-Complexity-Limit'),
    complexityRemaining: integer(headers, 'X-RateLimit-Complexity-Remaining'),
    complexityResetAt: epoch(headers, 'X-RateLimit-Complexity-Reset'),
    endpointName: headers.get('X-RateLimit-Endpoint-Name') ?? undefined,
    endpointRequestsLimit: integer(headers, 'X-RateLimit-Endpoint-Requests-Limit'),
    endpointRequestsRemaining: integer(headers, 'X-RateLimit-Endpoint-Requests-Remaining'),
    endpointRequestsResetAt: epoch(headers, 'X-RateLimit-Endpoint-Requests-Reset'),
    retryAfter: parseRetryAfter(headers.get('Retry-After'), now),
  }
  let present = false
  for (const key of Object.keys(info) as (keyof RateLimitInfo)[]) {
    if (info[key] === undefined) delete info[key]
    else present = true
  }
  return present ? info : undefined
}

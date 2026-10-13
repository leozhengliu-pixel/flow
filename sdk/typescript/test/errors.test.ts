import { describe, expect, it } from 'vitest'
import {
  AbortFlowError,
  AuthenticationFlowError,
  ConflictFlowError,
  FeatureNotAccessibleFlowError,
  FlowClient,
  FlowError,
  ForbiddenFlowError,
  InternalFlowError,
  InvalidInputFlowError,
  NetworkFlowError,
  NotFoundFlowError,
  RatelimitedFlowError,
  TimeoutFlowError,
  UnavailableFlowError,
  UnknownFlowError,
  isFlowError,
  parseFlowError,
  parseRateLimit,
  parseRetryAfter,
} from '../src/index.js'
import { API_URL, envelope, json, mockFetch, recordingSleep } from './helpers.js'

function flowFor(responder: Parameters<typeof mockFetch>[0], options: Record<string, unknown> = {}) {
  const mock = mockFetch(responder)
  return { ...mock, flow: new FlowClient({ apiKey: 'k', apiUrl: API_URL, fetch: mock.fetch, ...options }) }
}

async function failure(promise: Promise<unknown>): Promise<FlowError> {
  try {
    await promise
  } catch (error) {
    return error as FlowError
  }
  throw new Error('expected the request to fail')
}

describe('error mapping', () => {
  const cases: Array<[string, number, new (...args: never[]) => FlowError]> = [
    ['INVALID_INPUT', 400, InvalidInputFlowError],
    ['AUTHENTICATION_ERROR', 401, AuthenticationFlowError],
    ['FORBIDDEN', 403, ForbiddenFlowError],
    ['FEATURE_NOT_ACCESSIBLE', 403, FeatureNotAccessibleFlowError],
    ['NOT_FOUND', 404, NotFoundFlowError],
    ['CONFLICT', 409, ConflictFlowError],
    ['RATELIMITED', 429, RatelimitedFlowError],
    ['INTERNAL_ERROR', 500, InternalFlowError],
    ['UNAVAILABLE', 503, UnavailableFlowError],
  ]
  it.each(cases)('maps %s (%i)', async (type, status, ErrorClass) => {
    const { flow } = flowFor(() => envelope(type, `boom ${type}`, status))
    const error = await failure(flow.issue('ENG-1'))
    expect(error).toBeInstanceOf(ErrorClass)
    expect(error).toBeInstanceOf(FlowError)
    expect(error).toBeInstanceOf(Error)
    expect(isFlowError(error)).toBe(true)
    expect(error).toMatchObject({ type, code: type, status, requestId: 'req_123', message: `boom ${type}`, method: 'GET' })
    expect(error.url).toBe(`${API_URL}/api/issue-records/ENG-1`)
    expect(error.name).toBe(ErrorClass.name)
  })

  it('keeps specific codes and the current resource of a version conflict', async () => {
    const { flow } = flowFor(() => envelope('CONFLICT', 'issue changed', 409, { code: 'VERSION_CONFLICT', current: { id: 'issue_1', version: 7 } }))
    const error = await failure(flow.updateIssue('ENG-1', { title: 'x', expectedVersion: 6 }))
    expect(error).toBeInstanceOf(ConflictFlowError)
    expect(error.code).toBe('VERSION_CONFLICT')
    expect((error as ConflictFlowError).current).toEqual({ id: 'issue_1', version: 7 })
  })

  it('falls back to the HTTP status when the body is not an envelope', async () => {
    const { flow } = flowFor(() => new Response('<html>Bad gateway</html>', { status: 502, headers: { 'X-Request-ID': 'req_gw' } }))
    const error = await failure(flow.issue('ENG-1'))
    expect(error).toBeInstanceOf(UnavailableFlowError)
    expect(error.requestId).toBe('req_gw')
    expect(error.message).toContain('Bad gateway')
    expect(parseFlowError({ status: 410 })).toBeInstanceOf(NotFoundFlowError)
    expect(parseFlowError({ status: 422, body: { error: 'bad' } })).toBeInstanceOf(InvalidInputFlowError)
    expect(parseFlowError({ status: 412 })).toBeInstanceOf(ConflictFlowError)
  })

  it('reports invalid JSON in a success response', async () => {
    const { flow } = flowFor(() => new Response('not json', { status: 200 }))
    expect(await failure(flow.issue('ENG-1'))).toBeInstanceOf(UnknownFlowError)
  })

  it('is not retryable for client errors', () => {
    expect(parseFlowError({ status: 404 }).retryable).toBe(false)
    expect(parseFlowError({ status: 429 }).retryable).toBe(true)
    expect(parseFlowError({ status: 503 }).retryable).toBe(true)
  })
})

describe('rate limits', () => {
  const headers = {
    'X-RateLimit-Requests-Limit': '2500',
    'X-RateLimit-Requests-Remaining': '0',
    'X-RateLimit-Requests-Reset': '1791822527821',
    'X-Complexity': '51',
    'X-RateLimit-Complexity-Limit': '3000000',
    'X-RateLimit-Complexity-Remaining': '2999949',
    'X-RateLimit-Complexity-Reset': '1791822600000',
    'X-RateLimit-Endpoint-Name': 'fileUpload',
    'X-RateLimit-Endpoint-Requests-Limit': '1000',
    'X-RateLimit-Endpoint-Requests-Remaining': '10',
    'X-RateLimit-Endpoint-Requests-Reset': '1791822700000',
    'Retry-After': '12',
  }

  it('parses every header', () => {
    const info = parseRateLimit(new Headers(headers))!
    expect(info).toMatchObject({
      requestsLimit: 2500,
      requestsRemaining: 0,
      complexity: 51,
      complexityLimit: 3_000_000,
      complexityRemaining: 2_999_949,
      endpointName: 'fileUpload',
      endpointRequestsLimit: 1000,
      endpointRequestsRemaining: 10,
      retryAfter: 12,
    })
    expect(info.requestsResetAt).toEqual(new Date(1791822527821))
    expect(info.complexityResetAt).toEqual(new Date(1791822600000))
    expect(info.endpointRequestsResetAt).toEqual(new Date(1791822700000))
    expect(parseRateLimit(new Headers())).toBeUndefined()
  })

  it('parses Retry-After as seconds or HTTP date', () => {
    expect(parseRetryAfter('30')).toBe(30)
    expect(parseRetryAfter('Wed, 21 Oct 2026 07:28:30 GMT', Date.parse('Wed, 21 Oct 2026 07:28:00 GMT'))).toBe(30)
    expect(parseRetryAfter('garbage')).toBeUndefined()
    expect(parseRetryAfter(null)).toBeUndefined()
  })

  it('attaches rate-limit info to the error', async () => {
    const { flow } = flowFor(() => envelope('RATELIMITED', 'Too many requests', 429, {}, headers))
    const error = (await failure(flow.issue('ENG-1'))) as RatelimitedFlowError
    expect(error).toBeInstanceOf(RatelimitedFlowError)
    expect(error.retryAfter).toBe(12)
    expect(error.rateLimit).toMatchObject({ requestsRemaining: 0, complexity: 51 })
    expect(flow.rateLimit?.requestsRemaining).toBe(0)
  })
})

describe('retries', () => {
  it('does not retry by default', async () => {
    const { flow, calls } = flowFor(() => envelope('UNAVAILABLE', 'down', 503))
    await failure(flow.issue('ENG-1'))
    expect(calls).toHaveLength(1)
  })

  it('retries 429 honouring Retry-After', async () => {
    const { sleep, delays } = recordingSleep()
    const { flow, calls } = flowFor(
      (_, index) => (index < 2 ? envelope('RATELIMITED', 'slow down', 429, {}, { 'Retry-After': '3' }) : json({ id: 'issue_1' })),
      { retry: { retries: 3, sleep } },
    )
    expect((await flow.issue('ENG-1')).id).toBe('issue_1')
    expect(calls).toHaveLength(3)
    expect(delays).toEqual([3000, 3000])
  })

  it('retries 5xx of idempotent requests with exponential backoff', async () => {
    const { sleep, delays } = recordingSleep()
    const { flow, calls } = flowFor((_, index) => (index < 3 ? envelope('UNAVAILABLE', 'down', 503) : json({ id: 'issue_1' })), {
      retry: { retries: 3, baseDelayMs: 100, jitter: false, sleep },
    })
    await flow.issue('ENG-1')
    expect(calls).toHaveLength(4)
    expect(delays).toEqual([100, 200, 400])
  })

  it('applies jitter between 50% and 100% of the delay', async () => {
    const { sleep, delays } = recordingSleep()
    const { flow } = flowFor((_, index) => (index < 1 ? envelope('INTERNAL_ERROR', 'oops', 500) : json({ id: 'issue_1' })), {
      retry: { baseDelayMs: 1000, random: () => 0, sleep },
    })
    await flow.issue('ENG-1')
    expect(delays).toEqual([500])
  })

  it('gives up after the retry budget and throws the last error', async () => {
    const { sleep } = recordingSleep()
    const { flow, calls } = flowFor(() => envelope('INTERNAL_ERROR', 'oops', 500), { retry: { retries: 2, sleep } })
    expect(await failure(flow.issue('ENG-1'))).toBeInstanceOf(InternalFlowError)
    expect(calls).toHaveLength(3)
  })

  it('does not retry 5xx of non-idempotent requests, but retries their 429', async () => {
    const { sleep } = recordingSleep()
    const failing = flowFor(() => envelope('UNAVAILABLE', 'down', 503), { retry: { sleep } })
    await failure(failing.flow.createIssue({ teamId: 't', title: 'x', description: '' }))
    expect(failing.calls).toHaveLength(1)
    const limited = flowFor((_, index) => (index === 0 ? envelope('RATELIMITED', 'slow', 429, {}, { 'Retry-After': '1' }) : json({ id: 'issue_1' }, { status: 201 })), { retry: { sleep } })
    await limited.flow.createIssue({ teamId: 't', title: 'x', description: '' })
    expect(limited.calls).toHaveLength(2)
  })

  it('retries non-idempotent requests when asked to', async () => {
    const { sleep } = recordingSleep()
    const { flow, calls } = flowFor((_, index) => (index === 0 ? envelope('UNAVAILABLE', 'down', 503) : json({ id: 'issue_1' }, { status: 201 })), { retry: { retryNonIdempotent: true, sleep } })
    await flow.createIssue({ teamId: 't', title: 'x', description: '' })
    expect(calls).toHaveLength(2)
  })

  it('does not retry client errors', async () => {
    const { sleep } = recordingSleep()
    const { flow, calls } = flowFor(() => envelope('NOT_FOUND', 'missing', 404), { retry: { sleep } })
    await failure(flow.issue('ENG-1'))
    expect(calls).toHaveLength(1)
  })

  it('does not wait for a Retry-After longer than maxDelayMs', async () => {
    const { sleep, delays } = recordingSleep()
    const { flow, calls } = flowFor(() => envelope('RATELIMITED', 'slow', 429, {}, { 'Retry-After': '3600' }), { retry: { maxDelayMs: 60_000, sleep } })
    expect(await failure(flow.issue('ENG-1'))).toBeInstanceOf(RatelimitedFlowError)
    expect(calls).toHaveLength(1)
    expect(delays).toEqual([])
  })

  it('retries network errors of idempotent requests', async () => {
    const { sleep } = recordingSleep()
    let attempts = 0
    const mock = mockFetch(() => json({ id: 'issue_1' }))
    const flow = new FlowClient(
      { apiKey: 'k', apiUrl: API_URL, retry: { sleep } },
      {
        fetch: async (input, init) => {
          if (attempts++ === 0) throw new TypeError('fetch failed')
          return mock.fetch(input, init)
        },
      },
    )
    expect((await flow.issue('ENG-1')).id).toBe('issue_1')
    expect(attempts).toBe(2)
  })

  it('supports a per-request retry override', async () => {
    const { sleep } = recordingSleep()
    const { flow, calls } = flowFor(() => envelope('UNAVAILABLE', 'down', 503), { retry: { sleep } })
    await failure(flow.issue('ENG-1', { retries: 0 }))
    expect(calls).toHaveLength(1)
  })

  it('accepts a number as retry count', async () => {
    const { flow, calls } = flowFor(() => envelope('UNAVAILABLE', 'down', 503), { retry: 1, timeout: 0 })
    // real sleeping: keep the delay tiny by relying on the default 500 ms base only once
    await failure(flow.issue('ENG-1'))
    expect(calls).toHaveLength(2)
  })
})

describe('network, timeout and abort', () => {
  it('wraps fetch failures', async () => {
    const flow = new FlowClient({ apiKey: 'k', apiUrl: API_URL, fetch: async () => { throw new TypeError('fetch failed') } })
    const error = await failure(flow.issue('ENG-1'))
    expect(error).toBeInstanceOf(NetworkFlowError)
    expect(error.type).toBe('NETWORK_ERROR')
    expect(error.cause).toBeInstanceOf(TypeError)
  })

  it('times out slow requests', async () => {
    const { flow } = flowFor(() => new Promise<Response>(() => {}), { timeout: 20 })
    const error = await failure(flow.issue('ENG-1'))
    expect(error).toBeInstanceOf(TimeoutFlowError)
    expect(error).toBeInstanceOf(NetworkFlowError)
    expect(error.type).toBe('TIMEOUT')
  })

  it('lets a request override the client timeout', async () => {
    const { flow } = flowFor(() => new Promise<Response>(() => {}), { timeout: 60_000 })
    expect(await failure(flow.issue('ENG-1', { timeout: 10 }))).toBeInstanceOf(TimeoutFlowError)
  })

  it('aborts with the caller signal and never retries an abort', async () => {
    const { sleep } = recordingSleep()
    const { flow, calls } = flowFor(() => new Promise<Response>(() => {}), { retry: { sleep } })
    const controller = new AbortController()
    const pending = failure(flow.issue('ENG-1', { signal: controller.signal }))
    setTimeout(() => controller.abort(), 10)
    const error = await pending
    expect(error).toBeInstanceOf(AbortFlowError)
    expect(error.type).toBe('ABORTED')
    expect(calls).toHaveLength(1)
  })

  it('rejects at once with an already aborted signal', async () => {
    const { flow } = flowFor(() => json({}))
    const controller = new AbortController()
    controller.abort()
    expect(await failure(flow.issue('ENG-1', { signal: controller.signal }))).toBeInstanceOf(AbortFlowError)
  })
})

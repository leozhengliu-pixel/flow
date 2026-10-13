import { vi } from 'vitest'
import type { FetchLike } from '../src/index.js'

export interface RecordedRequest {
  url: string
  path: string
  query: URLSearchParams
  method: string
  headers: Headers
  /** Request body as text (JSON bodies, form bodies); binary bodies are in `bytes`. */
  text?: string
  bytes?: Uint8Array
  json?: any
  signal?: AbortSignal
}

export type Responder = (request: RecordedRequest, index: number) => Response | Promise<Response>

export function json(body: unknown, init: { status?: number; headers?: Record<string, string> } = {}): Response {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
    headers: { 'Content-Type': 'application/json', ...init.headers },
  })
}

export function envelope(type: string, message: string, status: number, extra: Record<string, unknown> = {}, headers: Record<string, string> = {}): Response {
  return json({ error: message, code: (extra.code as string) ?? type, type, requestId: 'req_123', ...extra }, { status, headers: { 'X-Request-ID': 'req_123', ...headers } })
}

/** A fetch double that records every request and answers through `responder`. */
export function mockFetch(responder: Responder) {
  const calls: RecordedRequest[] = []
  const fetch = vi.fn(async (input: string, init: RequestInit = {}) => {
    const url = new URL(input)
    const headers = new Headers(init.headers)
    const record: RecordedRequest = {
      url: input,
      path: url.pathname,
      query: url.searchParams,
      method: init.method ?? 'GET',
      headers,
      signal: init.signal ?? undefined,
    }
    const body = init.body
    if (typeof body === 'string') {
      record.text = body
      if ((headers.get('content-type') ?? '').includes('json')) record.json = JSON.parse(body)
    } else if (body instanceof Uint8Array) record.bytes = body
    else if (body instanceof ArrayBuffer) record.bytes = new Uint8Array(body)
    else if (typeof Blob !== 'undefined' && body instanceof Blob) record.bytes = new Uint8Array(await body.arrayBuffer())
    calls.push(record)
    const index = calls.length - 1
    const aborted = new Promise<never>((_, reject) => {
      if (init.signal?.aborted) reject(new DOMException('aborted', 'AbortError'))
      init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true })
    })
    return Promise.race([Promise.resolve(responder(record, index)), aborted])
  })
  return { fetch: fetch as unknown as FetchLike, calls, mock: fetch }
}

export const API_URL = 'https://flow.example.com'

/** A sleep that records the requested delays instead of waiting. */
export function recordingSleep() {
  const delays: number[] = []
  return { delays, sleep: async (ms: number) => void delays.push(ms) }
}

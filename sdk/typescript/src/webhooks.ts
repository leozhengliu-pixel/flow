import { createHmac, timingSafeEqual } from 'node:crypto'
import type { UnknownWebhookEvent, WebhookEvent, WebhookEventOf, WebhookEventType } from './webhook-events.js'

export type * from './webhook-events.js'

export const FLOW_SIGNATURE_HEADER = 'flow-signature'
export const FLOW_DELIVERY_HEADER = 'flow-delivery'
export const FLOW_EVENT_HEADER = 'flow-event'
export const FLOW_TIMESTAMP_HEADER = 'flow-timestamp'
export const FLOW_ATTEMPT_HEADER = 'flow-attempt'
export const FLOW_SIGNATURE_STATUS_HEADER = 'flow-signature-status'

/** Deliveries whose `webhookTimestamp` is further than this from your clock are rejected. */
export const DEFAULT_WEBHOOK_TOLERANCE_MS = 60_000

/** Webhook bodies are small; Flow's own limit for receivers is generous at 5 MiB. */
const DEFAULT_MAX_BODY_BYTES = 5 << 20

export type WebhookBody = string | Uint8Array | ArrayBuffer
export type WebhookSecret = string | Uint8Array

export type WebhookVerificationFailure = 'missing_signature' | 'invalid_signature' | 'invalid_payload' | 'missing_timestamp' | 'stale_timestamp'

/** Thrown when a delivery fails verification. Answer 401 (400 for `invalid_payload`) and drop it. */
export class WebhookVerificationError extends Error {
  readonly reason: WebhookVerificationFailure
  constructor(reason: WebhookVerificationFailure, message: string) {
    super(message)
    this.name = 'WebhookVerificationError'
    this.reason = reason
    Object.setPrototypeOf(this, new.target.prototype)
  }
}

function toBytes(body: WebhookBody): Uint8Array {
  if (typeof body === 'string') return new TextEncoder().encode(body)
  if (body instanceof ArrayBuffer) return new Uint8Array(body)
  return body
}

/** The hex HMAC-SHA256 of the raw body, as Flow sends it in `Flow-Signature`. Also useful to sign test deliveries. */
export function computeWebhookSignature(rawBody: WebhookBody, secret: WebhookSecret): string {
  return createHmac('sha256', secret).update(toBytes(rawBody)).digest('hex')
}

/**
 * Checks `Flow-Signature` against the raw request body in constant time.
 * Pass the exact bytes received (do not re-serialize parsed JSON). `secret`
 * may be a list to accept the old and the new secret while rotating.
 */
export function verifyWebhookSignature(rawBody: WebhookBody, signature: string | null | undefined, secret: WebhookSecret | readonly WebhookSecret[]): boolean {
  if (typeof signature !== 'string') return false
  const given = signature.trim()
  if (!/^[a-f0-9]{64}$/i.test(given)) return false
  const givenBytes = Buffer.from(given, 'hex')
  const secrets = Array.isArray(secret) ? (secret as readonly WebhookSecret[]) : [secret as WebhookSecret]
  const body = toBytes(rawBody)
  let matched = false
  for (const candidate of secrets) {
    const expected = createHmac('sha256', candidate).update(body).digest()
    // Evaluate every candidate so timing does not reveal which secret matched.
    if (timingSafeEqual(expected, givenBytes)) matched = true
  }
  return matched
}

export interface WebhookTimestampOptions {
  /** Allowed distance from now in milliseconds. Default 60000. */
  toleranceMs?: number
  /** Clock, for tests. */
  now?: () => number
}

/** True when `webhookTimestamp` (Unix milliseconds) is within the tolerance of the current time. */
export function verifyWebhookTimestamp(timestamp: unknown, options: WebhookTimestampOptions = {}): boolean {
  if (typeof timestamp !== 'number' || !Number.isFinite(timestamp)) return false
  const now = options.now ? options.now() : Date.now()
  return Math.abs(now - timestamp) <= (options.toleranceMs ?? DEFAULT_WEBHOOK_TOLERANCE_MS)
}

export interface ConstructEventOptions {
  /** Allowed clock distance in milliseconds (default 60000); `null` skips the replay check. */
  toleranceMs?: number | null
  now?: () => number
}

/**
 * Verifies a delivery and returns its parsed payload: signature of the raw
 * body, then the replay window on the signed `webhookTimestamp` field.
 * Throws `WebhookVerificationError`.
 */
export function constructWebhookEvent(
  rawBody: WebhookBody,
  signature: string | null | undefined,
  secret: WebhookSecret | readonly WebhookSecret[],
  options: ConstructEventOptions = {},
): WebhookEvent | UnknownWebhookEvent {
  if (typeof signature !== 'string' || signature.trim() === '') {
    throw new WebhookVerificationError('missing_signature', 'The Flow-Signature header is missing')
  }
  if (!verifyWebhookSignature(rawBody, signature, secret)) {
    throw new WebhookVerificationError('invalid_signature', 'The webhook signature does not match the request body')
  }
  let event: UnknownWebhookEvent
  try {
    event = JSON.parse(new TextDecoder().decode(toBytes(rawBody))) as UnknownWebhookEvent
  } catch {
    throw new WebhookVerificationError('invalid_payload', 'The webhook body is not valid JSON')
  }
  if (typeof event !== 'object' || event === null || typeof event.type !== 'string') {
    throw new WebhookVerificationError('invalid_payload', 'The webhook body is not an event')
  }
  if (options.toleranceMs !== null) {
    if (typeof event.webhookTimestamp !== 'number') {
      throw new WebhookVerificationError('missing_timestamp', 'The webhook payload has no webhookTimestamp')
    }
    if (!verifyWebhookTimestamp(event.webhookTimestamp, { toleranceMs: options.toleranceMs, now: options.now })) {
      throw new WebhookVerificationError('stale_timestamp', 'The webhook timestamp is outside the allowed window (possible replay)')
    }
  }
  return event as WebhookEvent | UnknownWebhookEvent
}

/** Delivery metadata from the request headers (not signed: do not trust it for decisions). */
export interface WebhookContext {
  /** `Flow-Delivery`: one delivery and its retries. */
  deliveryId?: string
  /** `Flow-Event`: the payload type. */
  eventName?: string
  /** `Flow-Attempt`: 1 for the first attempt, up to 4. */
  attempt?: number
  /** `unsigned` when the webhook has no signing secret. */
  signatureStatus?: string
}

export type WebhookHandler<E> = (event: E, context: WebhookContext) => void | Promise<void>

export interface FlowWebhookClientOptions {
  /** Replay window in milliseconds (default 60000); `null` disables the check. */
  toleranceMs?: number | null
  /** Clock, for tests. */
  now?: () => number
  /**
   * Answer 200 as soon as the delivery is verified and run the handlers
   * afterwards (Flow expects an answer within 5 seconds). Handler errors then
   * go to `onError` instead of failing the delivery. Default false.
   */
  background?: boolean
  /** Receives handler errors and unexpected failures. Defaults to `console.error`. */
  onError?: (error: unknown, event?: WebhookEvent | UnknownWebhookEvent) => void
  /** Reject bodies larger than this many bytes with 413. Default 5 MiB. */
  maxBodyBytes?: number
}

type AnyHandler = WebhookHandler<WebhookEvent | UnknownWebhookEvent>

/** Minimal shape of Node's `IncomingMessage` / an Express request. */
export interface NodeRequestLike {
  method?: string
  headers: Record<string, string | string[] | undefined>
  /** A Buffer / string from `express.raw()`; a parsed object cannot be verified. */
  body?: unknown
  /** Set by body parsers that keep the raw bytes (`express.json({ verify })`). */
  rawBody?: unknown
  readableEnded?: boolean
  on?(event: string, listener: (...args: any[]) => void): unknown
}

/** Minimal shape of Node's `ServerResponse` / an Express response. */
export interface NodeResponseLike {
  statusCode: number
  setHeader(name: string, value: string): unknown
  end(chunk?: string): unknown
}

function headerValue(headers: Record<string, string | string[] | undefined> | Headers, name: string): string | undefined {
  if (typeof (headers as Headers).get === 'function') return (headers as Headers).get(name) ?? undefined
  const record = headers as Record<string, string | string[] | undefined>
  const value = record[name] ?? record[name.toLowerCase()] ?? Object.entries(record).find(([key]) => key.toLowerCase() === name)?.[1]
  return Array.isArray(value) ? value[0] : value
}

function contextFrom(headers: Record<string, string | string[] | undefined> | Headers): WebhookContext {
  const attempt = Number(headerValue(headers, FLOW_ATTEMPT_HEADER))
  return {
    deliveryId: headerValue(headers, FLOW_DELIVERY_HEADER),
    eventName: headerValue(headers, FLOW_EVENT_HEADER),
    attempt: Number.isFinite(attempt) && attempt > 0 ? attempt : undefined,
    signatureStatus: headerValue(headers, FLOW_SIGNATURE_STATUS_HEADER),
  }
}

function statusFor(error: WebhookVerificationError): number {
  return error.reason === 'invalid_payload' ? 400 : 401
}

/**
 * Receives Flow webhooks: verifies signature and replay window, parses the
 * payload and calls your handlers.
 *
 * ```ts
 * const webhooks = new FlowWebhookClient(process.env.FLOW_WEBHOOK_SECRET!)
 * webhooks.on('Issue', async (event) => {
 *   if (event.action === 'create') await notify(event.data.identifier)
 * })
 * app.post('/hooks/flow', express.raw({ type: 'application/json' }), webhooks.createNodeHandler())
 * // or: export const POST = webhooks.createFetchHandler()
 * ```
 */
export class FlowWebhookClient {
  private readonly secrets: readonly WebhookSecret[]
  private readonly options: FlowWebhookClientOptions
  private readonly handlers = new Map<string, AnyHandler[]>()

  /** @param secret the signing secret; pass both secrets while rotating one */
  constructor(secret: WebhookSecret | readonly WebhookSecret[], options: FlowWebhookClientOptions = {}) {
    const list = (Array.isArray(secret) ? secret : [secret]) as readonly WebhookSecret[]
    if (list.length === 0 || list.some(item => item.length === 0)) throw new TypeError('FlowWebhookClient needs a non-empty signing secret')
    this.secrets = list
    this.options = options
  }

  /** Checks the signature only (no replay check). */
  verify(rawBody: WebhookBody, signature: string | null | undefined): boolean {
    return verifyWebhookSignature(rawBody, signature, this.secrets)
  }

  /** Verifies signature and replay window and returns the parsed event. Throws `WebhookVerificationError`. */
  constructEvent(rawBody: WebhookBody, signature: string | null | undefined): WebhookEvent | UnknownWebhookEvent {
    return constructWebhookEvent(rawBody, signature, this.secrets, { toleranceMs: this.options.toleranceMs, now: this.options.now })
  }

  /** Registers a handler for one payload type, or `'*'` for every delivery (including types this SDK does not know). */
  on<T extends WebhookEventType>(type: T, handler: WebhookHandler<WebhookEventOf<T>>): this
  on(type: '*', handler: WebhookHandler<WebhookEvent | UnknownWebhookEvent>): this
  on(type: string, handler: WebhookHandler<never>): this {
    const list = this.handlers.get(type) ?? []
    list.push(handler as unknown as AnyHandler)
    this.handlers.set(type, list)
    return this
  }

  /** Removes a handler registered with `on`. */
  off(type: WebhookEventType | '*', handler: WebhookHandler<never>): this {
    const list = this.handlers.get(type)?.filter(item => item !== (handler as unknown as AnyHandler))
    if (list) this.handlers.set(type, list)
    return this
  }

  /** Runs the handlers for an already verified event: type handlers in registration order, then `'*'` handlers. */
  async dispatch(event: WebhookEvent | UnknownWebhookEvent, context: WebhookContext = {}): Promise<void> {
    for (const handler of [...(this.handlers.get(event.type) ?? []), ...(this.handlers.get('*') ?? [])]) {
      await handler(event, context)
    }
  }

  private fail(error: unknown, event?: WebhookEvent | UnknownWebhookEvent): void {
    if (this.options.onError) this.options.onError(error, event)
    else console.error('Flow webhook handler failed', error)
  }

  /**
   * A `(request: Request) => Promise<Response>` handler for fetch-style
   * runtimes: Next.js route handlers, Bun, Deno, Cloudflare Workers, Hono.
   */
  createFetchHandler(): (request: Request) => Promise<Response> {
    const limit = this.options.maxBodyBytes ?? DEFAULT_MAX_BODY_BYTES
    return async request => {
      if (request.method !== 'POST') return new Response('Method Not Allowed', { status: 405, headers: { Allow: 'POST' } })
      const declared = Number(request.headers.get('content-length'))
      if (Number.isFinite(declared) && declared > limit) return new Response('Payload Too Large', { status: 413 })
      const raw = new Uint8Array(await request.arrayBuffer())
      if (raw.byteLength > limit) return new Response('Payload Too Large', { status: 413 })
      let event: WebhookEvent | UnknownWebhookEvent
      try {
        event = this.constructEvent(raw, request.headers.get(FLOW_SIGNATURE_HEADER))
      } catch (error) {
        if (error instanceof WebhookVerificationError) return new Response(error.message, { status: statusFor(error) })
        throw error
      }
      const context = contextFrom(request.headers)
      if (this.options.background) {
        void this.dispatch(event, context).catch(error => this.fail(error, event))
        return new Response('ok', { status: 200 })
      }
      try {
        await this.dispatch(event, context)
      } catch (error) {
        this.fail(error, event)
        return new Response('handler failed', { status: 500 })
      }
      return new Response('ok', { status: 200 })
    }
  }

  /**
   * A `(req, res, next?)` handler for Node's `http` server and Express. The
   * route must give it the raw body: `express.raw({ type: 'application/json' })`
   * before it, or no body parser at all (the handler then reads the stream).
   */
  createNodeHandler(): (req: NodeRequestLike, res: NodeResponseLike, next?: (error?: unknown) => void) => Promise<void> {
    const limit = this.options.maxBodyBytes ?? DEFAULT_MAX_BODY_BYTES
    const reply = (res: NodeResponseLike, status: number, text: string) => {
      res.statusCode = status
      res.setHeader('Content-Type', 'text/plain; charset=utf-8')
      res.end(text)
    }
    return async (req, res, next) => {
      try {
        if (req.method && req.method !== 'POST') {
          res.setHeader('Allow', 'POST')
          return reply(res, 405, 'Method Not Allowed')
        }
        const raw = await readNodeBody(req, limit)
        if (raw === 'too_large') return reply(res, 413, 'Payload Too Large')
        if (raw === undefined) {
          this.fail(new Error('The raw request body is unavailable: register express.raw({ type: "application/json" }) before the Flow webhook handler'))
          return reply(res, 500, 'raw body unavailable')
        }
        let event: WebhookEvent | UnknownWebhookEvent
        try {
          event = this.constructEvent(raw, headerValue(req.headers, FLOW_SIGNATURE_HEADER))
        } catch (error) {
          if (error instanceof WebhookVerificationError) return reply(res, statusFor(error), error.message)
          throw error
        }
        const context = contextFrom(req.headers)
        if (this.options.background) {
          reply(res, 200, 'ok')
          void this.dispatch(event, context).catch(error => this.fail(error, event))
          return
        }
        try {
          await this.dispatch(event, context)
        } catch (error) {
          this.fail(error, event)
          return reply(res, 500, 'handler failed')
        }
        reply(res, 200, 'ok')
      } catch (error) {
        if (next) next(error)
        else {
          this.fail(error)
          reply(res, 500, 'internal error')
        }
      }
    }
  }
}

async function readNodeBody(req: NodeRequestLike, limit: number): Promise<WebhookBody | 'too_large' | undefined> {
  for (const candidate of [req.rawBody, req.body]) {
    if (typeof candidate === 'string') return candidate.length > limit ? 'too_large' : candidate
    if (candidate instanceof Uint8Array) return candidate.byteLength > limit ? 'too_large' : candidate
    if (candidate instanceof ArrayBuffer) return candidate.byteLength > limit ? 'too_large' : new Uint8Array(candidate)
  }
  // A parsed body (express.json) or an already consumed stream cannot be verified.
  if (req.body !== undefined || req.readableEnded || typeof req.on !== 'function') return undefined
  return new Promise(resolve => {
    const chunks: Uint8Array[] = []
    let size = 0
    let done = false
    const finish = (value: WebhookBody | 'too_large' | undefined) => {
      if (done) return
      done = true
      resolve(value)
    }
    req.on!('data', (chunk: Uint8Array | string) => {
      const bytes = typeof chunk === 'string' ? new TextEncoder().encode(chunk) : chunk
      size += bytes.byteLength
      if (size > limit) return finish('too_large')
      chunks.push(bytes)
    })
    req.on!('end', () => {
      const out = new Uint8Array(size)
      let offset = 0
      for (const chunk of chunks) {
        out.set(chunk, offset)
        offset += chunk.byteLength
      }
      finish(out)
    })
    req.on!('error', () => finish(undefined))
  })
}

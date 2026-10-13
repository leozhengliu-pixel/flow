import { EventEmitter } from 'node:events'
import { describe, expect, it, vi } from 'vitest'
import {
  FlowWebhookClient,
  WebhookVerificationError,
  computeWebhookSignature,
  constructWebhookEvent,
  verifyWebhookSignature,
  verifyWebhookTimestamp,
  type WebhookEvent,
} from '../src/webhooks.js'

const secret = 'whsec_test_secret'
const NOW = 1_791_822_527_821

function payload(overrides: Record<string, unknown> = {}) {
  return {
    action: 'update',
    type: 'Issue',
    actor: { id: 'usr_7f2c', type: 'user', name: 'Ada Lovelace', email: 'ada@example.com', url: 'https://flow.example.com/acme/profiles/ada' },
    createdAt: '2026-10-12T16:28:47.818Z',
    data: { id: 'issue_105131', identifier: 'ENG-42', title: 'Retry failed payments', priority: 2 },
    url: 'https://flow.example.com/acme/issue/ENG-42/retry-failed-payments',
    updatedFrom: { title: 'Retry payments' },
    organizationId: 'workspace_acme',
    webhookId: 'webhook_1',
    eventId: 'evt_1',
    eventType: 'issue.updated',
    webhookTimestamp: NOW,
    ...overrides,
  }
}

function signed(body: object | string = payload(), key: string = secret) {
  const raw = typeof body === 'string' ? body : JSON.stringify(body)
  return { raw, signature: computeWebhookSignature(raw, key) }
}

describe('verifyWebhookSignature', () => {
  it('matches the documented algorithm: hex HMAC-SHA256 of the raw body', async () => {
    const { createHmac } = await import('node:crypto')
    const raw = '{"a":1}'
    expect(computeWebhookSignature(raw, secret)).toBe(createHmac('sha256', secret).update(raw).digest('hex'))
    expect(computeWebhookSignature(raw, secret)).toMatch(/^[a-f0-9]{64}$/)
  })

  it('accepts a valid signature for strings, bytes and buffers', () => {
    const { raw, signature } = signed()
    expect(verifyWebhookSignature(raw, signature, secret)).toBe(true)
    expect(verifyWebhookSignature(new TextEncoder().encode(raw), signature, secret)).toBe(true)
    expect(verifyWebhookSignature(Buffer.from(raw), signature.toUpperCase(), secret)).toBe(true)
    expect(verifyWebhookSignature(new TextEncoder().encode(raw).buffer as ArrayBuffer, signature, new TextEncoder().encode(secret))).toBe(true)
  })

  it('rejects a modified body, a wrong secret and a malformed signature', () => {
    const { raw, signature } = signed()
    expect(verifyWebhookSignature(raw + ' ', signature, secret)).toBe(false)
    expect(verifyWebhookSignature(raw, signature, 'other')).toBe(false)
    expect(verifyWebhookSignature(raw, signature.slice(1), secret)).toBe(false)
    expect(verifyWebhookSignature(raw, 'zz'.repeat(32), secret)).toBe(false)
    expect(verifyWebhookSignature(raw, '', secret)).toBe(false)
    expect(verifyWebhookSignature(raw, undefined, secret)).toBe(false)
    expect(verifyWebhookSignature(raw, null, secret)).toBe(false)
  })

  it('does not accept a re-serialised body', () => {
    const { raw, signature } = signed(JSON.stringify(payload(), null, 2))
    expect(verifyWebhookSignature(JSON.stringify(JSON.parse(raw)), signature, secret)).toBe(false)
  })

  it('accepts either secret while rotating', () => {
    const { raw, signature } = signed(payload(), 'old-secret')
    expect(verifyWebhookSignature(raw, signature, ['new-secret', 'old-secret'])).toBe(true)
    expect(verifyWebhookSignature(raw, signature, ['new-secret'])).toBe(false)
  })
})

describe('replay window', () => {
  it('accepts timestamps within 60 seconds either side', () => {
    expect(verifyWebhookTimestamp(NOW, { now: () => NOW })).toBe(true)
    expect(verifyWebhookTimestamp(NOW - 60_000, { now: () => NOW })).toBe(true)
    expect(verifyWebhookTimestamp(NOW + 60_000, { now: () => NOW })).toBe(true)
  })

  it('rejects stale, future and invalid timestamps', () => {
    expect(verifyWebhookTimestamp(NOW - 60_001, { now: () => NOW })).toBe(false)
    expect(verifyWebhookTimestamp(NOW + 60_001, { now: () => NOW })).toBe(false)
    expect(verifyWebhookTimestamp('1791822527821', { now: () => NOW })).toBe(false)
    expect(verifyWebhookTimestamp(undefined)).toBe(false)
    expect(verifyWebhookTimestamp(Number.NaN)).toBe(false)
  })

  it('supports a custom tolerance', () => {
    expect(verifyWebhookTimestamp(NOW - 100_000, { now: () => NOW, toleranceMs: 120_000 })).toBe(true)
    expect(verifyWebhookTimestamp(NOW - 5_000, { now: () => NOW, toleranceMs: 1_000 })).toBe(false)
  })
})

describe('constructWebhookEvent', () => {
  const now = () => NOW + 5_000

  it('returns the parsed event', () => {
    const { raw, signature } = signed()
    const event = constructWebhookEvent(raw, signature, secret, { now }) as WebhookEvent
    expect(event.type).toBe('Issue')
    expect(event.webhookTimestamp).toBe(NOW)
  })

  function reason(fn: () => unknown): string | undefined {
    try {
      fn()
    } catch (error) {
      expect(error).toBeInstanceOf(WebhookVerificationError)
      return (error as WebhookVerificationError).reason
    }
    return undefined
  }

  it('reports why a delivery was rejected', () => {
    const { raw, signature } = signed()
    expect(reason(() => constructWebhookEvent(raw, undefined, secret, { now }))).toBe('missing_signature')
    expect(reason(() => constructWebhookEvent(raw, signature, 'wrong', { now }))).toBe('invalid_signature')
    expect(reason(() => constructWebhookEvent(raw, signature, secret, { now: () => NOW + 61_000 }))).toBe('stale_timestamp')
    const noTimestamp = signed(payload({ webhookTimestamp: undefined }))
    expect(reason(() => constructWebhookEvent(noTimestamp.raw, noTimestamp.signature, secret, { now }))).toBe('missing_timestamp')
    const notJson = signed('not json')
    expect(reason(() => constructWebhookEvent(notJson.raw, notJson.signature, secret, { now }))).toBe('invalid_payload')
    const notEvent = signed('[1,2]')
    expect(reason(() => constructWebhookEvent(notEvent.raw, notEvent.signature, secret, { now }))).toBe('invalid_payload')
  })

  it('checks the signed body timestamp, not the Flow-Timestamp header', () => {
    // A replayed body keeps its old webhookTimestamp whatever header the attacker sends.
    const { raw, signature } = signed(payload({ webhookTimestamp: NOW - 3_600_000 }))
    expect(reason(() => constructWebhookEvent(raw, signature, secret, { now }))).toBe('stale_timestamp')
  })

  it('can skip the replay check', () => {
    const { raw, signature } = signed(payload({ webhookTimestamp: 1 }))
    expect(constructWebhookEvent(raw, signature, secret, { toleranceMs: null, now }).type).toBe('Issue')
  })
})

describe('FlowWebhookClient', () => {
  const now = () => NOW

  it('verifies and dispatches typed handlers, then wildcard handlers', async () => {
    const webhooks = new FlowWebhookClient(secret, { now })
    const order: string[] = []
    webhooks.on('Issue', event => {
      // `event` is narrowed to the Issue event
      order.push(`issue:${event.action}:${event.data.identifier}`)
    })
    webhooks.on('Comment', () => void order.push('comment'))
    webhooks.on('*', event => void order.push(`any:${event.type}`))
    const { raw, signature } = signed()
    await webhooks.dispatch(webhooks.constructEvent(raw, signature))
    expect(order).toEqual(['issue:update:ENG-42', 'any:Issue'])
  })

  it('verify() checks the signature only', () => {
    const webhooks = new FlowWebhookClient([secret, 'older'], { now })
    expect(webhooks.verify(...Object.values(signed()) as [string, string])).toBe(true)
    const old = signed(payload(), 'older')
    expect(webhooks.verify(old.raw, old.signature)).toBe(true)
    expect(webhooks.verify(old.raw, 'nope')).toBe(false)
  })

  it('requires a secret', () => {
    expect(() => new FlowWebhookClient('')).toThrow(TypeError)
    expect(() => new FlowWebhookClient([])).toThrow(TypeError)
  })

  it('removes handlers with off()', async () => {
    const webhooks = new FlowWebhookClient(secret, { now })
    const handler = vi.fn()
    webhooks.on('Issue', handler).off('Issue', handler)
    const { raw, signature } = signed()
    await webhooks.dispatch(webhooks.constructEvent(raw, signature))
    expect(handler).not.toHaveBeenCalled()
  })

  it('passes unknown event types to wildcard handlers', async () => {
    const webhooks = new FlowWebhookClient(secret, { now })
    const seen: string[] = []
    webhooks.on('*', event => void seen.push(event.type))
    const { raw, signature } = signed(payload({ type: 'SomethingNew', data: { id: 'x' } }))
    await webhooks.dispatch(webhooks.constructEvent(raw, signature))
    expect(seen).toEqual(['SomethingNew'])
  })

  describe('fetch handler', () => {
    function request(raw: string, headers: Record<string, string> = {}, method = 'POST') {
      return new Request('https://app.example.com/hooks/flow', { method, body: method === 'POST' ? raw : undefined, headers: { 'Content-Type': 'application/json', ...headers } })
    }

    it('answers 200 after running handlers, with delivery context from the headers', async () => {
      const webhooks = new FlowWebhookClient(secret, { now })
      const seen: unknown[] = []
      webhooks.on('Issue', (event, context) => void seen.push([event.eventId, context]))
      const { raw, signature } = signed()
      const response = await webhooks.createFetchHandler()(request(raw, { 'Flow-Signature': signature, 'Flow-Delivery': 'whd_1', 'Flow-Event': 'Issue', 'Flow-Attempt': '2' }))
      expect(response.status).toBe(200)
      expect(seen).toEqual([['evt_1', { deliveryId: 'whd_1', eventName: 'Issue', attempt: 2, signatureStatus: undefined }]])
    })

    it('answers 401 for a bad or missing signature and 401 for a replay, without running handlers', async () => {
      const webhooks = new FlowWebhookClient(secret, { now })
      const handler = vi.fn()
      webhooks.on('*', handler)
      const fetchHandler = webhooks.createFetchHandler()
      const { raw, signature } = signed()
      expect((await fetchHandler(request(raw, { 'Flow-Signature': 'a'.repeat(64) }))).status).toBe(401)
      expect((await fetchHandler(request(raw))).status).toBe(401)
      const stale = signed(payload({ webhookTimestamp: NOW - 120_000 }))
      expect((await fetchHandler(request(stale.raw, { 'Flow-Signature': stale.signature }))).status).toBe(401)
      const bad = signed('not json')
      expect((await fetchHandler(request(bad.raw, { 'Flow-Signature': bad.signature }))).status).toBe(400)
      expect(handler).not.toHaveBeenCalled()
      expect(signature).toBeTruthy()
    })

    it('rejects other methods and large bodies', async () => {
      const webhooks = new FlowWebhookClient(secret, { now, maxBodyBytes: 10 })
      const fetchHandler = webhooks.createFetchHandler()
      expect((await fetchHandler(request('', {}, 'GET'))).status).toBe(405)
      expect((await fetchHandler(request('x'.repeat(50)))).status).toBe(413)
    })

    it('answers 500 when a handler throws so Flow retries', async () => {
      const onError = vi.fn()
      const webhooks = new FlowWebhookClient(secret, { now, onError })
      webhooks.on('Issue', () => {
        throw new Error('database down')
      })
      const { raw, signature } = signed()
      expect((await webhooks.createFetchHandler()(request(raw, { 'Flow-Signature': signature }))).status).toBe(500)
      expect(onError).toHaveBeenCalledOnce()
    })

    it('answers before the handlers finish in background mode', async () => {
      const onError = vi.fn()
      const webhooks = new FlowWebhookClient(secret, { now, background: true, onError })
      let finished = false
      let release!: () => void
      webhooks.on('Issue', () => new Promise<void>(resolve => (release = () => ((finished = true), resolve()))))
      webhooks.on('Comment', () => {
        throw new Error('ignored')
      })
      const { raw, signature } = signed()
      const response = await webhooks.createFetchHandler()(request(raw, { 'Flow-Signature': signature }))
      expect(response.status).toBe(200)
      expect(finished).toBe(false)
      release()
      await new Promise(resolve => setTimeout(resolve, 0))
      expect(finished).toBe(true)
      expect(onError).not.toHaveBeenCalled()
    })
  })

  describe('node handler', () => {
    function response() {
      const res = { statusCode: 0, headers: {} as Record<string, string>, body: '', setHeader(name: string, value: string) { this.headers[name] = value }, end(chunk?: string) { this.body = chunk ?? '' } }
      return res
    }

    it('verifies an express.raw() body (Buffer)', async () => {
      const webhooks = new FlowWebhookClient(secret, { now })
      const handler = vi.fn()
      webhooks.on('Issue', handler)
      const { raw, signature } = signed()
      const res = response()
      await webhooks.createNodeHandler()({ method: 'POST', headers: { 'flow-signature': signature, 'flow-delivery': 'whd_9' }, body: Buffer.from(raw) }, res)
      expect(res.statusCode).toBe(200)
      expect(handler).toHaveBeenCalledOnce()
      expect(handler.mock.calls[0][1].deliveryId).toBe('whd_9')
    })

    it('reads the stream when no body parser ran', async () => {
      const webhooks = new FlowWebhookClient(secret, { now })
      const handler = vi.fn()
      webhooks.on('Issue', handler)
      const { raw, signature } = signed()
      const req = Object.assign(new EventEmitter(), { method: 'POST', headers: { 'Flow-Signature': signature } })
      const res = response()
      const pending = webhooks.createNodeHandler()(req, res)
      const bytes = Buffer.from(raw)
      req.emit('data', bytes.subarray(0, 20))
      req.emit('data', bytes.subarray(20))
      req.emit('end')
      await pending
      expect(res.statusCode).toBe(200)
      expect(handler).toHaveBeenCalledOnce()
    })

    it('uses rawBody saved by a JSON parser', async () => {
      const webhooks = new FlowWebhookClient(secret, { now })
      const { raw, signature } = signed()
      const res = response()
      await webhooks.createNodeHandler()({ method: 'POST', headers: { 'flow-signature': signature }, body: JSON.parse(raw), rawBody: Buffer.from(raw) }, res)
      expect(res.statusCode).toBe(200)
    })

    it('fails loudly when only a parsed body is available', async () => {
      const onError = vi.fn()
      const webhooks = new FlowWebhookClient(secret, { now, onError })
      const { raw, signature } = signed()
      const res = response()
      await webhooks.createNodeHandler()({ method: 'POST', headers: { 'flow-signature': signature }, body: JSON.parse(raw) }, res)
      expect(res.statusCode).toBe(500)
      expect(onError).toHaveBeenCalledOnce()
    })

    it('answers 401 for an invalid signature, 405 for GET, 413 for large streams', async () => {
      const webhooks = new FlowWebhookClient(secret, { now, maxBodyBytes: 2000 })
      const handler = webhooks.createNodeHandler()
      const { raw } = signed()
      const bad = response()
      await handler({ method: 'POST', headers: { 'flow-signature': 'b'.repeat(64) }, body: Buffer.from(raw) }, bad)
      expect(bad.statusCode).toBe(401)
      const get = response()
      await handler({ method: 'GET', headers: {} }, get)
      expect(get.statusCode).toBe(405)
      expect(get.headers.Allow).toBe('POST')
      const large = response()
      await handler({ method: 'POST', headers: {}, body: Buffer.alloc(5000) }, large)
      expect(large.statusCode).toBe(413)
    })

    it('hands unexpected errors to next()', async () => {
      const webhooks = new FlowWebhookClient(secret, { now })
      const next = vi.fn()
      const res = response()
      res.setHeader = () => {
        throw new Error('socket closed')
      }
      await webhooks.createNodeHandler()({ method: 'POST', headers: {}, body: Buffer.from('x') }, res, next)
      expect(next).toHaveBeenCalled()
    })
  })
})

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { backoffDelay } from '../src/backoff.js'
import { EventBuffer } from '../src/event-buffer.js'
import type { RunnerEvent } from '../src/protocol.js'
import { AuthError, RuntimeClient, RuntimeError, isRetryableStatus } from '../src/runtime-client.js'
import { json, startServer } from './helpers.js'

// Offline buffering (bounded, thoughts dropped first, idempotent retries) and
// the runtime client's retry and auth behaviour.

function event(id: string, type: RunnerEvent['type'] = 'action', body = '', ephemeral?: boolean): RunnerEvent {
  return { id, type, body, ephemeral, createdAt: '2026-10-18T00:00:00.000Z' }
}

const noSleep = async () => {}

test('batches events and retries the same ids while Flow is unreachable', async () => {
  const posted: string[][] = []
  let failures = 3
  const buffer = new EventBuffer({
    post: async (events) => {
      if (failures-- > 0) throw new RuntimeError('ECONNREFUSED', 0, true)
      posted.push(events.map((e) => e.id))
    },
    sleep: noSleep,
    flushIntervalMs: 1,
  })
  for (let i = 1; i <= 5; i++) buffer.push(event(`e${i}`))
  assert.equal(await buffer.flush(2000), true)
  assert.deepEqual(posted.flat(), ['e1', 'e2', 'e3', 'e4', 'e5'])
  assert.equal(buffer.pending, 0)
})

test('sends at most 100 events per request', async () => {
  const sizes: number[] = []
  const buffer = new EventBuffer({ post: async (events) => void sizes.push(events.length), sleep: noSleep })
  for (let i = 0; i < 250; i++) buffer.push(event(`e${i}`))
  await buffer.flush(2000)
  assert.ok(sizes.every((size) => size <= 100))
  assert.equal(sizes.reduce((a, b) => a + b, 0), 250)
})

test('overflow drops the oldest thoughts first, then ephemeral events, then the oldest', async () => {
  let release!: () => void
  const gate = new Promise<void>((resolve) => (release = resolve))
  const posted: string[] = []
  let first = true
  const buffer = new EventBuffer({
    post: async (events) => {
      if (first) {
        first = false
        await gate
        throw new RuntimeError('down', 503, true)
      }
      posted.push(...events.map((e) => e.id))
    },
    sleep: noSleep,
    maxBytes: 1200,
    batchSize: 1,
  })
  buffer.push(event('inflight', 'action', 'x'.repeat(10)))
  await new Promise((resolve) => setImmediate(resolve))
  buffer.push(event('t1', 'thought', 'x'.repeat(200)))
  buffer.push(event('a1', 'action', 'x'.repeat(200)))
  buffer.push(event('eph', 'action', 'x'.repeat(200), true))
  buffer.push(event('t2', 'thought', 'x'.repeat(200)))
  buffer.push(event('r1', 'response', 'x'.repeat(200)))
  buffer.push(event('a2', 'action', 'x'.repeat(200)))
  assert.ok(buffer.pendingBytes <= 1200)
  assert.ok(buffer.droppedCount >= 2)
  release()
  await buffer.flush(2000)
  assert.ok(!posted.includes('t1'), 'oldest thought dropped first')
  assert.ok(posted.includes('inflight'), 'the in-flight batch is never evicted')
  assert.ok(posted.includes('r1') && posted.includes('a2'), 'newest non-thought events survive')
})

test('a rejected batch (4xx) is dropped so it cannot block the queue; 401 is fatal', async () => {
  const posted: string[] = []
  let calls = 0
  const buffer = new EventBuffer({
    post: async (events) => {
      if (calls++ === 0) throw new RuntimeError('bad', 400, false)
      posted.push(...events.map((e) => e.id))
    },
    sleep: noSleep,
    batchSize: 1,
  })
  buffer.push(event('bad'))
  await buffer.flush(2000)
  buffer.push(event('good'))
  await buffer.flush(2000)
  assert.deepEqual(posted, ['good'])

  let fatal: Error | undefined
  const revoked = new EventBuffer({ post: async () => Promise.reject(new AuthError(401)), sleep: noSleep, onFatal: (error) => (fatal = error) })
  revoked.push(event('x'))
  assert.equal(await revoked.flush(200), false)
  assert.ok(fatal instanceof AuthError)
})

test('backoff is exponential, jittered and capped', () => {
  assert.equal(backoffDelay(0, 1000, 30_000, () => 0), 500)
  assert.equal(backoffDelay(0, 1000, 30_000, () => 1), 1000)
  assert.equal(backoffDelay(3, 1000, 30_000, () => 1), 8000)
  assert.equal(backoffDelay(20, 1000, 30_000, () => 1), 30_000)
  assert.deepEqual([408, 425, 429, 500, 502, 503, 501, 400, 404].map(isRetryableStatus), [true, true, true, true, true, true, false, false, false])
})

test('runtime client: auth header, retries on 5xx, 204 for warm bootstrap, AuthError on 401', async () => {
  let failures = 2
  let bound = false
  const server = await startServer(3, (req, _body, res) => {
    if (req.headers.authorization !== 'Bearer fcs_tok') return json(res, 401, { error: 'revoked' })
    if (req.url?.startsWith('/internal/coding-runtime/bootstrap')) {
      if (!bound) {
        bound = true
        res.writeHead(204)
        res.end()
        return
      }
      return json(res, 200, { sandboxId: 'sbx', mode: 'session' })
    }
    if (req.url === '/internal/coding-runtime/credentials') {
      if (failures-- > 0) return json(res, 503, { error: 'busy' })
      return json(res, 200, { username: 'u', password: 'p', expiresAt: '' })
    }
    if (req.url?.startsWith('/internal/coding-runtime/inbox')) return json(res, 200, { items: [{ seq: 1, kind: 'stop' }] })
    json(res, 404, {})
  })
  let token = 'fcs_tok'
  const client = new RuntimeClient({ baseUrl: server.url + '/', token: () => token, sleep: async () => {} })
  try {
    assert.equal(await client.bootstrap(60), null)
    assert.equal((await client.bootstrap(60))?.sandboxId, 'sbx')
    assert.equal(server.requests[0].url, '/internal/coding-runtime/bootstrap?wait=60s')
    assert.deepEqual(await client.credentials({ protocol: 'https', host: 'github.com', purpose: 'git' }), { username: 'u', password: 'p', expiresAt: '' })
    assert.deepEqual((await client.inbox(4, 25)).items, [{ seq: 1, kind: 'stop' }])
    assert.ok(server.requests.some((request) => request.url === '/internal/coding-runtime/inbox?after=4&wait=25s'))
    await assert.rejects(client.request('GET', '/internal/coding-runtime/nope', { retry: { attempts: 3 } }), (error: unknown) => error instanceof RuntimeError && error.status === 404 && !error.retryable)
    token = 'fcs_revoked'
    await assert.rejects(client.heartbeat({ phase: 'idle' }), (error: unknown) => error instanceof AuthError)
  } finally {
    await server.close()
  }
})

test('runtime client: network errors are retryable and give up after the attempts', async () => {
  let calls = 0
  const client = new RuntimeClient({
    baseUrl: 'http://127.0.0.1:1',
    token: () => 'fcs_x',
    fetch: async () => {
      calls++
      throw new TypeError('fetch failed')
    },
    sleep: async () => {},
  })
  await assert.rejects(client.request('GET', '/x', { retry: { attempts: 4 } }), (error: unknown) => error instanceof RuntimeError && error.retryable)
  assert.equal(calls, 4)
})

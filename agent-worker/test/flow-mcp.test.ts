import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { test } from 'node:test'
import { newRunContext } from '../src/context.js'
import { allowlisted, approvedMeta, connectorToolTimeoutMs, flowMcpServer, flowToolTimeoutMs, idempotencyKey } from '../src/flow-mcp.js'
import { approvalKind } from '../src/policy.js'

// Connector tools come from Flow's /mcp (api/cmd/server/agent_runtime_connectors.go): one whose connector is set to
// always ask is held for approval, and the approved call carries the approval Flow requires.
test('an always-ask connector tool is held and its approved call carries the approval', async () => {
  const server = flowMcpServer('http://flow.invalid', 'flowrun_test') as any
  const context = newRunContext('s', 'search the code', [])
  const runContext = { context }
  const asking = { name: 'external_ask', inputSchema: { type: 'object' }, _meta: { 'flow/approval': 'always' } }
  const automatic = { name: 'external_auto', inputSchema: { type: 'object' }, _meta: { 'flow/connector': 'Code' } }
  assert.equal(await server.toolFilter({ runContext, agent: {}, serverName: 'flow' }, asking), true)
  assert.equal(await server.toolFilter({ runContext, agent: {}, serverName: 'flow' }, automatic), true)
  assert.equal(approvalKind(context, 'external_ask'), 'connector')
  assert.equal(approvalKind(context, 'external_auto'), null)
  assert.deepEqual(await server.toolMetaResolver({ runContext, serverName: 'flow', toolName: 'external_ask', arguments: {} }), { [approvedMeta]: true })
  assert.equal(await server.toolMetaResolver({ runContext, serverName: 'flow', toolName: 'external_auto', arguments: {} }), undefined)
})

// file_request (a request filed from a Lark or Feishu group) is a Flow session tool that Flow lists as always
// asking: it is held like an always-ask connector tool and its approved call carries the approval.
test('a Flow session tool that always asks is held and its approved call carries the approval', async () => {
  const server = flowMcpServer('http://flow.invalid', 'flowrun_test') as any
  const context = newRunContext('s', 'file this as a request', [])
  const runContext = { context }
  const filing = { name: 'file_request', inputSchema: { type: 'object' }, _meta: { 'flow/approval': 'always' } }
  assert.equal(await server.toolFilter({ runContext, agent: {}, serverName: 'flow' }, filing), true)
  assert.equal(approvalKind(context, 'file_request'), 'flow')
  assert.deepEqual(await server.toolMetaResolver({ runContext, serverName: 'flow', toolName: 'file_request', arguments: {} }), { [approvedMeta]: true })
  assert.equal(await server.toolMetaResolver({ runContext, serverName: 'flow', toolName: 'get_issue', arguments: {} }), undefined)
})

// Only connector calls get the long limit (Flow may hold one for a question to the person); Flow's own tools keep
// 60 s. The SDK has one request limit, set to the longest; each call is cut at its own.
test('connector tools get the long call limit, Flow tools keep 60 s', async () => {
  const defaults = flowMcpServer('http://flow.invalid', 'flowrun_test') as any
  assert.equal(flowToolTimeoutMs, 60_000)
  assert.equal(connectorToolTimeoutMs, 390_000)
  assert.equal(defaults.underlying.timeout, connectorToolTimeoutMs)

  const server = flowMcpServer('http://flow.invalid', 'flowrun_test', { timeoutMs: 30, connectorTimeoutMs: 200 }) as any
  // A stand-in connection: each call answers after `delay` ms unless its signal aborts first.
  const delays: Record<string, number> = { get_issue: 100, external_slow: 100, external_stuck: 1_000 }
  server.underlying = {
    sessionId: undefined,
    callToolResult: (name: string, _args: unknown, _meta: unknown, options?: { signal?: AbortSignal }) =>
      new Promise((resolve, reject) => {
        const timer = setTimeout(() => resolve({ content: [{ type: 'text', text: `${name} done` }], isError: false }), delays[name])
        options?.signal?.addEventListener('abort', () => { clearTimeout(timer); reject(options.signal?.reason ?? new Error('aborted')) })
      }),
  }
  await assert.rejects(server.callToolResult('get_issue', {}), /Request timed out after 0 s/)
  assert.deepEqual((await server.callToolResult('external_slow', {})).content, [{ type: 'text', text: 'external_slow done' }])
  await assert.rejects(server.callToolResult('external_stuck', {}), /Request timed out/)
  // A stop still stops a call (and is not reported as a timeout).
  const stop = new AbortController()
  const stopped = server.callToolResult('external_stuck', {}, undefined, { signal: stop.signal })
  stop.abort(new Error('stopped by the person'))
  await assert.rejects(stopped, /stopped by the person/)
})

test('a run-token allowlist admits connector tools through external_*', () => {
  assert.ok(allowlisted(undefined, 'external_a'))
  assert.ok(!allowlisted(['get_issue'], 'external_a'))
  assert.ok(allowlisted(['get_issue', 'external_*'], 'external_a'))
  assert.ok(!allowlisted(['external_*'], 'save_issue'))
  assert.ok(allowlisted(['external_a'], 'external_a'))
})

// Flow's /mcp answers a repeated write of a run with the first result when the call carries an Idempotency-Key
// (api/cmd/server/mcp.go callMCPRunTool): each call of a run sends "<runId>:<callId>:<tool+arguments digest>".
test('each call of a run carries its Idempotency-Key header, keyed by run, call id, tool and arguments', async () => {
  const seen: { method: string; key?: string; meta?: unknown }[] = []
  const http = createServer((request, response) => {
    let body = ''
    request.on('data', chunk => { body += chunk })
    request.on('end', () => {
      if (request.method !== 'POST') { response.writeHead(405).end(); return }
      const message = JSON.parse(body)
      seen.push({ method: message.method, key: request.headers['idempotency-key'] as string | undefined, meta: message.params?._meta })
      if (message.id === undefined) { response.writeHead(202).end(); return }
      const result = message.method === 'initialize'
        ? { protocolVersion: message.params.protocolVersion, capabilities: { tools: {} }, serverInfo: { name: 'flow', version: 'test' } }
        : message.method === 'tools/list'
          ? { tools: [{ name: 'save_issue', inputSchema: { type: 'object' } }] }
          : { content: [{ type: 'text', text: 'ok' }], isError: false }
      response.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify({ jsonrpc: '2.0', id: message.id, result }))
    })
  })
  await new Promise<void>(resolve => http.listen(0, '127.0.0.1', resolve))
  const port = (http.address() as { port: number }).port
  const server = flowMcpServer(`http://127.0.0.1:${port}`, 'flowrun_test', { runId: 'agent_run_1' }) as any
  try {
    await server.connect()
    const context = newRunContext('s', 'file two bugs', [])
    // Two calls of one turn with the same tool and arguments are two calls (each its own key).
    context.turns.push([
      { callId: 'call_a', name: 'save_issue', arguments: '{"title":"Bug","team":"DEV"}' },
      { callId: 'call_b', name: 'save_issue', arguments: '{"team":"DEV","title":"Bug"}' },
    ])
    const runContext = { context }
    const args = { team: 'DEV', title: 'Bug' }
    const first = await server.toolMetaResolver({ runContext, serverName: 'flow', toolName: 'save_issue', arguments: args })
    const second = await server.toolMetaResolver({ runContext, serverName: 'flow', toolName: 'save_issue', arguments: args })
    assert.equal(first['flow/idempotency-key'], idempotencyKey('agent_run_1', 'call_a', 'save_issue', args))
    assert.equal(second['flow/idempotency-key'], idempotencyKey('agent_run_1', 'call_b', 'save_issue', args))
    // A call the model did not make in a recorded turn goes out without a key (Flow then runs it as before).
    assert.equal(await server.toolMetaResolver({ runContext, serverName: 'flow', toolName: 'save_issue', arguments: { title: 'Other' } }), undefined)
    await server.callToolResult('save_issue', args, first)
    await server.callToolResult('save_issue', args, { ...second, [approvedMeta]: true })
    await server.callToolResult('get_issue', { id: 'DEV-1' })
    const calls = seen.filter(request => request.method === 'tools/call')
    assert.deepEqual(calls.map(call => call.key), [first['flow/idempotency-key'], second['flow/idempotency-key'], undefined])
    assert.match(calls[0].key!, /^agent_run_1:call_a:[0-9a-f]{16}$/)
    // The key is a header only: the call's _meta keeps what Flow reads (the approval) and nothing else.
    assert.equal(calls[0].meta, undefined)
    assert.deepEqual(calls[1].meta, { [approvedMeta]: true })
    // The handshake and the tool list carry no key.
    assert.ok(seen.filter(request => request.method !== 'tools/call').every(request => request.key === undefined))
  } finally {
    await server.close().catch(() => {})
    http.close()
  }
})

test('an Idempotency-Key stays within the 160 characters Flow accepts', () => {
  const key = idempotencyKey('agent_run_'.padEnd(120, 'x'), 'call_'.padEnd(80, 'y'), 'save_issue', { title: 'Bug' })
  assert.ok(key.length <= 160)
  assert.equal(key, idempotencyKey('agent_run_'.padEnd(120, 'x'), 'call_'.padEnd(80, 'y'), 'save_issue', { title: 'Bug' }))
  assert.notEqual(idempotencyKey('r', 'c', 'save_issue', { title: 'A' }), idempotencyKey('r', 'c', 'save_issue', { title: 'B' }))
})

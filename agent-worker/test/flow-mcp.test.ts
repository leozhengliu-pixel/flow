import assert from 'node:assert/strict'
import { test } from 'node:test'
import { newRunContext } from '../src/context.js'
import { allowlisted, approvedMeta, connectorToolTimeoutMs, flowMcpServer, flowToolTimeoutMs } from '../src/flow-mcp.js'
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

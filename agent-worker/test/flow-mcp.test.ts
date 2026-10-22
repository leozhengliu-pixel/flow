import assert from 'node:assert/strict'
import { test } from 'node:test'
import { newRunContext } from '../src/context.js'
import { allowlisted, approvedMeta, flowMcpServer } from '../src/flow-mcp.js'
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

test('a run-token allowlist admits connector tools through external_*', () => {
  assert.ok(allowlisted(undefined, 'external_a'))
  assert.ok(!allowlisted(['get_issue'], 'external_a'))
  assert.ok(allowlisted(['get_issue', 'external_*'], 'external_a'))
  assert.ok(!allowlisted(['external_*'], 'save_issue'))
  assert.ok(allowlisted(['external_a'], 'external_a'))
})

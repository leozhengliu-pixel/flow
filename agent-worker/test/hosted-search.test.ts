/**
 * The provider's hosted web search on the worker (src/hosted-search.ts): offered per protocol when Flow asks for it,
 * shown as a web_search step, and dropped when the provider refuses it. Run: npm test
 */
import assert from 'node:assert/strict'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { setTracingDisabled } from '@openai/agents'
import { ScriptedModel, assistantMessage } from '@openai/agents/testing'
import type { FlowActivity } from '../src/activities.js'
import type { Protocol, WorkerConfig } from '../src/config.js'
import { hostedSearchQuery, hostedSearchRefused, hostedWebSearchTool } from '../src/hosted-search.js'
import { MemorySession } from '../src/session-store.js'
import { FlowAgentWorker } from '../src/worker.js'

setTracingDisabled(true)

function config(protocol: Protocol): WorkerConfig {
  return { protocol, model: 'scripted', apiKey: 'test', contextWindow: 200_000, flowUrl: 'http://flow.invalid', flowToken: 'unused', stateDir: mkdtempSync(join(tmpdir(), 'hosted-')), maxTurns: 10 }
}

test('the hosted search tool per protocol, its refusal and its query', () => {
  assert.equal((hostedWebSearchTool('openai-responses') as { type: string }).type, 'hosted_tool')
  assert.deepEqual((hostedWebSearchTool('anthropic') as { providerData: unknown }).providerData, { aiSdkProviderTool: { id: 'anthropic.web_search_20250305', args: { maxUses: 5 } } })
  assert.equal(hostedWebSearchTool('openai-chat'), undefined, 'Chat Completions has no hosted search')
  assert.equal(hostedSearchRefused(Object.assign(new Error('400 Unknown tool type: web_search'), { status: 400 })), true)
  assert.equal(hostedSearchRefused(Object.assign(new Error('500 upstream'), { status: 500 })), false)
  assert.equal(hostedSearchQuery({ providerData: { action: { query: 'flow changelog' } } }), 'flow changelog')
})

for (const [protocol, offered] of [['openai-responses', true], ['openai-chat', false]] as const) {
  test(`a run asked for hosted search on ${protocol} ${offered ? 'offers' : 'does not offer'} it`, async () => {
    const model = new ScriptedModel([[assistantMessage('Done.')]])
    const worker = new FlowAgentWorker(config(protocol), { model })
    await worker.session({ sessionId: `hosted-${protocol}`, systemPrompt: 'test', mcpServers: [], history: new MemorySession('h'), hostedWebSearch: true }).send('Search the web')
    const tools = JSON.stringify(model.calls[0]!.request.tools ?? [])
    assert.equal(tools.includes('hosted_tool'), offered, tools)
  })
}

test('a hosted search call shows as a web_search step', async () => {
  const model = new ScriptedModel([[{ type: 'hosted_tool_call', id: 'ws_1', name: 'web_search_call', status: 'completed', providerData: { type: 'web_search_call', action: { type: 'search', query: 'flow changelog' } } } as never, assistantMessage('Flow 2.0 ships loops.')]])
  const worker = new FlowAgentWorker(config('openai-responses'), { model })
  const activities: FlowActivity[] = []
  const outcome = await worker.session({ sessionId: 'hosted-step', systemPrompt: 'test', mcpServers: [], history: new MemorySession('s'), hostedWebSearch: true }).send('Search the web', activity => activities.push(activity))
  assert.equal(outcome.body, 'Flow 2.0 ships loops.')
  const step = activities.find(activity => activity.type === 'action')
  assert.deepEqual(step && step.type === 'action' ? [step.activityId, step.action, step.parameter] : undefined, ['hosted_ws_1', 'web_search', { query: 'flow changelog' }])
  assert.ok(activities.some(activity => activity.type === 'action.result' && activity.activityId === 'hosted_ws_1'))
})

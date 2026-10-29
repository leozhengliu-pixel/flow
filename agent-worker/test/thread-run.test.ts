/**
 * Built-in session runs on the worker (src/thread-run.ts): the activities a thread run posts, as the in-process run
 * appends them. Run: npm test
 */
import assert from 'node:assert/strict'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { setTracingDisabled, tool } from '@openai/agents'
import { ScriptedModel, assistantMessage, functionCall } from '@openai/agents/testing'
import type { WorkerConfig } from '../src/config.js'
import { MemorySession, type SuspendedStore } from '../src/session-store.js'
import { ThreadActivityPoster, threadProviderError, type ThreadActivity } from '../src/thread-run.js'
import { FlowAgentWorker } from '../src/worker.js'

setTracingDisabled(true)

function config(): WorkerConfig {
  return { protocol: 'openai-chat', model: 'scripted', apiKey: 'test', contextWindow: 200_000, flowUrl: 'http://flow.invalid', flowToken: 'unused', stateDir: mkdtempSync(join(tmpdir(), 'thread-run-')), maxTurns: 10 }
}

test('the thread poster posts Flow tool calls, throttled output, the question with its turn text and the end', async () => {
  const posted: ThreadActivity[] = []
  let clock = 0
  const poster = new ThreadActivityPoster(async activity => { posted.push(activity) }, { attempt: 2, now: () => clock })
  poster.push({ type: 'thought', at: 0, body: 'Working…', ephemeral: false })
  poster.push({ type: 'text.delta', at: 0, delta: 'Let me look at the issue first, it mentions the checkout page and a payment iframe that fails on every Safari release.', ephemeral: true })
  clock = 2_000
  poster.push({ type: 'text.delta', at: 0, delta: ' More.', ephemeral: true })
  poster.push({ type: 'text.delta', at: 0, delta: ' Unposted narration.', ephemeral: true })
  poster.push({ type: 'action', at: 0, activityId: 'c1', action: 'get_issue', parameter: {} })
  poster.push({ type: 'action', at: 0, activityId: 'c2', action: 'external_abc', parameter: {} })
  poster.push({ type: 'action.result', at: 0, activityId: 'c1', action: 'get_issue', result: '{}', isError: false })
  poster.push({ type: 'action', at: 0, activityId: 'q', action: 'ask_user', parameter: {} })
  poster.push({ type: 'elicitation', at: 0, elicitation: { kind: 'select', callId: 'q', body: 'Which?', options: [], allowFreeText: true } })
  poster.push({ type: 'error', at: 0, body: 'Error: 500 upstream' })
  poster.push({ type: 'error', at: 0, body: 'Flow says no', flow: true })
  await poster.drain()
  assert.deepEqual(posted.map(item => [item.activityId, item.type, item.body]), [
    ['2:1:output', 'output', 'Let me look at the issue first, it mentions the checkout page and a payment iframe that fails on every Safari release. More.'],
    ['2:2:action', 'action', 'get_issue'],
    ['2:3:elicitation', 'elicitation', ''],
    ['2:4:error', 'error', threadProviderError],
    ['2:5:error', 'error', 'Flow says no'],
  ])
})

test('a held call suspends a thread run with the text of the turn that asked', async () => {
  const held = tool<any>({ name: 'external_deploy', description: 'deploy', strict: false, needsApproval: true, parameters: { type: 'object', properties: {}, additionalProperties: true }, execute: async () => 'deployed' })
  const lookup = tool<any>({ name: 'get_issue', description: 'get', strict: false, parameters: { type: 'object', properties: {}, additionalProperties: true }, execute: async () => '{"identifier":"DEV-1"}' })
  const model = new ScriptedModel([
    [functionCall('get_issue', {}, { callId: 'g' })],
    [assistantMessage('Deploying the fix.'), functionCall('external_deploy', {}, { callId: 'd' })],
  ])
  const worker = new FlowAgentWorker(config(), { model })
  let saved: unknown
  const suspended: SuspendedStore = { save: async (state, meta) => { saved = { ...meta, state } }, load: async () => saved as never, clear: async () => { saved = undefined } }
  const posted: ThreadActivity[] = []
  const poster = new ThreadActivityPoster(async activity => { posted.push(activity) })
  const outcome = await worker.session({ sessionId: 'thread-1', systemPrompt: 'test', tools: [held, lookup], mcpServers: [], history: new MemorySession('thread-1'), suspended }).send('Fix DEV-1', activity => poster.push(activity))
  await poster.drain()
  assert.equal(outcome.status, 'awaiting')
  const question = posted.find(item => item.type === 'elicitation')
  assert.equal(question?.body, 'Deploying the fix.')
  assert.deepEqual((question?.elicitation as { calls: { callId: string }[] }).calls.map(call => call.callId), ['d'])
  assert.deepEqual(posted.filter(item => item.type === 'action').map(item => item.body), ['get_issue'])
})

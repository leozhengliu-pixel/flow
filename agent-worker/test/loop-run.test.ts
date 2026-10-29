/**
 * Loop runs on the worker (api/cmd/server/agent_worker_loop.go): the session offers only the tools Flow lists, ends
 * at finish_run, and its last word goes through Flow's guard, which may send it back several times (a loop's
 * completion nudges). Run: npm test
 */
import assert from 'node:assert/strict'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { setTracingDisabled, tool } from '@openai/agents'
import { ScriptedModel, assistantMessage, functionCall } from '@openai/agents/testing'
import type { WorkerConfig } from '../src/config.js'
import type { GuardRequest, GuardResponse } from '../src/run-guard.js'
import { MemorySession } from '../src/session-store.js'
import { FlowAgentWorker } from '../src/worker.js'

setTracingDisabled(true)

function config(): WorkerConfig {
  return { protocol: 'openai-chat', model: 'scripted', apiKey: 'test', contextWindow: 200_000, flowUrl: 'http://flow.invalid', flowToken: 'unused', stateDir: mkdtempSync(join(tmpdir(), 'loop-run-')), maxTurns: 10 }
}

const view = { enabled: true, codingDue: false, decisionRound: -1 }
const plain = (name: string, output: string, ran: string[]) => tool<any>({ name, description: name, strict: false, parameters: { type: 'object', properties: {}, additionalProperties: true }, execute: async () => { ran.push(name); return output } })

test('a loop run ends at finish_run and offers no host tools', async () => {
  const ran: string[] = []
  const requests: GuardRequest[] = []
  const api = async (request: GuardRequest): Promise<GuardResponse> => {
    requests.push(request)
    return request.op === 'finish' ? { ...view, reply: 'Two open issues.' } : view
  }
  const model = new ScriptedModel([
    [assistantMessage('Listing.'), functionCall('list_issues', {}, { callId: 'l' })],
    [functionCall('finish_run', { status: 'done', summary: 'Two open issues.' }, { callId: 'f' })],
    [assistantMessage('never asked')],
  ])
  const worker = new FlowAgentWorker(config(), { model })
  const outcome = await worker.session({ sessionId: 'loop-1', systemPrompt: 'loop', guard: api, tools: [plain('list_issues', '{"items":[]}', ran), plain('finish_run', '{"ok":true}', ran)], mcpServers: [], history: new MemorySession('loop-1'), allTools: true, stopAtTools: ['finish_run'] }).send('Run this loop now and follow its instructions.')
  assert.equal(outcome.status, 'response')
  assert.equal(outcome.body, 'Two open issues.')
  assert.deepEqual(ran, ['list_issues', 'finish_run'])
  assert.equal(model.calls.length, 2, 'the run stops after finish_run')
  const offered = (model.calls[0]!.request.tools ?? []).map(item => (item as { name: string }).name).sort()
  assert.deepEqual(offered, ['finish_run', 'list_issues'])
  assert.deepEqual(requests.filter(request => request.op === 'finish').map(request => (request as { phase: string }).phase), ['reply'])
})

test('a loop reply is sent back as often as Flow asks, within the bound', async () => {
  let finishes = 0
  const api = async (request: GuardRequest): Promise<GuardResponse> => {
    if (request.op !== 'finish') return view
    finishes++
    return finishes < 3 ? { ...view, nudge: 'Continue the loop and call finish_run.' } : { ...view, reply: `Done after ${finishes}` }
  }
  const model = new ScriptedModel([[assistantMessage('First.')], [assistantMessage('Second.')], [assistantMessage('Third.')]])
  const worker = new FlowAgentWorker(config(), { model })
  const outcome = await worker.session({ sessionId: 'loop-2', systemPrompt: 'loop', guard: api, mcpServers: [], history: new MemorySession('loop-2'), allTools: true }).send('Run this loop now and follow its instructions.')
  assert.equal(outcome.body, 'Done after 3')
  assert.equal(model.calls.length, 3)
})

test('loop diagnostics are awaited before calls execute, and an unusable turn is recovered', async () => {
  const ran: string[] = []
  const requests: GuardRequest[] = []
  let recover = true
  const api = async (request: GuardRequest): Promise<GuardResponse> => {
    requests.push(request)
    if (request.op === 'model' && request.round === 0) return { ...view, handled: true }
    if (request.op === 'finish' && recover) { recover = false; return { ...view, nudge: 'Use a complete tool call now.' } }
    if (request.op === 'finish') return { ...view, reply: 'Recovered.' }
    return view
  }
  const model = new ScriptedModel([[functionCall('save_issue', { title: 'cut' }, { callId: 'cut' })], [assistantMessage('Recovered.')]])
  const worker = new FlowAgentWorker(config(), { model })
  const outcome = await worker.session({ sessionId: 'loop-recovery', systemPrompt: 'loop', guard: api, tools: [plain('save_issue', '{}', ran)], mcpServers: [], history: new MemorySession('loop-recovery'), allTools: true }).send('Continue')
  assert.equal(outcome.status, 'response')
  assert.deepEqual(ran, [], 'suppressed calls never reach tools')
  assert.equal(requests.filter(request => request.op === 'model').length, 2)
  assert.equal(requests.filter(request => request.op === 'input').length, 2)
})

test('loop conversation budget stops before the provider is called', async () => {
  const model = new ScriptedModel([[assistantMessage('never called')]])
  const worker = new FlowAgentWorker(config(), { model })
  const api = async (request: GuardRequest): Promise<GuardResponse> => request.op === 'input' ? { ...view, error: 'conversation budget' } : view
  const outcome = await worker.session({ sessionId: 'loop-budget', systemPrompt: 'loop', guard: api, mcpServers: [], history: new MemorySession('loop-budget'), allTools: true }).send('Run')
  assert.equal(outcome.status, 'error')
  assert.match(outcome.body, /conversation budget/)
  assert.equal(model.calls.length, 0)
})

test('a Chat adapter token-limited empty turn reaches Flow recovery', async () => {
  const chunk = (delta: unknown, finish: string | null) => `data: ${JSON.stringify({ id: 'c', object: 'chat.completion.chunk', created: 1, model: 'test', choices: [{ index: 0, delta, finish_reason: finish }] })}\n\n`
  const streams = [chunk({ role: 'assistant' }, null) + chunk({}, 'length') + 'data: [DONE]\n\n', chunk({ role: 'assistant', content: 'Recovered.' }, 'stop') + 'data: [DONE]\n\n']
  const requests: GuardRequest[] = []
  let nudged = false
  const api = async (request: GuardRequest): Promise<GuardResponse> => {
    requests.push(request)
    if (request.op === 'model' && request.modelTurn.stopReason === 'length') return { ...view, handled: true }
    if (request.op === 'finish' && !nudged) { nudged = true; return { ...view, nudge: 'Think less and answer now.' } }
    return request.op === 'finish' ? { ...view, reply: 'Recovered.' } : view
  }
  const worker = new FlowAgentWorker({ ...config(), baseURL: 'https://model.invalid/v1' }, { fetch: async () => new Response(streams.shift(), { headers: { 'content-type': 'text/event-stream' } }) })
  const outcome = await worker.session({ sessionId: 'loop-cut-empty', systemPrompt: 'loop', guard: api, mcpServers: [], history: new MemorySession('loop-cut-empty'), allTools: true }).send('Run')
  assert.equal(outcome.status, 'response', outcome.body)
  assert.equal(outcome.body, 'Recovered.')
  assert.equal(requests.filter(request => request.op === 'model').length, 2)
})

test('saved held calls without RunState execute approved calls only and preserve results in history', async () => {
  const calls: string[] = []
  const mcp = {
    name: 'saved', connect: async () => {}, close: async () => {}, listTools: async () => [],
    callTool: async () => [],
    callToolResult: async (name: string, _args: unknown, meta: unknown) => { calls.push(name); assert.deepEqual(meta, { 'flow/approved': true }); return { content: [{ type: 'text', text: 'Saved result' }] } },
  }
  const model = new ScriptedModel([[assistantMessage('The saved action completed.')]])
  const history = new MemorySession('saved-calls')
  const worker = new FlowAgentWorker(config(), { model })
  const outcome = await worker.session({ sessionId: 'saved-calls', systemPrompt: 'test', mcpServers: [mcp as any], history, heldCalls: [{ id: 'yes', name: 'list_teams', arguments: '{}', approved: true }, { id: 'no', name: 'delete_issue', arguments: '{}', approved: false }] }).send('Approved')
  assert.equal(outcome.status, 'response')
  assert.deepEqual(calls, ['list_teams'])
  const input = JSON.stringify(model.calls[0]!.request.input)
  assert.match(input, /Saved result/)
  assert.match(input, /user rejected/)
})

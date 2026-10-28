/**
 * Chat replies on the worker (src/chat-run.ts): the conversation Flow serves with a chat run, the activities the
 * worker posts for the reply, and a chat run's suspend and resume with per-call decisions. Run: npm test
 */
import assert from 'node:assert/strict'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { setTracingDisabled, tool } from '@openai/agents'
import { ScriptedModel, assistantMessage, functionCall } from '@openai/agents/testing'
import { ChatActivityPoster, chatConversation, type ChatActivity } from '../src/chat-run.js'
import type { WorkerConfig } from '../src/config.js'
import type { GuardRequest, GuardResponse } from '../src/run-guard.js'
import { RuntimeApiError } from '../src/runtime-client.js'
import { MemorySession, type SuspendedStore } from '../src/session-store.js'
import { FlowAgentWorker } from '../src/worker.js'

setTracingDisabled(true)

function config(): WorkerConfig {
  return { protocol: 'openai-chat', model: 'scripted', apiKey: 'test', contextWindow: 200_000, flowUrl: 'http://flow.invalid', flowToken: 'unused', stateDir: mkdtempSync(join(tmpdir(), 'chat-run-')), maxTurns: 10 }
}

test('the run conversation becomes SDK items, the last user message the request', () => {
  const { history, request } = chatConversation([
    { role: 'user', content: 'List the backlog' },
    { role: 'assistant', content: 'Looking.', toolCalls: [{ id: 'c1', name: 'list_issues', arguments: '{"limit":5}' }] },
    { role: 'tool', content: '{"items":[]}', toolCallId: 'c1' },
    { role: 'tool', content: 'orphan', toolCallId: 'nope' },
    { role: 'assistant', content: 'The backlog is empty.' },
    { role: 'user', content: 'Thanks, and DEV-2?' },
  ])
  assert.equal(request, 'Thanks, and DEV-2?')
  assert.deepEqual(history.map(item => ('type' in item && item.type !== 'message' ? item.type : (item as { role: string }).role)), ['user', 'assistant', 'function_call', 'function_call_result', 'assistant'])
  assert.deepEqual(history[3], { type: 'function_call_result', callId: 'c1', name: 'list_issues', status: 'completed', output: { type: 'text', text: '{"items":[]}' } })
})

test('the poster coalesces text, keeps order, and ends a stopped run as stopped', async () => {
  const posted: ChatActivity[] = []
  let settled = 0
  const poster = new ChatActivityPoster(async activity => {
    posted.push(activity)
    return { settled: activity.type === 'thought' }
  }, { intervalMs: 1_000, onSettled: () => settled++ })
  poster.push({ type: 'thought', at: 0, body: 'Working…', ephemeral: false })
  poster.push({ type: 'thought', at: 0, body: 'reasoning', ephemeral: true })
  poster.push({ type: 'text.delta', at: 0, delta: 'Let me ', ephemeral: true })
  poster.push({ type: 'text.delta', at: 0, delta: 'look.', ephemeral: true })
  poster.push({ type: 'action', at: 0, activityId: 'c1', action: 'list_issues', parameter: { limit: 5 } })
  poster.push({ type: 'action.result', at: 0, activityId: 'c1', action: 'list_issues', result: '{}', isError: false, change: { runId: 'r', seq: 1 } })
  poster.push({ type: 'connector.elicitation', at: 0, elicitation: {} as never })
  poster.push({ type: 'question.delta', at: 0, activityId: 'ask', body: 'Which', status: 'running', ephemeral: true })
  poster.push({ type: 'question.delta', at: 0, activityId: 'ask', body: 'Which team?', status: 'running', ephemeral: true })
  poster.push({ type: 'response', at: 0, body: 'Stopped.', usage: {} as never, canceled: true })
  await poster.drain()
  assert.deepEqual(posted, [
    { seq: 1, type: 'thought', body: 'Working…' },
    { seq: 2, type: 'text.delta', body: 'Let me look.' },
    { seq: 3, type: 'action', activityId: 'c1:call', callId: 'c1', content: { action: 'list_issues', parameter: { limit: 5 } } },
    { seq: 4, type: 'action.result', activityId: 'c1:result', callId: 'c1', content: { action: 'list_issues', result: '{}' }, isError: false, change: { runId: 'r', seq: 1 } },
    { seq: 5, type: 'question.delta', callId: 'ask', body: 'Which team?' },
    { seq: 6, type: 'error', activityId: 'end', body: 'Generation stopped' },
  ])
  assert.equal(settled, 1, 'a reply settled on Flow before the run ended stops the run once')
})

test('a post that fails in transit or with a server error is sent again with its number; a refusal is not', async () => {
  const attempts: ChatActivity[] = []
  const errors: unknown[] = []
  const failures = new Map<number, unknown[]>([
    [1, [new TypeError('fetch failed'), new RuntimeApiError(503, 'unavailable')]],
    [2, [new RuntimeApiError(409, 'This worker does not hold the run')]],
  ])
  const poster = new ChatActivityPoster(async activity => {
    attempts.push(activity)
    const failure = failures.get(activity.seq!)?.shift()
    if (failure) throw failure
    return {}
  }, { retryDelaysMs: [1, 1, 1], onError: error => errors.push(error) })
  poster.push({ type: 'action', at: 0, activityId: 'c1', action: 'list_issues', parameter: {} })
  poster.push({ type: 'action.result', at: 0, activityId: 'c1', action: 'list_issues', result: '{}', isError: false })
  poster.push({ type: 'response', at: 0, body: 'Done.', usage: {} as never })
  await poster.drain()
  assert.deepEqual(attempts.map(activity => `${activity.seq}:${activity.type}`), ['1:action', '1:action', '1:action', '2:action.result', '3:response'])
  assert.equal(errors.length, 1, 'the refused post is reported, and the next one still goes out')
  assert.equal((errors[0] as RuntimeApiError).status, 409)
})

test('a chat run starts from the conversation, suspends held calls in Flow and resumes with per-call decisions', async () => {
  const ran: string[] = []
  const held = (name: string) => tool<any>({ name, description: name, strict: false, needsApproval: true, parameters: { type: 'object', properties: {}, additionalProperties: true }, execute: async () => {
    ran.push(name)
    return `${name} done`
  } })
  const model = new ScriptedModel([
    [functionCall('external_deploy', {}, { callId: 'd' }), functionCall('external_tag', {}, { callId: 't' })],
    [assistantMessage('Deployed; the tag was skipped.')],
  ])
  const worker = new FlowAgentWorker(config(), { model })
  let saved: { state: string } & Record<string, unknown> | undefined
  const suspended: SuspendedStore = {
    save: async (state, meta) => { saved = { ...meta, state } },
    load: async () => saved,
    clear: async () => { saved = undefined },
  }
  const { history, request } = chatConversation([{ role: 'user', content: 'Earlier question' }, { role: 'assistant', content: 'Earlier answer.' }, { role: 'user', content: 'Deploy and tag' }])
  const options = { sessionId: 'chat-1', systemPrompt: 'You are Flow Agent.', tools: [held('external_deploy'), held('external_tag')], mcpServers: [], suspended }
  const first = await worker.session({ ...options, history: new MemorySession('chat-1', history) }).send(request)
  assert.equal(first.status, 'awaiting')
  assert.equal(first.elicitation?.kind, 'approval')
  assert.deepEqual(first.elicitation?.kind === 'approval' ? first.elicitation.calls?.map(call => call.callId) : [], ['d', 't'])
  assert.ok(saved?.state, 'the suspended run is kept in the given store')
  const input = JSON.stringify(model.calls[0]!.request.input)
  assert.ok(input.includes('Earlier answer.') && input.includes('Deploy and tag'), 'the model reads the conversation Flow rendered')
  assert.deepEqual(ran, [])

  // Another session object (another worker) resumes it with the person's decisions.
  const second = await worker.session({ ...options, history: new MemorySession('chat-1') }).resumeWith({ d: 'approved', t: 'rejected' })
  assert.equal(second.status, 'response')
  assert.equal(second.body, 'Deployed; the tag was skipped.')
  assert.deepEqual(ran, ['external_deploy'], 'only the approved call ran')
  assert.equal(saved, undefined, 'the resumed state is cleared')
})

test('a spent tool budget withholds every tool from the last turn', async () => {
  const requests: GuardRequest[] = []
  const api = async (request: GuardRequest): Promise<GuardResponse> => {
    requests.push(request)
    const view = { enabled: true, codingDue: false, decisionRound: -1 }
    if (request.op === 'turn' && request.round >= 1) return { ...view, withhold: true, nudge: '\n\n[Flow] stop now' }
    if (request.op === 'finish') return { ...view, reply: request.text }
    return view
  }
  const lookup = tool<any>({ name: 'get_issue', description: 'Get an issue', strict: false, parameters: { type: 'object', properties: {}, additionalProperties: true }, execute: async () => '{"identifier":"DEV-1"}' })
  const model = new ScriptedModel([[functionCall('get_issue', {}, { callId: 'g' })], [assistantMessage('DEV-1 is open.')]])
  const worker = new FlowAgentWorker(config(), { model })
  const outcome = await worker.session({ sessionId: 'chat-budget', systemPrompt: 'test', guard: api, tools: [lookup], mcpServers: [], history: new MemorySession('chat-budget') }).send('Look at DEV-1')
  assert.equal(outcome.body, 'DEV-1 is open.')
  assert.ok((model.calls[0]!.request.tools ?? []).length > 0, 'the first turn has tools')
  assert.deepEqual(model.calls[1]!.request.tools ?? [], [], 'the last turn has none')
  assert.ok(JSON.stringify(model.calls[1]!.request.input).includes('[Flow] stop now'), 'the last turn reads the nudge')
})

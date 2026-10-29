/**
 * Protocol tests against mocked providers (no network): the Anthropic Messages path (unmeasured live — no key in
 * the spike environment) and the Chat Completions path on a non-OpenAI base URL. Run: npm test
 */
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Agent, RunState, run, setTracingDisabled, tool } from '@openai/agents'
import { z } from 'zod'
import type { ModelConfig } from '../src/config.js'
import { createModel, modelSettings, ObservedModel, type ModelDiagnostics } from '../src/models.js'

setTracingDisabled(true)

type Captured = { url: string; body: any }

function sse(events: [string, unknown][]) {
  return events.map(([event, data]) => `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`).join('')
}

/** Anthropic stream: two parallel tool_use blocks, arguments split across input_json_delta chunks. */
const parallelToolUse = sse([
  ['message_start', { type: 'message_start', message: { id: 'msg_1', type: 'message', role: 'assistant', model: 'claude-sonnet-5', content: [], stop_reason: null, stop_sequence: null, usage: { input_tokens: 1200, output_tokens: 1, cache_read_input_tokens: 1000, cache_creation_input_tokens: 0 } } }],
  ['content_block_start', { type: 'content_block_start', index: 0, content_block: { type: 'tool_use', id: 'toolu_a', name: 'get_issue', input: {} } }],
  ['content_block_delta', { type: 'content_block_delta', index: 0, delta: { type: 'input_json_delta', partial_json: '{"id": "DE' } }],
  ['content_block_delta', { type: 'content_block_delta', index: 0, delta: { type: 'input_json_delta', partial_json: 'V-1"}' } }],
  ['content_block_stop', { type: 'content_block_stop', index: 0 }],
  ['content_block_start', { type: 'content_block_start', index: 1, content_block: { type: 'tool_use', id: 'toolu_b', name: 'delete_issue', input: {} } }],
  ['content_block_delta', { type: 'content_block_delta', index: 1, delta: { type: 'input_json_delta', partial_json: '{"id": "DEV-2"}' } }],
  ['content_block_stop', { type: 'content_block_stop', index: 1 }],
  ['message_delta', { type: 'message_delta', delta: { stop_reason: 'tool_use', stop_sequence: null }, usage: { output_tokens: 40 } }],
  ['message_stop', { type: 'message_stop' }],
])

const finalText = sse([
  ['message_start', { type: 'message_start', message: { id: 'msg_2', type: 'message', role: 'assistant', model: 'claude-sonnet-5', content: [], stop_reason: null, stop_sequence: null, usage: { input_tokens: 1300, output_tokens: 1 } } }],
  ['content_block_start', { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } }],
  ['content_block_delta', { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 'Read DEV-1 and deleted DEV-2.' } }],
  ['content_block_stop', { type: 'content_block_stop', index: 0 }],
  ['message_delta', { type: 'message_delta', delta: { stop_reason: 'end_turn', stop_sequence: null }, usage: { output_tokens: 9 } }],
  ['message_stop', { type: 'message_stop' }],
])

function mockFetch(responses: string[], captured: Captured[], contentType = 'text/event-stream'): typeof fetch {
  return (async (input: any, init?: any) => {
    captured.push({ url: String(input instanceof Request ? input.url : input), body: JSON.parse(String(init?.body ?? '{}')) })
    const next = responses.shift()
    if (next === undefined) throw new Error('unexpected extra request')
    return new Response(next, { status: 200, headers: { 'content-type': contentType } })
  }) as typeof fetch
}

const anthropic: ModelConfig = { protocol: 'anthropic', model: 'claude-sonnet-5', apiKey: 'test-key', baseURL: 'https://anthropic.test/v1', reasoningEffort: 'medium', contextWindow: 200_000 }

function tools(calls: { name: string; id: string }[]) {
  const getIssue = tool({ name: 'get_issue', description: 'Get an issue', parameters: z.object({ id: z.string() }), execute: async ({ id }) => { calls.push({ name: 'get_issue', id }); return `{"identifier":"${id}"}` } })
  const deleteIssue = tool({ name: 'delete_issue', description: 'Delete an issue', parameters: z.object({ id: z.string() }), needsApproval: true, execute: async ({ id }) => { calls.push({ name: 'delete_issue', id }); return `{"deleted":true}` } })
  return [getIssue, deleteIssue]
}

test('anthropic: parallel tool_use arguments survive streaming; approval interruption serializes and resumes', async () => {
  const captured: Captured[] = []
  const fetch = mockFetch([parallelToolUse, finalText], captured)
  const calls: { name: string; id: string }[] = []
  const diagnostics: ModelDiagnostics[] = []
  const model = new ObservedModel(createModel(anthropic, { fetch }))
  model.afterModel = async (output, _usage, stats) => { diagnostics.push({ ...stats }); return output }
  const agent = new Agent({ name: 'Flow Agent', instructions: 'Be brief.', model, modelSettings: modelSettings(anthropic, 'session-1'), tools: tools(calls) })

  const first = await run(agent, 'Read DEV-1 and delete DEV-2', { stream: true })
  for await (const _ of first) { /* drain */ }
  await first.completed
  // get_issue ran; delete_issue is held for approval with its streamed arguments intact.
  assert.deepEqual(calls, [{ name: 'get_issue', id: 'DEV-1' }])
  assert.equal(first.interruptions.length, 1)
  assert.equal(first.interruptions[0].name, 'delete_issue')
  assert.equal(diagnostics[0]?.bytes, Buffer.byteLength(parallelToolUse))
  assert.equal(diagnostics[0]?.finished, true)
  assert.ok((diagnostics[0]?.events ?? 0) > 0)
  assert.deepEqual(JSON.parse(first.interruptions[0].arguments ?? '{}'), { id: 'DEV-2' })

  // Request 1: Messages API with tools, adaptive thinking, effort, cache control, parallel tool use allowed.
  assert.match(captured[0].url, /^https:\/\/anthropic\.test\/v1\/messages$/)
  const body = captured[0].body
  assert.equal(body.model, 'claude-sonnet-5')
  assert.equal(body.stream, true)
  assert.deepEqual(body.tools.map((t: any) => t.name).sort(), ['delete_issue', 'get_issue'])
  assert.equal(body.thinking?.type, 'adaptive')
  assert.notEqual(body.tool_choice?.disable_parallel_tool_use, true)
  assert.ok(JSON.stringify(body).includes('"cache_control":{"type":"ephemeral"}'), 'automatic cache_control is sent')

  // Suspend: the state survives JSON; a fresh agent object resumes it after approval.
  const serialized = first.state.toString()
  const agent2 = new Agent({ name: 'Flow Agent', instructions: 'Be brief.', model: createModel(anthropic, { fetch }), modelSettings: modelSettings(anthropic, 'session-1'), tools: tools(calls) })
  const state = await RunState.fromString(agent2, serialized)
  state.approve(state.getInterruptions()[0])
  const second = await run(agent2, state, { stream: true })
  for await (const _ of second) { /* drain */ }
  await second.completed
  assert.deepEqual(calls, [{ name: 'get_issue', id: 'DEV-1' }, { name: 'delete_issue', id: 'DEV-2' }])
  assert.equal(second.finalOutput, 'Read DEV-1 and deleted DEV-2.')

  // Request 2 carries both tool results, matched to their tool_use ids.
  const results = captured[1].body.messages.flatMap((m: any) => (Array.isArray(m.content) ? m.content : [])).filter((c: any) => c.type === 'tool_result')
  assert.deepEqual(results.map((r: any) => r.tool_use_id).sort(), ['toolu_a', 'toolu_b'])
})

test('openai-chat: OpenAI-compatible base URL, parallel tool calls, prompt cache key, reasoning effort', async () => {
  const captured: Captured[] = []
  const chunk = (delta: unknown, finish: string | null = null) => `data: ${JSON.stringify({ id: 'c1', object: 'chat.completion.chunk', created: 1, model: 'deepseek-chat', choices: [{ index: 0, delta, finish_reason: finish }] })}\n\n`
  const stream =
    chunk({ role: 'assistant', tool_calls: [{ index: 0, id: 'call_a', type: 'function', function: { name: 'get_issue', arguments: '' } }] }) +
    chunk({ tool_calls: [{ index: 1, id: 'call_b', type: 'function', function: { name: 'get_issue', arguments: '' } }] }) +
    chunk({ tool_calls: [{ index: 0, function: { arguments: '{"id":"DEV-1"}' } }] }) +
    chunk({ tool_calls: [{ index: 1, function: { arguments: '{"id":"DEV-3"}' } }] }) +
    chunk({}, 'tool_calls') + 'data: [DONE]\n\n'
  const done = chunk({ role: 'assistant', content: 'Both read.' }) + chunk({}, 'stop') + 'data: [DONE]\n\n'
  const config: ModelConfig = { protocol: 'openai-chat', model: 'deepseek-chat', apiKey: 'test-key', baseURL: 'https://compatible.test/v1', reasoningEffort: 'low', contextWindow: 128_000 }
  const calls: { name: string; id: string }[] = []
  const diagnostics: ModelDiagnostics[] = []
  const model = new ObservedModel(createModel(config, { fetch: mockFetch([stream, done], captured) }))
  model.afterModel = async (output, _usage, stats) => { diagnostics.push({ ...stats }); return output }
  const agent = new Agent({ name: 'Flow Agent', instructions: 'Be brief.', model, modelSettings: modelSettings(config, 'session-7'), tools: tools(calls) })
  const result = await run(agent, 'Read DEV-1 and DEV-3', { stream: true })
  for await (const _ of result) { /* drain */ }
  await result.completed
  assert.deepEqual(calls.map(c => c.id).sort(), ['DEV-1', 'DEV-3'])
  assert.equal(result.finalOutput, 'Both read.')
  assert.equal(diagnostics[0]?.bytes, Buffer.byteLength(stream))
  assert.equal(diagnostics[0]?.stopReason, 'tool_calls')
  assert.equal(diagnostics[1]?.stopReason, 'stop')
  assert.equal(captured[0].url, 'https://compatible.test/v1/chat/completions')
  assert.equal(captured[0].body.parallel_tool_calls, true)
  assert.equal(captured[0].body.prompt_cache_key, 'session-7')
  assert.equal(captured[0].body.reasoning_effort, 'low')
})

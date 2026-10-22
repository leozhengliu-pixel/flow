/**
 * The worker loop under Flow's run guard, driven by the shared vectors in
 * api/cmd/server/testdata/worker_run_guard (Go answers the same requests with the same responses, see
 * agent_runtime_guard_test.go). A scripted model plays each vector's turns against a code-search connector tool;
 * the fake runtime API checks that the worker sends exactly the vector's requests, in order, and answers with the
 * recorded responses. Run: npm test
 */
import assert from 'node:assert/strict'
import { mkdtempSync, readdirSync, readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { setTracingDisabled, tool } from '@openai/agents'
import { ScriptedModel, assistantMessage, functionCall } from '@openai/agents/testing'
import type { WorkerConfig } from '../src/config.js'
import { isErrorOutput, RunGuard, ToolErrorFlags, type GuardRequest, type GuardResponse } from '../src/run-guard.js'
import { FlowAgentWorker } from '../src/worker.js'

setTracingDisabled(true)

const vectors = new URL('../../api/cmd/server/testdata/worker_run_guard/', import.meta.url)
const placeholders: Record<string, string> = { '{{env0}}': 'cenv_api', '{{env1}}': 'cenv_web', '{{issue}}': 'ENG-7' }

type Vector = {
  name: string
  maxTurns: number
  connector: 'match' | 'error' | 'isError' | 'empty' | 'none'
  model: ({ call: { name: string; arguments: Record<string, unknown> } } | { text: string })[]
  expect: { connectorCalls: number; coding: boolean; replyContains?: string[]; replyOmits?: string[] }
  exchanges: { request: GuardRequest; response: GuardResponse }[]
}

function load(file: string): Vector {
  let text = readFileSync(new URL(file, vectors), 'utf8')
  for (const [placeholder, value] of Object.entries(placeholders)) text = text.replaceAll(placeholder, value)
  return JSON.parse(text)
}

function config(maxTurns: number): WorkerConfig {
  return { protocol: 'openai-chat', model: 'scripted', apiKey: 'test', contextWindow: 200_000, flowUrl: 'http://flow.invalid', flowToken: 'unused', stateDir: mkdtempSync(join(tmpdir(), 'run-guard-')), maxTurns }
}

/** The connector; in isError mode its result is plain text flagged isError, as Flow's /mcp passes it through. */
function connectorTool(mode: Vector['connector'], calls: string[], flags?: ToolErrorFlags) {
  return tool<any>({
    name: 'external_sourcebot_glob',
    description: 'Sourcebot tool glob. Arguments: pattern (required), repo.',
    strict: false,
    parameters: { type: 'object', required: ['pattern'], properties: { pattern: { type: 'string' }, repo: { type: 'string' } }, additionalProperties: false },
    execute: async (input: unknown) => {
      calls.push(JSON.stringify(input))
      if (mode === 'isError') {
        flags?.record('external_sourcebot_glob', input, true)
        return 'search backend unavailable'
      }
      return mode === 'error' ? 'Error: search backend unavailable' : mode === 'empty' ? '' : 'acme/api@main api/go.mod'
    },
  })
}

const getIssue = tool<any>({
  name: 'get_issue',
  description: 'Get an issue',
  strict: false,
  parameters: { type: 'object', required: ['id'], properties: { id: { type: 'string' } }, additionalProperties: false },
  execute: async () => '{"identifier":"ENG-7","title":"Add the multiply endpoint"}',
})

function offered(request: any): string[] {
  return (request.tools ?? []).map((item: any) => item.name)
}

function lastInputText(request: any): string {
  const input = Array.isArray(request.input) ? request.input : []
  const last = input.at(-1)
  const output = last?.output
  if (typeof output === 'string') return output
  return output?.type === 'text' ? output.text : JSON.stringify(output ?? '')
}

for (const file of readdirSync(vectors).filter(name => name.endsWith('.json')).sort()) {
  test(`shared vector ${file}`, async () => {
    const vector = load(file)
    const pending = [...vector.exchanges]
    const api = async (request: GuardRequest): Promise<GuardResponse> => {
      const next = pending.shift()
      assert.ok(next, `unexpected guard request ${JSON.stringify(request)}`)
      assert.deepEqual(request, next.request)
      return structuredClone(next.response)
    }
    const model = new ScriptedModel(vector.model.map((step, index) => ('text' in step ? [assistantMessage(step.text)] : [functionCall(step.call.name, step.call.arguments, { callId: `call_${index}` })])))
    const connectorCalls: string[] = []
    const toolErrors = new ToolErrorFlags()
    const tools = vector.connector === 'none' ? [getIssue] : [connectorTool(vector.connector, connectorCalls, toolErrors), getIssue]
    const worker = new FlowAgentWorker(config(vector.maxTurns), { model })
    const session = worker.session({ sessionId: `guard-${file}`, systemPrompt: 'You are Flow Agent.', guard: api, tools, mcpServers: [], toolErrors })
    const outcome = await session.send('Delegated issue ENG-7')

    assert.deepEqual(pending, [], 'every request of the vector was sent')
    assert.equal(model.remainingSteps, 0, 'the model played every scripted turn')
    assert.equal(connectorCalls.length, vector.expect.connectorCalls, 'calls that reached the connector')
    const finish = vector.exchanges.at(-1)!
    const reply = finish.response.reply || (finish.request as { text: string }).text
    assert.equal(outcome.status, 'response')
    assert.equal(outcome.body, reply)
    for (const part of vector.expect.replyContains ?? []) assert.ok(outcome.body.includes(part), `the reply contains ${part}: ${outcome.body}`)
    for (const part of vector.expect.replyOmits ?? []) assert.ok(!outcome.body.includes(part), `the reply omits ${part}: ${outcome.body}`)

    // What the model saw: connector tools until Flow withdraws them, and the decision nudge on that turn's input.
    for (const exchange of vector.exchanges) {
      if (exchange.request.op !== 'turn') continue
      const request = model.calls[exchange.request.round]?.request
      assert.ok(request, `model turn ${exchange.request.round} ran`)
      const connectors = offered(request).some(name => name.startsWith('external_'))
      assert.equal(connectors, exchange.request.connectors && !exchange.response.mustDecide, `turn ${exchange.request.round} offers connector tools`)
      if (exchange.response.nudge) assert.ok(lastInputText(request).endsWith(exchange.response.nudge), `turn ${exchange.request.round} reads the nudge`)
    }
  })
}

test('a guard that Flow does not enable (a chat run) leaves the loop alone', async () => {
  const requests: GuardRequest[] = []
  const api = async (request: GuardRequest): Promise<GuardResponse> => {
    requests.push(request)
    return { enabled: false, codingDue: false, decisionRound: -1 }
  }
  const model = new ScriptedModel([[functionCall('external_sourcebot_glob', { pattern: '**/go.mod' }, { callId: 'a' })], [assistantMessage('Found it.')]])
  const calls: string[] = []
  const worker = new FlowAgentWorker(config(24), { model })
  const outcome = await worker.session({ sessionId: 'guard-chat', systemPrompt: 'test', guard: api, tools: [connectorTool('match', calls)], mcpServers: [] }).send('find go.mod')
  assert.deepEqual(requests, [{ op: 'start' }])
  assert.equal(outcome.body, 'Found it.')
  assert.equal(calls.length, 1)
})

test('a start_coding_session result is annotated with the delivery state Flow reads from it', async () => {
  const requests: GuardRequest[] = []
  const annotated = '{"sessionId":"cs_1","started":true,"deliveryStatus":"running","reportAs":"The coding session is running; nothing is delivered yet."}'
  const api = async (request: GuardRequest): Promise<GuardResponse> => {
    requests.push(request)
    const view = { enabled: true, codingDue: request.op !== 'coding' && !requests.some(item => item.op === 'coding'), decisionRound: -1 }
    if (request.op === 'coding') return { ...view, content: annotated }
    if (request.op === 'finish') return { ...view, reply: 'Started a coding session for ENG-7; it is running.' }
    return view
  }
  const startCoding = tool<any>({ name: 'start_coding_session', description: 'Start Coding', strict: false, parameters: { type: 'object', required: ['issueId'], properties: { issueId: { type: 'string' }, environmentId: { type: 'string' }, prompt: { type: 'string' } }, additionalProperties: false }, execute: async () => '{"sessionId":"cs_1","started":true}' })
  const model = new ScriptedModel([[functionCall('start_coding_session', { issueId: 'ENG-7', environmentId: 'cenv_api', prompt: 'Implement it' }, { callId: 's' })], [assistantMessage('Done: implemented and merged.')]])
  const worker = new FlowAgentWorker(config(24), { model })
  const outcome = await worker.session({ sessionId: 'guard-coding', systemPrompt: 'test', guard: api, tools: [startCoding], mcpServers: [] }).send('Implement ENG-7')
  assert.deepEqual(requests.map(item => item.op), ['start', 'turn', 'coding', 'turn', 'finish'])
  assert.deepEqual(requests[2], { op: 'coding', content: '{"sessionId":"cs_1","started":true}', failed: false })
  assert.equal(lastInputText(model.calls[1]!.request), annotated, 'the model reads the annotated result')
  assert.equal(outcome.body, 'Started a coding session for ENG-7; it is running.', 'a done claim is replaced by the reply Flow gives')
})

test('the guard fails open when Flow cannot be reached', async () => {
  const guard = new RunGuard(async () => {
    throw new Error('connection refused')
  }, 24)
  await guard.start({})
  assert.equal(guard.enabled, false)
  assert.deepEqual(await guard.finish('reply', 'text'), { reply: 'text', transferred: false })
})

test('the structured isError flag is read back for the same tool and arguments', () => {
  const flags = new ToolErrorFlags()
  flags.record('external_a', { b: 1, a: 'x' }, true)
  flags.record('external_a', { a: 'y' }, false)
  assert.equal(flags.take('external_a', '{"a":"x","b":1}'), true, 'key order does not matter')
  assert.equal(flags.take('external_a', '{"a":"x","b":1}'), undefined, 'a flag is read once')
  assert.equal(flags.take('external_a', '{"a":"y"}'), false)
  assert.ok(!isErrorOutput('search backend unavailable'), 'plain text alone is not an error')
})

test('error outputs count as failures', () => {
  assert.ok(isErrorOutput('Error: search backend unavailable'))
  assert.ok(isErrorOutput('An error occurred while running the tool. Please try again.'))
  assert.ok(isErrorOutput('{"error":"upstream timeout"}'))
  assert.ok(!isErrorOutput('{"error":"x","items":[]}'))
  assert.ok(!isErrorOutput('acme/api@main api/go.mod'))
})

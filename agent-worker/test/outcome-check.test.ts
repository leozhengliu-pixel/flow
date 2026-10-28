/**
 * A worker run reports change outcomes only from its tool calls: Flow keeps the run's write calls from /mcp and
 * checks the run's last word (api/cmd/server/agent_run_outcomes.go). A reply that reports outcomes no write produced
 * is answered with a nudge once: the loop sends it to the model and asks again; Flow's answer to the second reply
 * (its statement, or the reply with its count of the run's writes) is what the run posts. Run: npm test
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
import { FlowAgentWorker } from '../src/worker.js'

setTracingDisabled(true)

const nudge = 'Your last reply reported the outcome of changes (what failed, what succeeded, or a permission problem), but no write tool ran in this reply.'
const statement = '未执行任何更改：本次回复没有调用任何写入工具，因此没有任何操作被执行（成功 0 项，失败 0 项），也没有进行权限检查，不存在权限错误。如需执行，请再发送一次请求。'

function config(): WorkerConfig {
  return { protocol: 'openai-chat', model: 'scripted', apiKey: 'test', contextWindow: 200_000, flowUrl: 'http://flow.invalid', flowToken: 'unused', stateDir: mkdtempSync(join(tmpdir(), 'outcome-check-')), maxTurns: 24 }
}

/** The text of the last user message a model call read. */
function lastUserText(request: any): string {
  const input = Array.isArray(request.input) ? request.input : []
  for (let index = input.length - 1; index >= 0; index--) {
    const item = input[index]
    if (item?.role !== 'user') continue
    if (typeof item.content === 'string') return item.content
    return (item.content ?? []).map((part: any) => part.text ?? '').join('')
  }
  return ''
}

/** A fake guard API: finish answers come from `finishes`, in order; everything else is the enabled view. */
function guardApi(finishes: GuardResponse[]) {
  const requests: GuardRequest[] = []
  const api = async (request: GuardRequest): Promise<GuardResponse> => {
    requests.push(request)
    const view = { enabled: true, codingDue: false, decisionRound: -1 }
    if (request.op === 'finish') {
      const next = finishes.shift()
      assert.ok(next, `unexpected finish ${JSON.stringify(request)}`)
      return { ...view, ...next }
    }
    return view
  }
  return { api, requests }
}

function saveIssue(calls: string[], fail: (input: any) => string | undefined) {
  return tool<any>({
    name: 'save_issue',
    description: 'Create or update an issue',
    strict: false,
    parameters: { type: 'object', properties: { id: { type: 'string' }, team: { type: 'string' } }, additionalProperties: true },
    execute: async (input: any) => {
      calls.push(input.id)
      const error = fail(input)
      return error ? `Error: ${error}` : JSON.stringify({ identifier: input.id, team: input.team })
    },
  })
}

test('a text-only failure claim is sent back once, then the run posts Flow\'s statement', async () => {
  const invented = '两个 issue 都迁移失败：你没有权限将 issue 移入私有团队 VLT。'
  const { api, requests } = guardApi([{ enabled: true, codingDue: false, decisionRound: -1, nudge }, { enabled: true, codingDue: false, decisionRound: -1, reply: statement }])
  const model = new ScriptedModel([[assistantMessage(invented)], [assistantMessage(invented)]])
  const calls: string[] = []
  const worker = new FlowAgentWorker(config(), { model })
  const outcome = await worker.session({ sessionId: 'outcome-text-only', systemPrompt: 'test', guard: api, tools: [saveIssue(calls, () => undefined)], mcpServers: [] }).send('把 ENG-1 和 ENG-2 迁移到 VLT 团队')

  assert.equal(model.remainingSteps, 0, 'the invented answer and one retry')
  assert.equal(lastUserText(model.calls[1]!.request), nudge, 'the retry reads the nudge as the next message')
  const finishes = requests.filter(item => item.op === 'finish')
  assert.deepEqual(finishes, [{ op: 'finish', phase: 'reply', text: invented }, { op: 'finish', phase: 'reply', text: invented }])
  assert.equal(outcome.status, 'response')
  assert.equal(outcome.body, statement, 'the run posts Flow\'s statement, not the invented failure')
  assert.deepEqual(calls, [], 'nothing was written')
})

test('after the nudge the model makes the real calls; Flow\'s counts and correction are the reply', async () => {
  const corrected = 'Correction from Flow: of 2 change(s), 1 succeeded, 1 failed and 0 were not executed.\n\n**Result from Flow:** 2 change(s) requested: 2 executed (1 succeeded, 1 failed), 0 not executed.\n- Failed: ENG-2 (save_issue): team "VLT" not found'
  const { api, requests } = guardApi([{ enabled: true, codingDue: false, decisionRound: -1, nudge }, { enabled: true, codingDue: false, decisionRound: -1, reply: corrected }])
  const model = new ScriptedModel([
    [assistantMessage('I could not move ENG-1 or ENG-2: you do not have permission.')],
    [functionCall('save_issue', { id: 'ENG-1', team: 'VLT' }, { callId: 'a' }), functionCall('save_issue', { id: 'ENG-2', team: 'VLT' }, { callId: 'b' })],
    [assistantMessage('Both moves failed.')],
  ])
  const calls: string[] = []
  const worker = new FlowAgentWorker(config(), { model })
  const outcome = await worker.session({ sessionId: 'outcome-partial', systemPrompt: 'test', guard: api, tools: [saveIssue(calls, input => (input.id === 'ENG-2' ? 'team "VLT" not found' : undefined))], mcpServers: [] }).send('Move ENG-1 and ENG-2 to the VLT team')

  assert.equal(model.remainingSteps, 0)
  assert.deepEqual(calls, ['ENG-1', 'ENG-2'], 'the nudged model made the real calls')
  assert.deepEqual(requests.filter(item => item.op === 'finish').map(item => (item as { text: string }).text), ['I could not move ENG-1 or ENG-2: you do not have permission.', 'Both moves failed.'])
  assert.equal(outcome.body, corrected, 'the run posts Flow\'s account of its writes')
})

test('the nudge is followed at most once, whatever Flow answers', async () => {
  const { api } = guardApi([{ enabled: true, codingDue: false, decisionRound: -1, nudge }, { enabled: true, codingDue: false, decisionRound: -1, nudge }])
  const model = new ScriptedModel([[assistantMessage('All of them failed.')], [assistantMessage('Nothing was changed.')]])
  const worker = new FlowAgentWorker(config(), { model })
  const outcome = await worker.session({ sessionId: 'outcome-once', systemPrompt: 'test', guard: api, mcpServers: [] }).send('Move ENG-1 to VLT')
  assert.equal(model.remainingSteps, 0)
  assert.equal(outcome.body, 'Nothing was changed.')
})

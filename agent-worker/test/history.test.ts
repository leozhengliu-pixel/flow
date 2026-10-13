import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { AgentInputItem } from '@openai/agents'
import { compactHistory } from '../src/history.js'

const user = (text: string) => ({ type: 'message', role: 'user', content: text }) as AgentInputItem
const call = (id: string) => ({ type: 'function_call', callId: id, name: 'list_issues', arguments: '{}' }) as AgentInputItem
const result = (id: string, size: number) => ({ type: 'function_call_result', callId: id, name: 'list_issues', status: 'completed', output: { type: 'text', text: 'x'.repeat(size) } }) as AgentInputItem

test('old large tool results are elided; the current request keeps its results', () => {
  const input = [user('a'), call('1'), result('1', 50_000), user('b'), call('2'), result('2', 50_000)]
  const out = compactHistory(input, { maxOldResultChars: 6_000, budgetTokens: 1e9, keepRequests: 4 })
  assert.equal(out.elided, 1)
  assert.ok(JSON.stringify(out.input[2]).length < 2_000)
  assert.equal((out.input[5] as any).output.text.length, 50_000)
})

test('over budget: whole earlier requests are dropped, call/result pairs stay together', () => {
  const input = Array.from({ length: 10 }, (_, i) => [user(`q${i}`), call(`${i}`), result(`${i}`, 3_000)]).flat()
  const out = compactHistory(input, { maxOldResultChars: 6_000, budgetTokens: 4_000, keepRequests: 2 })
  assert.ok(out.dropped > 0)
  assert.equal((out.input[0] as any).role, 'user')
  const calls = out.input.filter(i => i.type === 'function_call').length
  assert.equal(calls, out.input.filter(i => i.type === 'function_call_result').length)
})

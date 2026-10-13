import assert from 'node:assert/strict'
import { test } from 'node:test'
import { partialJSONStringField, QuestionStreamer } from '../src/question-stream.js'

test('partial JSON string field', () => {
  assert.equal(partialJSONStringField('{"question": "Which pri', 'question'), 'Which pri')
  assert.equal(partialJSONStringField('{"question":"a\\nb \\u00e9\\u', 'question'), 'a\nb é')
  assert.equal(partialJSONStringField('{"options":[{"label":"x"}],"question":"done"}', 'question'), 'done')
  assert.equal(partialJSONStringField('{"quest', 'question'), '')
})

test('streams ask_user question from each protocol, ignores other tools', () => {
  const responses = new QuestionStreamer()
  responses.feed({ type: 'response.output_item.added', item: { type: 'function_call', id: 'fc_1', call_id: 'call_1', name: 'ask_user' } })
  assert.deepEqual(responses.feed({ type: 'response.function_call_arguments.delta', item_id: 'fc_1', delta: '{"question":"Which prio' }), { callId: 'call_1', text: 'Which prio' })

  const chat = new QuestionStreamer()
  chat.feed({ choices: [{ delta: { tool_calls: [{ index: 0, id: 'call_2', function: { name: 'get_issue', arguments: '{"id":' } }] } }] })
  assert.deepEqual(chat.feed({ choices: [{ delta: { tool_calls: [{ index: 1, id: 'call_3', function: { name: 'ask_user', arguments: '{"question":"Pick' } }] } }] }), { callId: 'call_3', text: 'Pick' })
  assert.deepEqual(chat.feed({ choices: [{ delta: { tool_calls: [{ index: 1, function: { arguments: ' one?"' } }] } }] }), { callId: 'call_3', text: 'Pick one?' })

  const anthropic = new QuestionStreamer()
  anthropic.feed({ type: 'tool-input-start', id: 'toolu_1', toolName: 'ask_user' })
  assert.deepEqual(anthropic.feed({ type: 'tool-input-delta', id: 'toolu_1', delta: '{"question": "高' }), { callId: 'toolu_1', text: '高' })
})

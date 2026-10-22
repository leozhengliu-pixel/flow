import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { ConnectorElicitation } from '../src/activities.js'
import { ConnectorElicitationWatcher } from '../src/connector-elicitation.js'

const question = (status: ConnectorElicitation['status'], action?: string): ConnectorElicitation => ({
  id: 'elicit_1',
  sessionId: 'agent_session_1',
  connectorName: 'Deploy',
  connectorUrl: 'https://deploy.example/mcp',
  mode: 'form',
  message: 'Choose a region',
  status,
  ...(action ? { action } : {}),
  answerPath: '/api/agent/sessions/agent_session_1/elicitations/elicit_1',
})

// While a connector call is in flight the worker shows the connector's question once, then its resolution (the
// last look after the call ends delivers it); with no call in flight it does not poll.
test('connector questions are shown once each while a connector call is in flight', async () => {
  const calls: number[] = []
  const answers = [
    { supported: true, version: 1, items: [question('pending')] },
    { supported: true, version: 1, items: [question('pending')] },
  ]
  const api = async (after: number, waitMs: number, signal: AbortSignal) => {
    calls.push(after)
    if (waitMs === 0) return { supported: true, version: 2, items: [question('completed', 'accept')] }
    const answer = answers.shift()
    if (answer) return answer
    return new Promise<never>((_, reject) => signal.addEventListener('abort', () => reject(new Error('aborted'))))
  }
  const shown: ConnectorElicitation[] = []
  const watcher = new ConnectorElicitationWatcher(api, item => shown.push(item), { waitMs: 20_000, retryMs: 5 })
  await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(calls.length, 0, 'no poll without a connector call')

  const end = watcher.begin()
  const started = Date.now()
  while (shown.length < 1 && Date.now() - started < 2000) await new Promise(resolve => setTimeout(resolve, 5))
  await new Promise(resolve => setTimeout(resolve, 20))
  assert.deepEqual(shown.map(item => item.status), ['pending'], 'a repeated answer is not shown again')
  assert.equal(calls[1], 1, 'the next poll asks for changes after the version seen')
  await end()
  assert.deepEqual(shown.map(item => [item.status, item.action]), [['pending', undefined], ['completed', 'accept']])
})

// A run without a person to ask (a thread or loop run) has no questions: the watcher stops asking for good.
test('a run without connector questions is asked once', async () => {
  let calls = 0
  const watcher = new ConnectorElicitationWatcher(async () => {
    calls++
    return { supported: false, version: 0, items: [] }
  }, () => assert.fail('nothing to show'), { waitMs: 20_000, retryMs: 5 })
  await watcher.begin()()
  await watcher.begin()()
  assert.equal(calls, 1)
})

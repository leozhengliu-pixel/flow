import { afterEach, describe, expect, it } from 'vitest'
import type { ApplicationActivity } from '@/lib/application-agents'
import { makeSession } from './coding-session-test-fixtures'
import {
  buildStreamItems,
  buildStreamTurns,
  commitsByTurn,
  currentPlanStep,
  durationParts,
  formatDuration,
  isCodingSessionGenerating,
  pendingElicitation,
  readQueuedMessages,
  selectOptionsInline,
  visibleActivities,
  workedForLabel,
  writeQueuedMessages,
} from './coding-session-stream-model'

let seq = 0
const at = (second: number) => new Date(Date.UTC(2026, 9, 19, 10, 0, second)).toISOString()
const act = (type: string, body = type, extra: Partial<ApplicationActivity> = {}): ApplicationActivity => ({ id: `a${++seq}`, sessionId: 't', actorId: type === 'prompt' ? 'u' : 'app', type, body, createdAt: at(seq), ...extra })
const identity = (text: string) => text

afterEach(() => { seq = 0; localStorage.clear() })

describe('visible activities', () => {
  it('drops deleted, unknown and superseded ephemeral activities and thoughts repeated by the response', () => {
    const old = act('thought', 'old', { ephemeral: true })
    const repeated = act('thought', 'Done.')
    const response = act('response', 'Done.')
    const removed = act('response', 'gone')
    const marker = act('deleted', '', { metadata: { activityId: removed.id } })
    const live = act('action', 'Read', { ephemeral: true })
    expect(visibleActivities([old, act('feedback'), repeated, response, removed, marker, live]).map(item => item.body)).toEqual(['Done.', 'Read'])
  })
})

describe('stream items', () => {
  it('folds two or more adjacent thoughts and actions into a finished group', () => {
    const items = buildStreamItems([act('prompt', 'go'), act('thought', 'plan'), act('action', 'Read', { metadata: { action: 'Read', parameter: '`a.ts`' } }), act('response', 'done')], false)
    expect(items.map(item => item.kind)).toEqual(['prompt', 'group', 'text'])
    const group = items[1]
    expect(group.kind === 'group' && group.loading).toBe(false)
    expect(group.kind === 'group' && group.children.map(child => child.kind)).toEqual(['reasoning', 'action'])
  })

  it('keeps a lone action as its own row, but groups a lone thought before a text', () => {
    expect(buildStreamItems([act('prompt'), act('action', 'Edit'), act('error', 'x')], false).map(item => item.kind)).toEqual(['prompt', 'action', 'error'])
    expect(buildStreamItems([act('prompt'), act('thought', 'hm'), act('response', 'ok')], false).map(item => item.kind)).toEqual(['prompt', 'group', 'text'])
  })

  it('marks the trailing step live while generating and promotes the latest thought', () => {
    const items = buildStreamItems([act('prompt'), act('thought', 'Looking at the tests'), act('action', 'Bash', { metadata: { action: 'Bash', parameter: '`npm test`' } })], true)
    const group = items.at(-1)!
    expect(group.kind).toBe('group')
    if (group.kind !== 'group') return
    expect(group.loading).toBe(true)
    expect(group.live?.metadata?.action).toBe('Bash')
    expect(group.promoted).toBe('Looking at the tests')
    expect(items.some(item => item.kind === 'thinking')).toBe(false)
  })

  it('shows Thinking… while generating before the first step, and a live lone thought as a working group', () => {
    expect(buildStreamItems([act('prompt')], true).map(item => item.kind)).toEqual(['prompt', 'thinking'])
    const items = buildStreamItems([act('prompt'), act('thought', 'hmm')], true)
    expect(items.map(item => item.kind)).toEqual(['prompt', 'group'])
    expect(items[1].kind === 'group' && items[1].live).toBeUndefined()
  })

  it('folds runs of actions into "Used N tools" when a group has more than 20 children', () => {
    const activities = [act('prompt'), act('thought', 'start'), ...Array.from({ length: 21 }, (_, index) => act('action', `Read ${index}`)), act('response', 'ok')]
    const group = buildStreamItems(activities, false)[1]
    expect(group.kind === 'group' && group.children.map(child => child.kind)).toEqual(['reasoning', 'tools'])
    expect(group.kind === 'group' && group.children[1].kind === 'tools' && group.children[1].actions).toHaveLength(21)
  })

  it('maps stop prompts, auth questions and errors', () => {
    const items = buildStreamItems([act('prompt', 'Stop requested', { signal: 'stop' }), act('elicitation', 'Connect', { signal: 'auth' }), act('elicitation', 'Which?', { signal: 'select' }), act('error', 'boom')], false)
    expect(items.map(item => item.kind)).toEqual(['prompt', 'auth', 'text', 'error'])
    expect(items[0].kind === 'prompt' && items[0].stop).toBe(true)
  })
})

describe('turns', () => {
  it('starts a turn at each prompt and shows the time before the first turn and after a day change', () => {
    const first = act('prompt', 'one', { createdAt: '2026-10-18T10:00:00Z' })
    const reply = act('response', 'r', { createdAt: '2026-10-18T10:00:05Z' })
    const sameDay = act('prompt', 'two', { createdAt: '2026-10-18T11:00:00Z' })
    const nextDay = act('prompt', 'three', { createdAt: '2026-10-19T11:00:00Z' })
    const turns = buildStreamTurns(buildStreamItems([first, reply, sameDay, nextDay], false))
    expect(turns.map(turn => [turn.prompt?.activity.body, turn.items.length, turn.showTime])).toEqual([['one', 1, true], ['two', 0, false], ['three', 0, true]])
  })

  it('opens a turn without a prompt when the agent started the stream', () => {
    const turns = buildStreamTurns(buildStreamItems([act('action', 'Setting up'), act('response', 'hi')], false))
    expect(turns).toHaveLength(1)
    expect(turns[0].prompt).toBeUndefined()
    expect(turns[0].showTime).toBe(true)
  })

  it('attaches commits to their turn; the first turn also takes the initial turn id', () => {
    const prompt = act('prompt', 'more')
    const turns = buildStreamTurns(buildStreamItems([act('response', 'first'), prompt, act('response', 'second')], false))
    const commit = (sha: string) => ({ sha, branch: 'b', signed: false, createdAt: '' })
    const session = makeSession({ id: 'cs-1', commitsByTurnActivityId: { 'turn_cs-1': [commit('aaa')], [prompt.id]: [commit('bbb')], unknown: [commit('ccc')] } })
    const map = commitsByTurn(turns, session)
    expect(map.get('start')?.map(item => item.sha)).toEqual(['aaa'])
    expect(map.get(prompt.id)?.map(item => item.sha)).toEqual(['bbb', 'ccc'])
  })
})

describe('durations', () => {
  it.each([
    [800, 1, 'second'], [11_000, 11, 'second'], [59_400, 59, 'second'], [90_000, 2, 'minute'], [59 * 60_000, 59, 'minute'],
    [60 * 60_000, 1, 'hour'], [(60 + 39) * 60_000, 1, 'hour'], [(60 + 40) * 60_000, 2, 'hour'], [9 * 3600_000, 9, 'hour'], [3 * 86400_000, 3, 'day'], [15 * 86400_000, 2, 'week'],
  ])('%i ms → %i %s', (ms, count, unit) => {
    const parts = durationParts(ms)
    expect(parts.unit).toBe(unit)
    if (count) expect(parts.count).toBe(count)
  })

  it('writes full unit names and "Worked for …"', () => {
    expect(formatDuration(1000, identity)).toBe('1 second')
    expect(formatDuration(180_000, identity)).toBe('3 minutes')
    expect(workedForLabel(at(0), at(11), identity)).toBe('Worked for 11 seconds')
    expect(workedForLabel(at(5), at(5), identity)).toBe('Worked for 1 second')
    expect(workedForLabel(undefined, at(5), identity)).toBe('Worked for a while')
  })
})

describe('plan, options, questions, queue', () => {
  it('picks the current plan step', () => {
    expect(currentPlanStep([{ content: 'a', status: 'completed' }, { content: 'b', status: 'inProgress' }, { content: 'c', status: 'pending' }])?.index).toBe(1)
    expect(currentPlanStep([{ content: 'a', status: 'completed' }, { content: 'c', status: 'pending' }])?.index).toBe(1)
    expect(currentPlanStep([{ content: 'a', status: 'completed' }, { content: 'b', status: 'canceled' }])?.index).toBe(1)
    expect(currentPlanStep([])).toBeUndefined()
  })

  it('inlines one option or two short ones, else a dropdown', () => {
    expect(selectOptionsInline([{ value: 'a very long option label that goes on and on' }])).toBe(true)
    expect(selectOptionsInline([{ value: 'Yes' }, { value: 'No' }])).toBe(true)
    expect(selectOptionsInline([{ value: 'https://github.com/acme/app' }, { value: 'No' }])).toBe(false)
    expect(selectOptionsInline([{ value: 'a' }, { value: 'b' }, { value: 'c' }])).toBe(false)
  })

  it('finds the unanswered question', () => {
    const question = act('elicitation', 'Which?')
    expect(pendingElicitation([act('prompt'), question])).toBe(question)
    expect(pendingElicitation([question, act('prompt', 'that one')])).toBeUndefined()
  })

  it('reports generating for pending and active sessions only', () => {
    expect(isCodingSessionGenerating(makeSession({ state: 'active', phase: 'running' }))).toBe(true)
    expect(isCodingSessionGenerating(makeSession({ state: 'awaitingInput', phase: 'awaitingUser' }))).toBe(false)
    expect(isCodingSessionGenerating(makeSession({ state: 'pending', phase: 'awaitingApproval' }))).toBe(false)
  })

  it('persists the queue per session', () => {
    writeQueuedMessages('ws', 's1', [{ id: 'q1', text: 'next', createdAt: '' }])
    expect(readQueuedMessages('ws', 's1').map(item => item.text)).toEqual(['next'])
    expect(readQueuedMessages('ws', 's2')).toEqual([])
    writeQueuedMessages('ws', 's1', [])
    expect(localStorage.length).toBe(0)
  })
})

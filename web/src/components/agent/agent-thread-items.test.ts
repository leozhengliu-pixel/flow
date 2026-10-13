import { describe, expect, it } from 'vitest'
import type { ApplicationActivity } from '@/lib/application-agents'
import { agentThreadItems, agentThreadReactions, agentWorkingPhase } from './agent-thread-items'

const activity = (id: string, type: string, body: string, extra: Partial<ApplicationActivity> = {}): ApplicationActivity => ({ id, type, body, sessionId: 'task', actorId: 'app', createdAt: '', ...extra })
const question = activity('q', 'elicitation', 'Language?', { signal: 'select', signalMetadata: { options: [{ value: 'English' }, { value: 'Chinese' }] } })

describe('application agent thread', () => {
  it('groups streamed thoughts and replaces only the current answer on completion', () => {
    const items = agentThreadItems([
      activity('1', 'thought', 'Read '), activity('2', 'thought', 'context'),
      activity('3', 'output', 'Par'), activity('4', 'output', 'tial'), activity('5', 'response', 'Final'),
      activity('6', 'prompt', 'Follow up'), activity('7', 'output', 'Next'),
    ])
    expect(items.map(item => item.body)).toEqual(['Read context', 'Final', 'Follow up', 'Next'])
    expect(items.at(-1)?.output).toBe(true)
  })
  it('resolves a selection and preserves an accompanying reply', () => {
    const items = agentThreadItems([question, activity('2', 'prompt', 'Chinese\n\nKeep it brief'), activity('3', 'response', 'Answer')])
    expect(items[0]).toMatchObject({ selected: 'Chinese', resolved: true })
    expect(items.map(item => item.body)).toEqual(['Language?', 'Keep it brief', 'Answer'])
  })
  it('does not hide later prompts that happen to match an old selection', () => {
    const items = agentThreadItems([question, activity('2', 'prompt', 'Chinese'), activity('3', 'response', 'Answer'), activity('4', 'prompt', 'English')])
    expect(items.map(item => item.body)).toEqual(['Language?', 'Answer', 'English'])
  })
  it('keeps an unanswered question pending and closes it on cancellation', () => {
    expect(agentThreadItems([question])[0].resolved).toBeUndefined()
    expect(agentThreadItems([question, activity('2', 'canceled', 'Stopped')])[0].resolved).toBe(true)
  })
  it('persists independent reactions and hides feedback from conversation messages', () => {
    const feedback = (id: string, actorId: string, enabled: boolean) => activity(id, 'feedback', '👍', { actorId, metadata: { activityId: 'q', enabled } })
    const activities=[question, feedback('2','one',true),feedback('3','two',true),feedback('4','one',false)]
    expect(agentThreadItems(activities)).toHaveLength(1)
    expect(agentThreadReactions(activities)).toEqual({q:{'👍':['two']}})
    expect(agentThreadItems(activities)[0].resolved).toBeUndefined()
  })
  it('drops replies a person deleted from the thread', () => {
    const items = agentThreadItems([activity('1', 'response', 'First'), activity('2', 'deleted', '', { metadata: { activityId: '1' } }), activity('3', 'prompt', 'Again'), activity('4', 'response', 'Second')])
    expect(items.map(item => item.body)).toEqual(['Again', 'Second'])
  })
  it('names the working phase: "Working…" first, then the running step of the current turn', () => {
    const t = (source: string) => source
    expect(agentWorkingPhase([activity('1', 'prompt', 'Go'), activity('2', 'thought', 'Planning')], t)).toBe('Working…')
    expect(agentWorkingPhase([activity('1', 'prompt', 'Go'), activity('2', 'action', 'search_issues')], t)).toMatch(/…$/)
    expect(agentWorkingPhase([activity('1', 'action', 'search_issues'), activity('2', 'prompt', 'Next')], t)).toBe('Working…')
    expect(agentWorkingPhase([activity('1', 'prompt', 'Go'), activity('2', 'action', '', { metadata: { action: 'Researching export risks' } })], t)).toBe('Researching export risks…')
  })
})

describe('suggestion blocks in threads', () => {
  it('drops the chat-only ```suggestions block from replies, questions and streamed output, never from people', () => {
    const fence = '\n\n```suggestions\nShow the history\n```'
    const items = agentThreadItems([
      activity('1', 'response', `Answer${fence}`),
      activity('2', 'prompt', `Quote${fence}`, { actorId: 'user' }),
      activity('3', 'elicitation', `Which one?${fence}`),
      activity('4', 'prompt', 'This one', { actorId: 'user' }),
      activity('5', 'output', 'Streaming\n\n```sugg'),
    ])
    expect(items.map(item => item.body)).toEqual(['Answer', `Quote${fence}`, 'Which one?', 'This one', 'Streaming'])
  })
})

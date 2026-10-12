import { describe, expect, it } from 'vitest'
import type { ApplicationActivity } from '@/lib/application-agents'
import { agentThreadItems, agentThreadReactions } from './agent-thread-items'

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
})

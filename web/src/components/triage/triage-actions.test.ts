import { describe, expect, it } from 'vitest'
import type { Issue, WorkflowState } from '@/types/flow'
import { canceledState, DEFAULT_TRIAGE_DISPLAY, isSnoozed, parseSnoozeInput, readTriageDisplay, snoozePresets, sortTriageIssues, writeTriageDisplay } from './triage-model'

const state = (id: string, name: string, type: WorkflowState['type'], teamId = 'team'): WorkflowState => ({ id, name, type, teamId, color: '#999', position: 0 } as WorkflowState)
const issue = (id: string, fields: Partial<Issue>) => ({ id, createdAt: '2026-09-01T00:00:00Z', priority: 0, ...fields } as Issue)

describe('triage model', () => {
  it('declines into the team canceled state, not a Duplicate state', () => {
    const states = [state('dup', 'Duplicate', 'canceled'), state('can', 'Canceled', 'canceled'), state('other', 'Canceled', 'canceled', 'other-team')]
    expect(canceledState(states, 'team')?.id).toBe('can')
  })

  it("offers Linear's snooze presets and detects active snoozes", () => {
    const now = new Date(2026, 8, 24, 10, 0) // Thursday
    const presets = snoozePresets(now)
    expect(presets.map(item => item.label)).toEqual(['An hour from now', 'Tomorrow', 'Next week', 'A month from now'])
    expect(presets[0].until.getTime() - now.getTime()).toBe(3_600_000)
    expect([presets[1].until.getDate(), presets[1].until.getHours()]).toEqual([25, 9])
    expect([presets[2].until.getDay(), presets[2].until.getHours()]).toEqual([1, 9])
    expect([presets[3].until.getMonth(), presets[3].until.getHours()]).toEqual([9, 9])
    expect(isSnoozed({ snoozedUntil: '2026-09-25T00:00:00Z' }, now.getTime())).toBe(true)
    expect(isSnoozed({ snoozedUntil: '2026-09-23T00:00:00Z' }, now.getTime())).toBe(false)
    expect(isSnoozed({}, now.getTime())).toBe(false)
  })

  it('parses natural-language snooze input', () => {
    const now = new Date(2026, 8, 24, 10, 0)
    const at = (text: string) => parseSnoozeInput(text, now)
    expect(at('4 pm')).toEqual(new Date(2026, 8, 24, 16, 0))
    expect(at('9am')).toEqual(new Date(2026, 8, 25, 9, 0))
    expect(at('2 days')!.getTime() - now.getTime()).toBe(2 * 86_400_000)
    expect(at('in 5 weeks')!.getTime() - now.getTime()).toBe(35 * 86_400_000)
    expect(at('an hour')!.getTime() - now.getTime()).toBe(3_600_000)
    expect(at('tomorrow')).toEqual(new Date(2026, 8, 25, 9, 0))
    expect(at('monday')).toEqual(new Date(2026, 8, 28, 9, 0))
    expect(at('in 2 months')).toEqual(new Date(2026, 10, 24, 10, 0))
    expect(at('banana')).toBeUndefined()
    expect(at('13 pm')).toBeUndefined()
  })

  it('orders by added-to-triage, priority and due date in either direction', () => {
    const list = [
      issue('a', { statusChangedAt: '2026-09-03T00:00:00Z', priority: 3, dueDate: '2026-10-03' }),
      issue('b', { statusChangedAt: '2026-09-05T00:00:00Z', priority: 1 }),
      issue('c', { statusChangedAt: '2026-09-04T00:00:00Z', priority: 0, dueDate: '2026-10-01' }),
    ]
    const ids = (ordering: 'addedToTriage' | 'priority' | 'dueDate', newestFirst = true) => sortTriageIssues(list, { ordering, newestFirst }).map(item => item.id)
    expect(ids('addedToTriage')).toEqual(['b', 'c', 'a'])
    expect(ids('addedToTriage', false)).toEqual(['a', 'c', 'b'])
    expect(ids('priority')).toEqual(['b', 'a', 'c'])
    expect(ids('dueDate')).toEqual(['c', 'a', 'b'])
    expect(ids('dueDate', false)).toEqual(['a', 'c', 'b'])
  })

  it('persists display settings and falls back to the defaults', () => {
    localStorage.clear()
    expect(readTriageDisplay('k')).toEqual(DEFAULT_TRIAGE_DISPLAY)
    writeTriageDisplay('k', { ordering: 'dueDate', newestFirst: false, showSnoozed: true, properties: ['id'] })
    expect(readTriageDisplay('k')).toEqual({ ordering: 'dueDate', newestFirst: false, showSnoozed: true, properties: ['id'] })
    localStorage.setItem('k', '{"ordering":"bogus","properties":["x"]}')
    expect(readTriageDisplay('k')).toEqual({ ...DEFAULT_TRIAGE_DISPLAY, properties: [] })
    localStorage.clear()
  })
})

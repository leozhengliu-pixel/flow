import { describe, expect, it } from 'vitest'
import type { WorkflowState } from '@/types/flow'
import { canceledState, isSnoozed, snoozePresets } from './triage-actions'

const state = (id: string, name: string, type: WorkflowState['type'], teamId = 'team'): WorkflowState => ({ id, name, type, teamId, color: '#999', position: 0 } as WorkflowState)

describe('triage actions', () => {
  it('declines into the team canceled state, not a Duplicate state', () => {
    const states = [state('dup', 'Duplicate', 'canceled'), state('can', 'Canceled', 'canceled'), state('other', 'Canceled', 'canceled', 'other-team')]
    expect(canceledState(states, 'team')?.id).toBe('can')
  })

  it('offers future snooze presets and detects active snoozes', () => {
    const now = new Date('2026-09-24T10:00:00Z')
    const presets = snoozePresets(now)
    expect(presets.map(item => item.label)).toEqual(['Tomorrow', 'Next week', 'In two weeks', 'In a month'])
    expect(presets.every(item => item.until.getTime() > now.getTime())).toBe(true)
    expect(presets[1].until.getDay()).toBe(1)
    expect(isSnoozed({ snoozedUntil: '2026-09-25T00:00:00Z' }, now.getTime())).toBe(true)
    expect(isSnoozed({ snoozedUntil: '2026-09-23T00:00:00Z' }, now.getTime())).toBe(false)
    expect(isSnoozed({}, now.getTime())).toBe(false)
  })
})

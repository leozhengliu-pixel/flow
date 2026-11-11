import { describe, expect, it } from 'vitest'
import type { SLARule } from '@/types/flow'
import {
  conditionsToRule,
  draftFromRule,
  draftMinutes,
  formatSlaDuration,
  ruleAction,
  ruleConditions,
  ruleNameFor,
  slaFeatureEnabled,
  splitDuration,
  validAmount,
} from './sla-model'

const teams = [{ id: 'a', name: 'Alpha' }, { id: 'b', name: 'Beta' }] as never
const rule = (overrides: Partial<SLARule> = {}): SLARule => ({
  id: 'r', name: 'Rule', teamIds: [], filters: {}, targetMinutes: 1440, pauseStatuses: [], businessHours: false, enabled: true, createdAt: '', updatedAt: '', ...overrides,
})
const t = (value: string) => value

describe('SLA durations', () => {
  it('reads whole weeks, then days beyond the first, then hours, then minutes', () => {
    expect(splitDuration(10080, false)).toEqual({ amount: 1, unit: 'weeks' })
    expect(splitDuration(20160, false)).toEqual({ amount: 2, unit: 'weeks' })
    expect(splitDuration(4320, false)).toEqual({ amount: 3, unit: 'days' })
    expect(splitDuration(1440, false)).toEqual({ amount: 24, unit: 'hours' })
    expect(splitDuration(90, false)).toEqual({ amount: 90, unit: 'minutes' })
    expect(splitDuration(7200, true)).toEqual({ amount: 1, unit: 'weeks' })
    expect(splitDuration(4320, true)).toEqual({ amount: 3, unit: 'days' })
    expect(splitDuration(480, true)).toEqual({ amount: 8, unit: 'hours' })
  })

  it('formats every duration as one translatable phrase', () => {
    expect(formatSlaDuration(1440, false, t)).toBe('24 hours')
    expect(formatSlaDuration(10080, false, t)).toBe('1 week')
    expect(formatSlaDuration(60, false, t)).toBe('1 hour')
    expect(formatSlaDuration(4320, true, t)).toBe('3 business days')
    expect(formatSlaDuration(7200, true, t)).toBe('1 business week')
    expect(formatSlaDuration(1000, false, (value) => `[${value}]`)).toBe('[{count} minutes]'.replace('{count}', '1,000'))
  })

  it('turns a draft back into minutes in the unit it was written in', () => {
    const draft = draftFromRule(rule({ targetMinutes: 4320, businessHours: true }), { teams })
    expect(draft).toMatchObject({ amount: 3, unit: 'days', businessHours: true, action: 'add' })
    expect(draftMinutes(draft)).toBe(4320)
    expect(draftMinutes({ ...draft, businessHours: false })).toBe(3 * 1440)
  })

  it('accepts only whole numbers from 1 up', () => {
    expect([0, 1.5, -2, NaN, 100000].some(validAmount)).toBe(false)
    expect([1, 24, 99999].every(validAmount)).toBe(true)
  })
})

describe('SLA rule conditions', () => {
  it('lists priority, team, status, label and project in that order', () => {
    const conditions = ruleConditions(rule({ teamIds: ['a'], filters: { project: 'p', priority: 1, label: 'l', status: 'started' } }), { teams })
    expect(conditions.map((condition) => condition.kind)).toEqual(['priority', 'team', 'status', 'label', 'project'])
    expect(conditions[0]).toEqual({ kind: 'priority', value: '1' })
  })

  it('treats a priority of 0 as the "No priority" condition', () => {
    expect(ruleConditions(rule({ filters: { priority: 0 } }), { teams })).toEqual([{ kind: 'priority', value: '0' }])
  })

  it('does not show the team list an older rule was saved with as a condition', () => {
    expect(ruleConditions(rule({ teamIds: ['a', 'b'] }), { teams })).toEqual([])
    expect(ruleConditions(rule({ teamIds: ['a'] }), { teams })).toEqual([{ kind: 'team', value: 'a' }])
  })

  it('round-trips conditions into filters and team ids', () => {
    const conditions = ruleConditions(rule({ teamIds: ['a'], filters: { priority: 2, label: 'l' } }), { teams })
    expect(conditionsToRule(conditions)).toEqual({ filters: { priority: 2, label: 'l' }, teamIds: ['a'] })
  })

  it('names a rule after its conditions', () => {
    const data = { teams, labels: [{ id: 'l', name: 'Bug' }], projects: [{ id: 'p', name: 'Launch' }] } as never
    expect(ruleNameFor([{ kind: 'priority', value: '1' }, { kind: 'label', value: 'l' }], data)).toBe('Priority is Urgent and Label is Bug')
    expect(ruleNameFor([{ kind: 'team', value: 'a,b' }, { kind: 'project', value: 'p' }], data)).toBe('Team is Alpha, Beta and Project is Launch')
    expect(ruleNameFor([], data)).toBe('SLA rule')
  })
})

describe('SLA switches', () => {
  it('treats a rule saved before actions existed as one that adds an SLA', () => {
    expect(ruleAction(rule())).toBe('add')
    expect(ruleAction(rule({ action: 'remove' }))).toBe('remove')
  })

  it('follows an explicit choice, and a workspace that never chose keeps the rules it has running', () => {
    expect(slaFeatureEnabled({ settings: { sla: { enabled: false } }, slaRules: [rule()] } as never)).toBe(false)
    expect(slaFeatureEnabled({ settings: { sla: { enabled: true } }, slaRules: [] } as never)).toBe(true)
    expect(slaFeatureEnabled({ settings: {}, slaRules: [rule()] } as never)).toBe(true)
    expect(slaFeatureEnabled({ settings: {}, slaRules: [] } as never)).toBe(false)
    expect(slaFeatureEnabled({} as never)).toBe(false)
  })
})

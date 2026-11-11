import { describe, expect, it } from 'vitest'
import type { IssueSLA } from '@/types/flow'
import { makeIssue } from '@/test/fixtures'
import { getSlaState, issueSlaFor } from './issue-sla'

const now = Date.parse('2026-11-10T12:00:00Z')
const after = (hours: number) => new Date(now + hours * 3_600_000).toISOString()
const sla = (patch: Partial<IssueSLA> = {}): IssueSLA => ({ id: 'sla', issueId: 'issue', ruleId: 'rule', startedAt: after(-24), dueAt: after(200), status: 'active', remainingMinutes: 0, pausedMinutes: 0, ...patch })

describe('SLA state', () => {
  it('uses inclusive 24-hour and seven-day risk windows and breaches at the deadline', () => {
    expect([200, 168, 24.01, 24, 0, -1].map(hours => getSlaState(sla({ dueAt: after(hours) }), now))).toEqual(['low', 'medium', 'medium', 'high', 'breached', 'breached'])
  })

  it('distinguishes achieved and failed independently of whether the timer swept', () => {
    expect(getSlaState(sla({ completedAt: after(1), dueAt: after(2), status: 'completed' }), now)).toBe('achieved')
    expect(getSlaState(sla({ completedAt: after(2), dueAt: after(2), status: 'completed' }), now)).toBe('failed')
    expect(getSlaState(sla({ completedAt: after(3), dueAt: after(2), status: 'completed' }), now)).toBe('failed')
    expect(getSlaState(sla({ status: 'completed', breachedAt: after(-1) }), now)).toBe('failed')
  })

  it('honors calculated business-day risk boundaries and paused/removed/canceled instances', () => {
    expect(getSlaState({ ...sla(), highRiskAt: after(-1), mediumRiskAt: after(-7) }, now)).toBe('high')
    expect(getSlaState(sla({ status: 'paused', dueAt: after(-1) }), now)).toBe('paused')
    expect(getSlaState(sla({ status: 'removed' }), now)).toBe('none')
    expect(getSlaState({ ...sla(), canceledAt: after(-1) }, now)).toBe('none')
  })

  it('resolves the latest automatic instance and manual deadlines, omitting canceled issues', () => {
    const issue = makeIssue({ id: 'issue', slaBreachesAt: after(10) })
    expect(issueSlaFor(issue, [sla(), sla({ id: 'latest' })])?.id).toBe('latest')
    expect(issueSlaFor(issue, [])?.dueAt).toBe(after(10))
    expect(issueSlaFor({ ...issue, state: { ...issue.state, type: 'canceled' } }, [sla()])).toBeUndefined()
  })
})

import { describe, expect, it } from 'vitest'
import { teammate, viewer } from '@/test/fixtures'
import type { Presence, User } from '@/types/flow'
import { issueViewers } from './issue-presence'

const third = { ...teammate, id: 'user-3', displayName: 'Third' } as User
const row = (clientId: string, user: User, issueId?: string): Presence => ({ clientId, user, issueId, lastSeenAt: '2026-10-12T00:00:00.000Z' })

describe('issueViewers', () => {
  it('shows one avatar per person even when they have the issue open in several tabs', () => {
    const viewers = issueViewers([row('a', teammate, 'issue-1'), row('b', teammate, 'issue-1'), row('c', third, 'issue-1')], 'issue-1', viewer.id)
    expect(viewers.map(item => item.user.id)).toEqual([teammate.id, third.id])
  })

  it('never lists the viewer, including their other tabs and devices', () => {
    expect(issueViewers([row('tab-1', viewer, 'issue-1'), row('tab-2', viewer, 'issue-1')], 'issue-1', viewer.id)).toEqual([])
  })

  it('keeps only people on this issue', () => {
    expect(issueViewers([row('a', teammate, 'issue-2'), row('b', third), row('c', third, 'issue-1')], 'issue-1', viewer.id).map(item => item.clientId)).toEqual(['c'])
  })
})

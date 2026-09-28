import { describe, expect, it } from 'vitest'
import { boardDropRejectReason } from './issue-board'
import type { MyIssuesGroupData, MyIssuesRowData } from '@/components/my-issues/my-issues-list'

const issue = (priority: 0 | 1 | 2 | 3 | 4): MyIssuesRowData => ({
  id: 'issue-1', identifier: 'FLOW-1', title: 'Triage item', teamId: 'team-1',
  priority, state: { id: 'triage', name: 'Triage', type: 'backlog', color: '#888' },
  createdAt: '2026-09-01T12:00:00Z', updatedAt: '2026-09-02T12:00:00Z',
})

const group = (id: string, label: string): MyIssuesGroupData => ({
  id, label, stateType: 'backlog', issues: [],
})

describe('boardDropRejectReason', () => {
  it('blocks leaving triage without a priority', () => {
    expect(boardDropRejectReason(issue(0), group('triage', 'Triage'), group('started', 'In progress'))).toBe(
      "Can't move out of triage without a priority",
    )
  })

  it('allows leaving triage when priority is set', () => {
    expect(boardDropRejectReason(issue(2), group('triage', 'Triage'), group('started', 'In progress'))).toBeNull()
  })
})

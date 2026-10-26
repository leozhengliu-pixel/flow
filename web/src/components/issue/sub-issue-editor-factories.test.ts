import { describe, expect, it } from 'vitest'
import { backlog, completed, makeBootstrap, makeIssue, started } from '@/test/fixtures'
import type { WorkflowState } from '@/types/flow'
import { createSubIssueInputFromDraft } from './sub-issue-editor-factories'

const state = (id: string, type: WorkflowState['type'], position: number, teamId: string) => ({ id, name: id, color: '#888', type, position, teamId }) as WorkflowState

describe('sub-issue defaults', () => {
  it("starts a sub-issue of an inheriting sub-team in the parent's first unstarted status, not its own copies", () => {
    const data = makeBootstrap({
      states: [backlog, started, completed, state('parent-ready', 'unstarted', 1, 'team-parent'), state('parent-intake', 'backlog', 0, 'team-parent'), state('own-copy', 'unstarted', 0, 'team-1')],
      teamSettings: { 'team-1': { inheritWorkflowStatuses: true, parentTeamId: 'team-parent' } } as never,
    })
    expect(createSubIssueInputFromDraft(makeIssue(), data).stateId).toBe('parent-ready')
  })

  it('uses the team default when the workflow has no unstarted status', () => {
    const data = makeBootstrap({ states: [backlog, started, completed], teamSettings: { 'team-1': { defaultStateId: started.id } } as never })
    expect(createSubIssueInputFromDraft(makeIssue(), data).stateId).toBe(started.id)
  })
})

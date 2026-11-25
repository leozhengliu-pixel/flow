import { describe, expect, it } from 'vitest'
import { indexMilestoneIssueProgress, milestoneIssueProgress } from './milestone-progress'

describe('indexed milestone progress', () => {
  it('counts issues once across projects and excludes archived and unassigned issues', () => {
    const issues = Array.from({ length: 10_000 }, (_, index) => ({
      project: { id: `p${index % 31}` },
      projectMilestoneId: index % 7 ? `m${index % 13}` : undefined,
      archivedAt: index % 3 ? undefined : '2026-11-24',
      state: { type: ['completed', 'started', 'canceled', 'duplicate'][index % 4] },
    }))
    const progress = indexMilestoneIssueProgress(issues)
    for (const project of ['p0', 'p10', 'p30', 'missing']) {
      for (const milestone of ['m0', 'm1', 'm12', 'missing']) {
        expect(progress(project, milestone)).toBe(milestoneIssueProgress(issues, project, milestone))
      }
    }
  })
})

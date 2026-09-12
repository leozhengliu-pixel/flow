import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { MilestoneProgressIcon } from './milestone-progress-icon'
import { currentProjectMilestone, milestoneIssueProgress, milestoneProgressLength } from './milestone-progress'

const issue = (overrides: Partial<{ id: string; projectId: string; milestoneId: string; type: string; archivedAt?: string }> = {}) => ({
  archivedAt: overrides.archivedAt,
  project: { id: overrides.projectId ?? 'project-1' },
  projectMilestoneId: overrides.milestoneId ?? 'milestone-1',
  state: { type: overrides.type ?? 'started' },
})

describe('milestone progress icon', () => {
  it('counts completed and canceled issues toward progress', () => {
    expect(milestoneIssueProgress([
      issue({ type: 'completed' }),
      issue({ type: 'canceled' }),
      issue({ type: 'started' }),
      issue({ type: 'completed', archivedAt: '2026-01-01' }),
      issue({ projectId: 'other', type: 'completed' }),
    ], 'project-1', 'milestone-1')).toBe(67)
  })

  it('prefers the first incomplete milestone', () => {
    const milestones = [{ id: 'done' }, { id: 'current' }, { id: 'later' }]
    const progress = { done: 100, current: 40, later: 0 }
    expect(currentProjectMilestone(milestones, id => progress[id as keyof typeof progress])?.id).toBe('current')
    expect(currentProjectMilestone([{ id: 'done' }], () => 100)?.id).toBe('done')
  })

  it('renders Linear empty, progress, complete, and unassigned states', () => {
    const empty = render(<MilestoneProgressIcon empty />).container.querySelector('svg')
    expect(empty).toHaveClass('is-empty')
    expect(empty?.querySelectorAll('path')).toHaveLength(1)

    const zero = render(<MilestoneProgressIcon overdue progress={0} />).container.querySelector('svg')
    expect(zero).toHaveClass('is-progress', 'is-overdue')
    expect(zero?.querySelector('.is-value')).toHaveAttribute('stroke-dasharray', `${milestoneProgressLength(0)} ${31 - milestoneProgressLength(0)}`)

    const progress = render(<MilestoneProgressIcon overdue progress={81} />).container.querySelector('svg')
    expect(progress).toHaveClass('is-progress', 'is-overdue')
    expect(progress?.querySelector('.is-value')).toHaveAttribute('stroke-dasharray', `${milestoneProgressLength(81)} ${31 - milestoneProgressLength(81)}`)

    const complete = render(<MilestoneProgressIcon progress={100} />).container.querySelector('svg')
    expect(complete).toHaveClass('is-complete')
    expect(complete?.querySelectorAll('path')).toHaveLength(1)

    const unassigned = render(<MilestoneProgressIcon unassigned />).container.querySelector('svg')
    expect(unassigned).toHaveClass('is-unassigned')
  })
})

import { addDays, format, startOfDay } from 'date-fns'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { buildProgressData, shouldShowProgressGraph } from './project-progress-data'
import { ProjectDetailsSidebar } from './project-details-sidebar'
import type { Issue } from '@/types/flow'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project } from '@/test/fixtures'

function issue(createdAt: Date, state: 'started' | 'completed', index = 0, estimate?: number): Issue {
  const timestamp = createdAt.toISOString()
  return {
    id: `${state}-${timestamp}-${index}`,
    title: 'Synthetic progress issue',
    identifier: 'SYN-1',
    createdAt: timestamp,
    updatedAt: timestamp,
    estimate,
    completedAt: state === 'completed' ? timestamp : undefined,
    state: { id: state, name: state, type: state, color: '#5e6ad2' },
  } as Issue
}

describe('project progress data', () => {
  it('creates a forecast window when the target date is still ahead', () => {
    const today = startOfDay(new Date())
    const start = addDays(today, -14)
    const target = addDays(today, 7)
    const result = buildProgressData([
      issue(addDays(today, -6), 'started', 0),
      ...Array.from({ length: 5 }, (_, index) => issue(addDays(today, -10), 'completed', index + 1)),
    ], format(start, 'yyyy-MM-dd'), format(target, 'yyyy-MM-dd'))

    expect(result.targetDate.getTime()).toBeGreaterThan(result.currentDate.getTime())
    expect(result.forecast.completed).toBe(5)
    expect(result.forecast.optimisticDate).toBeDefined()
    expect(result.forecast.pessimisticDate).toBeDefined()
    expect(result.series.map(item => item.id)).toEqual(['Scope', 'Started', 'Completed', 'Target'])
  })

  it('keeps the target marker while projecting completion beyond an overdue target', () => {
    const today = startOfDay(new Date())
    const start = addDays(today, -14)
    const target = addDays(today, -2)
    const result = buildProgressData([
      issue(addDays(today, -10), 'started', 0),
      ...Array.from({ length: 5 }, (_, index) => issue(addDays(today, -3), 'completed', index + 1)),
    ], format(start, 'yyyy-MM-dd'), format(target, 'yyyy-MM-dd'))

    expect(result.targetDate.getTime()).toBeLessThan(result.currentDate.getTime())
    expect(result.endDate.getTime()).toBeGreaterThan(result.currentDate.getTime())
    expect(result.forecast.optimisticDate?.getTime()).toBeGreaterThan(result.currentDate.getTime())
    expect(result.forecast.pessimisticDate?.getTime()).toBeGreaterThan(result.forecast.optimisticDate?.getTime() ?? 0)
    expect(result.series.map(item => item.id)).toEqual(['Scope', 'Started', 'Completed', 'Target'])
  })

  it('uses persisted weekly history for the rendered series', () => {
    const today = startOfDay(new Date())
    const start = addDays(today, -14)
    const target = addDays(today, 7)
    const history = [0, 3, 6].map((value, index) => ({ date: addDays(start, index * 7).toISOString(), value }))
    const result = buildProgressData([], format(start, 'yyyy-MM-dd'), format(target, 'yyyy-MM-dd'), {
      completedScopeHistory: history,
      inProgressScopeHistory: history,
      issueCountHistory: history,
      progressHistory: history,
      scopeHistory: history,
    })

    expect(result.series[0].data.map(point => point.y)).toEqual([0, 3, 6, 6])
    expect(result.series[2].data.map(point => point.y)).toEqual([0, 3, 6])
    expect(result.series[1].data.map(point => point.y)).toEqual([0, 6, 12])
    expect(result.series[3].data.every(point => point.y === 0)).toBe(true)
  })

  it('uses estimate points for target and engaged progress', () => {
    const today = startOfDay(new Date())
    const start = addDays(today, -14)
    const target = addDays(today, 7)
    const result = buildProgressData([
      issue(addDays(today, -10), 'started', 0, 3),
      issue(addDays(today, -8), 'completed', 1, 5),
    ], format(start, 'yyyy-MM-dd'), format(target, 'yyyy-MM-dd'))

    expect(result.totalEstimate).toBe(8)
    expect(result.series[3].data.every(point => point.y === 8)).toBe(true)
    expect(result.forecast.completed).toBe(5)
    expect(result.series[1].data.at(-1)?.y).toBe(8)
  })

  it('hides the graph until a started project has aligned non-empty history', () => {
    expect(shouldShowProgressGraph({})).toBe(false)
    expect(shouldShowProgressGraph({ startDate: format(startOfDay(new Date()), 'yyyy-MM-dd'), scopeHistory: [], completedScopeHistory: [] })).toBe(false)
    expect(shouldShowProgressGraph({ startDate: format(startOfDay(new Date()), 'yyyy-MM-dd'), scopeHistory: [{ date: new Date().toISOString(), value: 2 }], completedScopeHistory: [] })).toBe(false)
    expect(shouldShowProgressGraph({ startDate: format(startOfDay(new Date()), 'yyyy-MM-dd'), scopeHistory: [{ date: new Date().toISOString(), value: 2 }], completedScopeHistory: [{ date: new Date().toISOString(), value: 0 }] })).toBe(true)
    expect(shouldShowProgressGraph({ startDate: format(startOfDay(new Date()), 'yyyy-MM-dd'), scopeHistory: [{ date: new Date().toISOString(), value: 2 }], completedScopeHistory: [{ date: new Date().toISOString(), value: 0 }], status: { id: 'planned', name: 'Planned', color: '#888', type: 'planned' } })).toBe(false)
  })
})

describe('project detail dependency relations', () => {
  it('renders directional relations from projectRelations', () => {
    const data = makeBootstrap()
    const current = { ...project, id: 'project-current', slugId: 'current', name: 'Current project', dependencyIds: [], createdAt: '2026-08-01T00:00:00.000Z', updatedAt: '2026-08-01T00:00:00.000Z' }
    const blockedBy = { ...project, id: 'project-blocked-by', slugId: 'blocked-by', name: 'Blocked by project', dependencyIds: [], createdAt: '2026-08-01T00:00:00.000Z', updatedAt: '2026-08-01T00:00:00.000Z' }
    const blocking = { ...project, id: 'project-blocking', slugId: 'blocking', name: 'Blocking project', dependencyIds: [], createdAt: '2026-08-01T00:00:00.000Z', updatedAt: '2026-08-01T00:00:00.000Z' }
    render(<I18nProvider><ProjectDetailsSidebar
      initiatives={[]}
      integrationConnections={[]}
      labelGroups={[]}
      labels={[]}
      onConvertMilestone={vi.fn()}
      onCreateMilestone={vi.fn()}
      onDeleteMilestone={vi.fn()}
      onMoveMilestone={vi.fn()}
      onOpenIssueFilter={vi.fn()}
      onOpenMilestoneIssues={vi.fn()}
      onReorderMilestones={vi.fn()}
      onTabChange={vi.fn()}
      onUpdate={vi.fn().mockResolvedValue(undefined)}
      onUpdateProject={vi.fn().mockResolvedValue(current)}
      onUpdateMilestone={vi.fn()}
      project={current}
      projectIssues={[]}
      projectRelations={[
        { id: 'relation-blocked-by', projectId: current.id, relatedProjectId: blockedBy.id, type: 'blocked_by', createdAt: '', updatedAt: '' },
        { id: 'relation-blocking', projectId: blocking.id, relatedProjectId: current.id, type: 'blocked_by', createdAt: '', updatedAt: '' },
      ]}
      projects={[current, blockedBy, blocking]}
      projectStatuses={[current.status]}
      projectUpdates={[]}
      teams={data.teams}
      users={data.users}
      viewer={data.viewer}
    /></I18nProvider>)

    expect(screen.getByRole('link', { name: 'Blocked by project' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Blocking project' })).toBeVisible()
  })

  it('uses directional payloads when removing a relation', async () => {
    const user = userEvent.setup()
    const data = makeBootstrap()
    const current = { ...project, id: 'project-current', slugId: 'current', name: 'Current project', dependencyIds: [], createdAt: '2026-08-01T00:00:00.000Z', updatedAt: '2026-08-01T00:00:00.000Z' }
    const blockedBy = { ...project, id: 'project-blocked-by', slugId: 'blocked-by', name: 'Blocked by project', dependencyIds: [], createdAt: '2026-08-01T00:00:00.000Z', updatedAt: '2026-08-01T00:00:00.000Z' }
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    render(<I18nProvider><ProjectDetailsSidebar
      initiatives={[]}
      integrationConnections={[]}
      labelGroups={[]}
      labels={[]}
      onConvertMilestone={vi.fn()}
      onCreateMilestone={vi.fn()}
      onDeleteMilestone={vi.fn()}
      onMoveMilestone={vi.fn()}
      onOpenIssueFilter={vi.fn()}
      onOpenMilestoneIssues={vi.fn()}
      onReorderMilestones={vi.fn()}
      onTabChange={vi.fn()}
      onUpdate={onUpdate}
      onUpdateProject={vi.fn().mockResolvedValue(current)}
      onUpdateMilestone={vi.fn()}
      project={current}
      projectIssues={[]}
      projectRelations={[{ id: 'relation-blocked-by', projectId: current.id, relatedProjectId: blockedBy.id, type: 'blocked_by', createdAt: '', updatedAt: '' }]}
      projects={[current, blockedBy]}
      projectStatuses={[current.status]}
      projectUpdates={[]}
      teams={data.teams}
      users={data.users}
      viewer={data.viewer}
    /></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Menu' }))
    await user.click(screen.getByRole('menuitem', { name: 'Remove dependency' }))
    expect(onUpdate).toHaveBeenCalledWith({ dependencyRelations: [] })
  })

  it('updates the owning project when a relation is stored in the inverse direction', async () => {
    const user = userEvent.setup()
    const data = makeBootstrap()
    const current = { ...project, id: 'project-current', slugId: 'current', name: 'Current project', dependencyIds: [], createdAt: '2026-08-01T00:00:00.000Z', updatedAt: '2026-08-01T00:00:00.000Z' }
    const owner = { ...project, id: 'project-owner', slugId: 'owner', name: 'Owner project', dependencyIds: [], createdAt: '2026-08-01T00:00:00.000Z', updatedAt: '2026-08-01T00:00:00.000Z' }
    const onUpdateProject = vi.fn().mockResolvedValue(current)
    render(<I18nProvider><ProjectDetailsSidebar
      initiatives={[]}
      integrationConnections={[]}
      labelGroups={[]}
      labels={[]}
      onConvertMilestone={vi.fn()}
      onCreateMilestone={vi.fn()}
      onDeleteMilestone={vi.fn()}
      onMoveMilestone={vi.fn()}
      onOpenIssueFilter={vi.fn()}
      onOpenMilestoneIssues={vi.fn()}
      onReorderMilestones={vi.fn()}
      onTabChange={vi.fn()}
      onUpdate={vi.fn().mockResolvedValue(undefined)}
      onUpdateProject={onUpdateProject}
      onUpdateMilestone={vi.fn()}
      project={current}
      projectIssues={[]}
      projectRelations={[{ id: 'relation-inverse', projectId: owner.id, relatedProjectId: current.id, type: 'blocks', createdAt: '', updatedAt: '' }]}
      projects={[current, owner]}
      projectStatuses={[current.status]}
      projectUpdates={[]}
      teams={data.teams}
      users={data.users}
      viewer={data.viewer}
    /></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Menu' }))
    await user.click(screen.getByRole('menuitem', { name: 'Remove dependency' }))
    expect(onUpdateProject).toHaveBeenCalledWith(owner.id, { dependencyRelations: [] })
  })
})

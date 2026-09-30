import { addDays, format, startOfDay } from 'date-fns'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { buildProgressData, shouldShowProgressGraph } from './project-progress-data'
import { ProjectDetailsSidebar } from './project-details-sidebar'
import type { Initiative, Issue } from '@/types/flow'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project } from '@/test/fixtures'
import type { ProjectPickerRequest } from './project-detail-shortcuts'

async function findTooltip(text: string) {
  let match: HTMLElement | undefined
  await waitFor(() => {
    match = screen.queryAllByRole('tooltip').find(tooltip => tooltip.textContent?.includes(text))
    expect(match).toBeDefined()
  }, { timeout: 2000 })
  return match!
}

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
    // With no local issues the target follows the latest persisted scope, and the axis fits every series.
    expect(result.series[3].data.every(point => point.y === 6)).toBe(true)
    expect(result.yMax).toBe(12)
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

describe('project sidebar activity', () => {
  it('shows the creation event with a project glyph and no synthetic property changes', () => {
    const data = makeBootstrap()
    const current = { ...project, id: 'project-activity', priority: 2, priorityLabel: 'High', lead: data.viewer, createdAt: '2026-09-27T00:00:00.000Z', updatedAt: '2026-09-27T00:00:00.000Z' }
    const { container } = render(<I18nProvider><ProjectDetailsSidebar
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
      projectRelations={[]}
      projects={[current]}
      projectStatuses={[current.status]}
      projectUpdates={[]}
      teams={data.teams}
      users={data.users}
      viewer={data.viewer}
    /></I18nProvider>)

    const activity = container.querySelector('.project-details-sidebar__activity')
    expect(activity?.children).toHaveLength(1)
    expect(activity).toHaveTextContent('created the project')
    expect(activity).not.toHaveTextContent('changed priority')
    expect(activity).not.toHaveTextContent('assigned themselves')
    expect(activity?.querySelector('.project-details-sidebar__activity-glyph')).toBeInTheDocument()
    expect(activity?.querySelector('.avatar')).not.toBeInTheDocument()
  })
})

describe('project sidebar Linear parity', () => {
  function renderSidebar(overrides: Partial<typeof project> = {}, options: { tab?: 'overview' | 'activity'; projectIssues?: Issue[]; initiatives?: Initiative[]; otherProjects?: (typeof project)[]; pickerRequest?: ProjectPickerRequest; onPickerRequestHandled?: () => void } = {}) {
    const data = makeBootstrap()
    const current = { ...project, id: 'project-parity', lead: undefined, memberIds: [], startDate: undefined, targetDate: undefined, slackChannelName: undefined, initiatives: [], milestones: [{ id: 'milestone-alpha', name: 'Alpha', sortOrder: 0 }], createdAt: '2026-09-27T00:00:00.000Z', updatedAt: '2026-09-27T00:00:00.000Z', ...overrides } as typeof project
    return render(<I18nProvider><ProjectDetailsSidebar
      initiatives={options.initiatives ?? []}
      integrationConnections={[]}
      labelGroups={[]}
      labels={[]}
      onConvertMilestone={vi.fn()}
      onCreateMilestone={vi.fn()}
      onDeleteMilestone={vi.fn()}
      onMoveMilestone={vi.fn()}
      onOpenIssueFilter={vi.fn()}
      onOpenMilestoneIssues={vi.fn()}
      onPickerRequestHandled={options.onPickerRequestHandled}
      pickerRequest={options.pickerRequest}
      onReorderMilestones={vi.fn()}
      onTabChange={vi.fn()}
      onUpdate={vi.fn().mockResolvedValue(undefined)}
      onUpdateProject={vi.fn().mockResolvedValue(current)}
      onUpdateMilestone={vi.fn()}
      project={current}
      projectIssues={options.projectIssues ?? []}
      projectRelations={[]}
      projects={[current, ...(options.otherProjects ?? [])]}
      projectStatuses={[current.status]}
      projectUpdates={[]}
      tab={options.tab}
      teams={data.teams}
      users={data.users}
      viewer={data.viewer}
    /></I18nProvider>)
  }

  it('dims empty property placeholders and uses Linear copy', () => {
    const { container } = renderSidebar()
    expect(screen.getByText('Add lead').closest('button')).toHaveClass('is-empty')
    expect(screen.getByText('Add members').closest('button')).toHaveClass('is-empty')
    expect(screen.getByText('Slack channel').closest('button')).toHaveClass('is-empty')
    expect(screen.getByText('Target').closest('button')).toHaveClass('is-empty')
    expect(container.querySelector('.project-details-sidebar__property-dates svg.lucide-arrow-right')).toBeInTheDocument()
    expect(screen.queryByText('Initiatives')).toBeNull()
  })

  it('hides Progress without issues and omits empty milestone chrome', () => {
    const { container } = renderSidebar()
    expect(screen.queryByText('Progress')).toBeNull()
    expect(screen.getByText('Activity')).toBeInTheDocument()
    const alpha = screen.getByRole('button', { name: 'Alpha 0% of 0' })
    expect(alpha).not.toHaveTextContent('No date')
    expect(container.querySelector('.project-details-sidebar__milestone.is-unassigned')?.textContent).toBe('No milestone')
  })

  it('shows Progress once the project has issues', () => {
    renderSidebar({}, { projectIssues: [issue(new Date(), 'started')] })
    expect(screen.getByText('Progress')).toBeInTheDocument()
  })

  it('shows only Properties and Milestones on the Activity tab', () => {
    renderSidebar({}, { tab: 'activity', projectIssues: [issue(new Date(), 'started')] })
    expect(screen.getByText('Properties')).toBeInTheDocument()
    expect(screen.getByText('Milestones')).toBeInTheDocument()
    expect(screen.queryByText('Progress')).toBeNull()
    expect(screen.queryByText('Activity')).toBeNull()
  })

  it('hides the Initiatives row when the project has none, even if the workspace has initiatives', () => {
    const initiative = { id: 'initiative-1', name: 'Launch', status: 'active' } as Initiative
    renderSidebar({}, { initiatives: [initiative] })
    expect(screen.queryByText('Initiatives')).toBeNull()
  })

  it('shows the Initiatives row once the project belongs to an initiative', () => {
    const initiative = { id: 'initiative-1', name: 'Launch', status: 'active' } as Initiative
    renderSidebar({ initiatives: ['initiative-1'] }, { initiatives: [initiative] })
    expect(screen.getByText('Initiatives')).toBeInTheDocument()
    expect(screen.getByText('Launch')).toBeInTheDocument()
  })

  it('disables Add dependency when there is no other active project', async () => {
    const archived = { ...project, id: 'project-archived', name: 'Archived', archivedAt: '2026-09-01T00:00:00.000Z' } as typeof project
    renderSidebar({}, { otherProjects: [archived] })
    const add = screen.getByRole('button', { name: 'Add dependency' })
    expect(add).toHaveAttribute('aria-disabled', 'true')
    expect(add).toHaveAttribute('data-disabled')
    await userEvent.click(add)
    expect(screen.queryByText('Blocked by…')).toBeNull()
  })

  it('opens the dependency menu when another project exists', async () => {
    const other = { ...project, id: 'project-other', name: 'Other', archivedAt: undefined } as typeof project
    renderSidebar({}, { otherProjects: [other] })
    const add = screen.getByRole('button', { name: 'Add dependency' })
    expect(add).not.toHaveAttribute('aria-disabled')
    await userEvent.click(add)
    expect(await screen.findByText('Blocked by…')).toBeInTheDocument()
    expect(screen.getByText('Blocking…')).toBeInTheDocument()
  })

  it('marks the Milestones card so its Linear header geometry applies', () => {
    renderSidebar()
    expect(screen.getByText('Milestones').closest('section')).toHaveClass('project-details-sidebar__section', 'is-milestones')
    expect(screen.getByText('Properties').closest('section')).not.toHaveClass('is-milestones')
  })

  it('shows Linear tooltips with shortcuts on the property controls', async () => {
    const user = userEvent.setup()
    renderSidebar()
    await user.hover(screen.getByRole('combobox', { name: /Change Status/ }))
    const status = await findTooltip('Change project status')
    expect(status.textContent).toBe('Change project statusPthenS')
    await user.keyboard('{Escape}')
    await user.hover(screen.getByRole('button', { name: 'Change project target date' }))
    expect((await findTooltip('Add target date')).textContent).toMatch(/D$/)
    await user.keyboard('{Escape}')
    await user.hover(screen.getByRole('button', { name: 'Slack channel' }))
    expect(await findTooltip('Slack channel')).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await user.hover(screen.getByRole('button', { name: 'Add milestone' }))
    expect(await findTooltip('Create new project milestone')).toBeInTheDocument()
  }, 10000)

  it('explains why Add dependency is disabled', async () => {
    const user = userEvent.setup()
    renderSidebar()
    await user.hover(screen.getByRole('button', { name: 'Add dependency' }))
    expect(await findTooltip('All projects are related to this project')).toBeInTheDocument()
  })

  it('opens the requested property picker from a keyboard shortcut', async () => {
    const onPickerRequestHandled = vi.fn()
    const { unmount } = renderSidebar({}, { pickerRequest: { kind: 'status', id: 1 }, onPickerRequestHandled })
    expect(await screen.findByRole('dialog', { name: 'Change Status' })).toBeInTheDocument()
    expect(onPickerRequestHandled).toHaveBeenCalledTimes(1)
    unmount()
    renderSidebar({}, { pickerRequest: { kind: 'targetDate', id: 2 } })
    expect(await screen.findByRole('button', { name: 'Change project target date' })).toHaveAttribute('aria-expanded', 'true')
  })
})

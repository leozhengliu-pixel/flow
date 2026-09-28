import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { ProjectDetailsSidebar } from './project-details-sidebar'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project } from '@/test/fixtures'

function renderSidebar(overrides: Partial<typeof project> & Record<string, unknown>) {
  const data = makeBootstrap()
  const current = { ...project, id: 'project-creator', createdAt: '2026-09-27T00:00:00.000Z', updatedAt: '2026-09-27T00:00:00.000Z', ...overrides } as typeof project
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
  return { activity: container.querySelector('.project-details-sidebar__activity'), data }
}

describe('project sidebar creation entry', () => {
  it('credits the recorded creator rather than the lead', () => {
    const data = makeBootstrap()
    const creator = { ...data.viewer, id: 'user-creator', name: 'Skyler Anderson', displayName: 'Skyler Anderson' }
    const { activity } = renderSidebar({ lead: data.viewer, creatorId: creator.id, creator })
    expect(activity).toHaveTextContent('Skyler Anderson created the project')
  })

  it('falls back to the lead for projects created before the creator was recorded', () => {
    const data = makeBootstrap()
    const lead = { ...data.viewer, id: 'user-lead', name: 'Lead Person', displayName: 'Lead Person' }
    const { activity } = renderSidebar({ lead, creatorId: undefined, creator: undefined })
    expect(activity).toHaveTextContent('Lead Person created the project')
  })
})

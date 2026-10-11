import { describe, expect, it } from 'vitest'

import type { ProjectTemplate } from '@/types/flow'
import { projectTemplateChoice, projectTemplateChoices, projectTemplateDependencyRelations } from './project-template-choice'

const template: ProjectTemplate = {
  id: 'template-1',
  name: 'Launch template',
  projectName: 'Product launch',
  summary: 'Ship it',
  description: 'Launch brief',
  icon: 'Rocket',
  color: '#24b4c7',
  statusId: 'status-started',
  priority: 2,
  teamIds: ['team-1'],
  labelIds: ['label-project', 'label-issue'],
  leadId: 'user-lead',
  memberIds: ['user-a', 'user-b'],
  dependencyIds: ['project-blocker'],
  dependencyRelations: [
    { projectId: 'project-blocker', type: 'blocked_by' },
    { projectId: 'project-blocked', type: 'blocks' },
    { projectId: 'project-deleted', type: 'blocks' },
  ],
  initiativeIds: ['initiative-1'],
  milestones: [{ id: 'milestone-beta', name: 'Beta', description: 'Beta exit' }, { id: 'milestone-ga', name: 'GA' }],
  issues: [{ id: 'issue-1', title: 'Plan', priority: 0, labelIds: [] }],
  creator: { id: 'user-lead', name: 'Lead', displayName: 'Lead', email: 'lead@example.com' } as ProjectTemplate['creator'],
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
}

const context = {
  projectStatuses: [{ id: 'status-started', name: 'In Progress' }],
  projectLabels: [{ id: 'label-project' }],
  projects: [{ id: 'project-blocker' }, { id: 'project-blocked' }],
}

describe('projectTemplateChoice', () => {
  it('maps every preset the new-project dialog applies', () => {
    expect(projectTemplateChoice(template, context)).toEqual({
      id: 'template-1',
      label: 'Launch template',
      name: 'Product launch',
      description: 'Launch brief',
      summary: 'Ship it',
      icon: 'Rocket',
      color: '#24b4c7',
      status: 'In Progress',
      priority: 'High',
      leadId: 'user-lead',
      memberIds: ['user-a', 'user-b'],
      teamIds: ['team-1'],
      initiativeIds: ['initiative-1'],
      labelIds: ['label-project'],
      dependencyIds: ['project-blocker'],
      dependencyRelations: [{ projectId: 'project-blocker', type: 'blocked_by' }, { projectId: 'project-blocked', type: 'blocks' }],
      milestones: [{ templateMilestoneId: 'milestone-beta', name: 'Beta', description: 'Beta exit' }, { templateMilestoneId: 'milestone-ga', name: 'GA', description: undefined }],
    })
  })

  it('falls back to the template name and the legacy blocked-by list', () => {
    const legacy = { ...template, projectName: '  ', dependencyRelations: undefined, statusId: undefined, leadId: undefined, memberIds: undefined, milestones: undefined }
    const [choice] = projectTemplateChoices([legacy], { ...context, projects: undefined })
    expect(choice.name).toBe('Launch template')
    expect(choice.status).toBeUndefined()
    expect(choice.leadId).toBeUndefined()
    expect(choice.memberIds).toEqual([])
    expect(choice.milestones).toEqual([])
    expect(choice.dependencyRelations).toEqual([{ projectId: 'project-blocker', type: 'blocked_by' }])
    expect(projectTemplateDependencyRelations({ dependencyIds: undefined, dependencyRelations: [] })).toEqual([])
  })

  it('keeps the template dependencies while the project list has not loaded', () => {
    const choice = projectTemplateChoice(template, { ...context, projects: [] })
    expect(choice.dependencyRelations).toEqual(projectTemplateDependencyRelations(template))
  })
})

import type { BootstrapData, Issue, IssueLabel, Project, User, WorkflowState } from '@/types/flow'

export const viewer = {
  id: 'user-1', name: 'viewer', displayName: 'Viewer', email: 'viewer@example.test', active: true,
} as User

export const teammate = {
  id: 'user-2', name: 'teammate', displayName: 'Teammate', email: 'teammate@example.test', active: true,
} as User

export const backlog = {
  id: 'state-backlog', name: 'Backlog', color: '#777777', type: 'backlog', position: 0,
} as WorkflowState

export const started = {
  id: 'state-started', name: 'In progress', color: '#f0c000', type: 'started', position: 1,
} as WorkflowState

export const completed = {
  id: 'state-completed', name: 'Done', color: '#5e6ad2', type: 'completed', position: 2,
} as WorkflowState

export const label = {
  id: 'label-1', name: 'Feature', color: '#5e6ad2', resourceType: 'issue', groupId: 'group-1',
} as IssueLabel

export const project = {
  id: 'project-1', slugId: 'project-one', name: 'Project one', summary: 'Summary', description: 'Description',
  icon: 'Project', color: '#5e6ad2', priority: 2, priorityLabel: 'High', health: 'onTrack',
  status: { id: 'project-status-1', name: 'In progress', color: '#5e6ad2', type: 'started' },
  lead: viewer, memberIds: [viewer.id], teamIds: ['team-1'], labelIds: [], initiatives: [], dependencyIds: [],
  resources: [], milestones: [], customers: [], issueCount: 0,
} as unknown as Project

export function makeIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: 'issue-1', identifier: 'TST-1', number: 1, title: 'Test issue', description: 'Issue body',
    priority: 2, priorityLabel: 'High', sortOrder: 1, createdAt: '2026-08-01T00:00:00.000Z',
    updatedAt: '2026-08-02T00:00:00.000Z', team: { id: 'team-1', key: 'TST', name: 'Test team', color: '#5e6ad2' },
    state: started, creator: teammate, assignee: viewer, labels: [label], subscriberIds: [viewer.id],
    subIssueIds: [], relations: [], attachments: [],
    ...overrides,
  } as Issue
}

export function makeBootstrap(overrides: Partial<BootstrapData> = {}): BootstrapData {
  const issue = makeIssue()
  return {
    workspace: { id: 'workspace-1', name: 'Workspace', urlKey: 'workspace', color: '#5e6ad2', region: 'us' },
    viewer, users: [viewer, teammate], teams: [issue.team], states: [backlog, started, completed],
    labels: [label], labelGroups: [{ id: 'group-1', name: 'Type', color: '#5e6ad2', resourceType: 'issue' }],
    issues: [issue], projects: [project], projectStatuses: [project.status], projectUpdates: {}, initiatives: [],
    initiativeUpdates: {}, cycles: [], issueTemplates: [], releases: [], releasePipelines: [], subscriptions: [], activities: {},
    ...overrides,
  } as unknown as BootstrapData
}

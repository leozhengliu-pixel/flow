import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { Initiative, Project, ProjectUpdate, User } from '@/types/flow'

import { classifyInboxHost } from './inbox-host-types'
import { InitiativeUpdatesInboxView } from './initiative-updates-inbox-view'
import { PriorityInboxSettings } from './priority-inbox-settings'
import {
  defaultPriorityInboxRuleState,
  notificationTypeMatchesPriorityRules,
} from './priority-inbox-settings-metadata'
import { ProjectOverviewInboxView } from './project-overview-inbox-view'
import { ProjectUpdatesInboxView } from './project-updates-inbox-view'

const viewer = {
  id: 'user-1',
  name: 'Ada Lovelace',
  displayName: 'Ada Lovelace',
  email: 'ada@flow.test',
  active: true,
  emailVerified: true,
} as User

const project = {
  id: 'project-1',
  name: 'Launch Flow',
  slugId: 'launch',
  summary: 'Ship the inbox hosts',
  description: 'Inbox depth for updates and overviews.',
  color: '#5e6ad2',
  priority: 2,
  priorityLabel: 'High',
  progress: 0.4,
  health: 'atRisk',
  status: { id: 'status-1', name: 'In progress', color: '#f59e0b', type: 'started' },
  memberIds: [],
  labelIds: [],
  teamIds: [],
  dependencyIds: [],
  initiatives: [],
  customers: [],
  resources: [],
  milestones: [],
  comments: [],
  descriptionRevisions: [],
  updateCadence: 'weekly',
  issueCount: 3,
  createdAt: '2026-09-01T00:00:00.000Z',
  updatedAt: '2026-09-20T00:00:00.000Z',
} as Project

const update = {
  id: 'update-1',
  projectId: project.id,
  body: 'Design review landed; implementing hosts next.',
  health: 'onTrack',
  createdAt: '2026-09-21T12:00:00.000Z',
  user: viewer,
  comments: [],
  reactions: {},
  attachments: [],
} as ProjectUpdate

const initiative = {
  id: 'initiative-1',
  name: 'Inbox parity',
  slugId: 'inbox-parity',
  summary: 'Close Linear inbox gaps',
  description: '',
  color: '#d6a526',
  status: 'active',
  priority: 1,
  priorityLabel: 'Urgent',
  health: 'onTrack',
  creator: viewer,
  contributingTeamIds: [],
  labelIds: [],
  projectIds: [project.id],
  resources: [],
  comments: [],
  favorite: false,
  subscribed: true,
  notificationRules: { descriptionChanges: true, newUpdate: true, allProjectUpdates: false },
  updateSchedule: { cadence: 'none', weekday: 1, timeRange: '09:00-12:00' },
  descriptionHistory: [],
  createdAt: '2026-09-01T00:00:00.000Z',
  updatedAt: '2026-09-20T00:00:00.000Z',
} as Initiative

describe('classifyInboxHost', () => {
  it('routes project and initiative update/overview notification types', () => {
    expect(classifyInboxHost({ type: 'projectUpdateCreated', projectId: 'p1' })).toBe('project-updates')
    expect(classifyInboxHost({ type: 'initiativeUpdateCreated', sourceType: 'initiative', sourceId: 'i1' })).toBe(
      'initiative-updates',
    )
    expect(classifyInboxHost({ type: 'projectSubscribed', projectId: 'p1' })).toBe('project-overview')
    expect(classifyInboxHost({ type: 'initiativeOverview', sourceType: 'initiative', sourceId: 'i1' })).toBe(
      'initiative-overview',
    )
  })
})

describe('ProjectUpdatesInboxView', () => {
  it('renders update stream chrome and posts a new update', async () => {
    const user = userEvent.setup()
    const onCreateUpdate = vi.fn().mockResolvedValue(undefined)
    render(
      <ProjectUpdatesInboxView
        project={project}
        updates={[update]}
        viewer={viewer}
        onOpenProject={vi.fn()}
        onCreateUpdate={onCreateUpdate}
      />,
    )
    expect(screen.getByText('Launch Flow')).toBeInTheDocument()
    expect(screen.getByText(/Design review landed/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'New update' }))
    await user.type(screen.getByLabelText('Project update'), 'Hosts are shipping')
    await user.click(screen.getByRole('button', { name: 'Post update' }))
    expect(onCreateUpdate).toHaveBeenCalledWith({ body: 'Hosts are shipping', health: 'onTrack' })
  })
})

describe('ProjectOverviewInboxView', () => {
  it('shows overview properties without honesty banners', () => {
    render(
      <ProjectOverviewInboxView project={project} latestUpdate={update} onOpenProject={vi.fn()} />,
    )
    expect(screen.getByText('Project overview')).toBeInTheDocument()
    expect(screen.getByText('In progress')).toBeInTheDocument()
    expect(screen.queryByText(/not implemented|coming soon|gap/i)).not.toBeInTheDocument()
  })
})

describe('InitiativeUpdatesInboxView', () => {
  it('opens the write-update composer when there are no updates yet', () => {
    render(
      <InitiativeUpdatesInboxView
        initiative={initiative}
        updates={[]}
        viewer={viewer}
        onOpenInitiative={vi.fn()}
        onCreateUpdate={vi.fn()}
      />,
    )
    expect(screen.getByText('Inbox parity')).toBeInTheDocument()
    expect(screen.getByLabelText('Initiative update')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Post update' })).toBeDisabled()
  })
})

describe('priority inbox settings metadata', () => {
  it('matches assignment notifications when assigned-to-you is enabled', () => {
    const rules = defaultPriorityInboxRuleState()
    expect(notificationTypeMatchesPriorityRules('assignment', 'assignment', rules)).toBe(true)
    rules['assigned-to-you'] = false
    expect(notificationTypeMatchesPriorityRules('assignment', 'assignment', rules)).toBe(false)
  })

  it('renders priority rule toggles', async () => {
    const user = userEvent.setup()
    render(<PriorityInboxSettings priorityInboxEnabled />)
    expect(screen.getByText('Assigned to you')).toBeInTheDocument()
    const toggle = screen.getByRole('switch', { name: /Disable Assigned to you/i })
    await user.click(toggle)
    expect(screen.getByRole('switch', { name: /Enable Assigned to you/i })).toBeInTheDocument()
  })
})

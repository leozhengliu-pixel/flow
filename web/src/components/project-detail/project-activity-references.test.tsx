import type { ComponentProps } from 'react'
import { render, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { makeBootstrap, makeIssue, project, viewer } from '@/test/fixtures'
import { MentionShell } from '@/test/mention-host-harness'
import type { ProjectUpdate } from '@/types/flow'
import { ProjectActivity } from './project-activity'

const apiMocks = vi.hoisted(() => ({
  listProjectHistory: vi.fn(async () => ({ nodes: [] as unknown[], nextCursor: '', total: 0 })),
  fetchIssueRecord: vi.fn(),
  listProjectRecords: vi.fn(),
  listIssueRecords: vi.fn(async () => ({ items: [], hasMore: false, total: 0 })),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...apiMocks }))
vi.mock('@/lib/route-pages', () => ({ AgentChatPanel: () => null }))

function activityProps() {
  const data = makeBootstrap()
  return {
    activities: [],
    documents: data.documents,
    drafts: [],
    initiatives: [],
    integrationConnections: [],
    issues: [],
    labelGroups: [],
    labels: [],
    onCommentProject: async () => ({}) as never,
    onCommentProjectUpdate: async () => ({}) as never,
    onConvertMilestone: async () => project,
    onCreateMilestone: async () => ({}) as never,
    onCreateReminder: async () => ({}) as never,
    onCreateResource: async () => ({}) as never,
    onCreateSavedView: async () => ({}) as never,
    onCreateUpdate: async () => ({}) as never,
    onDelete: async () => undefined,
    onDeleteIssues: async () => undefined,
    onDeleteMilestone: async () => undefined,
    onDeleteProjectUpdateAttachment: async () => ({}) as never,
    onDeleteResource: async () => undefined,
    onDeleteSavedView: async () => undefined,
    onDeleteUpdate: async () => undefined,
    onMoveMilestone: async () => undefined,
    onOpenIssue: () => undefined,
    onReorderMilestones: async () => [],
    onReactProjectUpdate: async () => ({}) as never,
    onSetSubscriptionEvents: async () => undefined,
    onTabChange: () => undefined,
    onToggleFavorite: async () => undefined,
    onUpdate: async () => project,
    onUpdateIssue: async () => ({}) as never,
    onUpdateMilestone: async () => ({}) as never,
    onUpdateProjectUpdate: async () => ({}) as never,
    onUpdateResource: async () => ({}) as never,
    onUpdateSavedView: async () => ({}) as never,
    onUploadProjectUpdateAttachment: async () => ({}) as never,
    onCreateIssue: () => undefined,
    onDeleteProjectUpdate: async () => undefined,
    onOpenMilestoneIssues: () => undefined,
    onOpenSavedView: () => undefined,
    onEditSavedView: () => undefined,
    onCreateProjectUpdate: async () => ({}) as never,
    onUpdateProject: async () => project,
    project,
    projectRelations: [],
    projectStatuses: [project.status],
    projectUpdates: [],
    projects: [project],
    savedViews: [],
    tab: 'activity' as const,
    teams: data.teams,
    users: data.users,
    viewer,
  } as unknown as ComponentProps<typeof ProjectActivity>
}


const event = (id: string, type: string, metadata: Record<string, string>) => ({ id, type, createdAt: '2026-09-27T12:00:00.000Z', actor: viewer, metadata })

beforeEach(() => {
  apiMocks.fetchIssueRecord.mockReset()
  resetAgentRecordCache()
})

describe('ProjectActivity references', () => {
  it('renders an update body with references as chips', async () => {
    const data = mentionFixture()
    const update = { id: 'update-1', projectId: project.id, body: `Shipped [TST-1](${mentionUrls.issue}) for [Project one](${mentionUrls.project}) cc @viewer`, health: 'onTrack', createdAt: '2026-09-27T12:00:00.000Z', user: viewer, comments: [], reactions: {} } as unknown as ProjectUpdate
    const { container } = render(<MentionShell data={data}><ProjectActivity {...activityProps()} projectUpdates={[update]}/></MentionShell>)
    const body = container.querySelector('.project-activity__update .project-activity__rich') as HTMLElement
    await waitFor(() => expect(body.querySelector('a[data-agent-entity="issue"]')).toHaveTextContent('TST-1 Test issue'))
    expect(body.querySelector('a[data-agent-entity="project"]')).toHaveAttribute('href', mentionUrls.project)
    expect(body.querySelector('a[data-agent-entity="user"]')).toHaveTextContent('@Viewer')
    expect(body.textContent).not.toContain('](')
  })

  it('renders the resource a history event names as a chip, fetching an issue the client does not hold', async () => {
    apiMocks.fetchIssueRecord.mockResolvedValue(makeIssue({ id: '0a1b2c3d-1111-2222-3333-444455556666', identifier: 'TST-9', title: 'Remote issue' }))
    const data = mentionFixture({ issueCollectionPaged: true, issues: [] })
    const events = [
      event('a1', 'project.initiative_added', { initiativeId: 'initiative-1', initiativeName: 'Roadmap' }),
      event('a2', 'project.milestone_created', { milestoneId: 'milestone-1', name: 'Alpha' }),
      event('a3', 'project.issue_added', { issueId: '0a1b2c3d-1111-2222-3333-444455556666' }),
      event('a4', 'project.document_added', { documentId: 'deleted-doc', title: 'Old plan' }),
    ]
    const { container } = render(<MentionShell data={data}><ProjectActivity {...activityProps()} activities={events as never}/></MentionShell>)
    const feed = container.querySelector('.project-activity__feed') as HTMLElement
    await waitFor(() => expect(feed.querySelector('a[data-agent-entity="initiative"]')).toHaveAttribute('href', mentionUrls.initiative))
    expect(feed.querySelector('a[data-agent-entity="milestone"]')).toHaveTextContent('Alpha')
    await waitFor(() => expect(feed.querySelector('a[data-agent-entity="issue"]')).toHaveTextContent('TST-9 Remote issue'))
    expect(feed.querySelector('[data-agent-entity="document"]')).toHaveAttribute('data-mention-state', 'missing')
    expect(feed.querySelector('[data-agent-entity="document"]')).toHaveTextContent('Old plan')
    expect(feed.textContent).toMatch(/Viewer project initiative_added\s+Roadmap/)
  })
})

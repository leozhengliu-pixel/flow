import { makeBootstrap, project as baseProject, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, Project } from '@/types/flow'

const milestoneProject = { ...baseProject, milestones: [{ id: 'milestone-1', projectId: 'project-1', name: 'Alpha', description: '', targetDate: '2026-09-30', createdAt: '', updatedAt: '' }] } as unknown as Project

/** Workspace data holding one resource of every kind a mention can name. */
export function mentionFixture(overrides: Partial<BootstrapData> = {}) {
  return makeBootstrap({
    users: [{ ...viewer, username: 'viewer' }, teammate],
    projects: [milestoneProject],
    initiatives: [{ id: 'initiative-1', slugId: 'roadmap', name: 'Roadmap', summary: 'Ship it', description: '', color: '#f2c94c', icon: 'Initiative', status: 'active', priority: 2, priorityLabel: 'High', health: 'onTrack', owner: viewer, creator: viewer, projectIds: [], contributingTeamIds: [], labelIds: [], resources: [], comments: [], favorite: false, subscribed: false, createdAt: '', updatedAt: '' }] as never,
    documents: [{ id: 'document-1', slugId: 'plan-abc', title: 'Launch plan', color: '#8b8b90', content: '', creator: viewer, projectIds: ['project-1'], teamIds: [], subscriberIds: [], favorite: false, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-29T00:00:00Z', revisions: [] }] as never,
    cycles: [{ id: 'cycle-1', number: 3, name: 'Cycle 3', description: '', teamId: 'team-1', startsAt: '2026-09-28T00:00:00Z', endsAt: '2026-10-04T00:00:00Z', status: 'current', capacity: 0, favorite: false, resources: [], createdAt: '', updatedAt: '' }] as never,
    customers: [{ id: 'customer-1234567890ab', name: 'Acme Corp', status: 'active', tier: 'Gold', domains: [], createdAt: '', updatedAt: '' }] as never,
    savedViews: [{ id: 'view-1', slugId: 'urgent-view', name: 'Urgent view', description: 'All urgent work', scope: 'workspace', ownerId: 'user-2', view: 'all', filters: [], display: {}, createdAt: '', updatedAt: '2026-09-27T00:00:00Z' }] as never,
    releasePipelines: [{ id: 'pipeline-1', slugId: 'web', name: 'Web', teamIds: [], type: 'scheduled', production: true, stages: [], stageStatuses: {}, position: 0, pathFilters: [], autoGenerateReleaseNotes: false, createdAt: '', updatedAt: '' }] as never,
    releases: [{ id: 'release-1', slugId: 'v1', name: 'Version one', version: '1.0', description: '', status: 'planned', pipelineId: 'pipeline-1', position: 0, projectIds: [], issueIds: [], subscriberIds: [], resources: [], creator: viewer, createdAt: '', updatedAt: '' }] as never,
    reviews: [{ id: 'review-1', slugId: 'fix-login', provider: 'github', externalId: '1', number: 7, title: 'Fix login', description: '', status: 'inReview', repositoryOwner: 'acme', repositoryName: 'web', url: 'https://github.com/acme/web/pull/7', author: teammate, reviewerIds: [], teamReviewers: [], issueIds: [], baseBranch: 'main', headBranch: 'fix', branchState: 'upToDate', additions: 0, deletions: 0, commitCount: 1, checks: [], files: [], events: [], favorite: false, draft: false, quickToReview: false, createdAt: '', updatedAt: '' }] as never,
    teamMembers: [{ teamId: 'team-1', userId: 'user-1', role: 'member', joinedAt: '' }] as never,
    ...overrides,
  })
}

/** In-app URLs for the fixture's resources, keyed by mention kind. */
export const mentionUrls = {
  issue: '/workspace/issue/TST-1/test-issue',
  project: '/workspace/project/project-one/overview',
  initiative: '/workspace/initiative/roadmap/overview',
  document: '/workspace/document/plan-abc',
  team: '/workspace/team/TST/overview',
  cycle: '/workspace/team/TST/cycle/3',
  label: '/workspace/issue-label/Feature',
  milestone: '/workspace/project/project-one/overview#milestone-milestone-1',
  customer: '/workspace/customer/acme-corp-1234567890ab',
  release: '/workspace/pipeline/web/release/v1/issues',
  view: '/workspace/view/urgent-view',
  review: '/workspace/review/fix-login',
  user: '/workspace/profiles/viewer',
}

/** What each mention kind is labelled in the fixture. */
export const mentionLabels: Record<keyof typeof mentionUrls, string> = {
  issue: 'TST-1', project: 'Project one', initiative: 'Roadmap', document: 'Launch plan', team: 'Test team', cycle: 'Cycle 3', label: 'Feature', milestone: 'Alpha', customer: 'Acme Corp', release: 'Version one', view: 'Urgent view', review: 'Fix login', user: 'Viewer',
}

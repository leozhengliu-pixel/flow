import { describe, expect, it } from 'vitest'

import { applyRealtimePatch } from '@/store/apply-realtime-patch'
import { completed, makeBootstrap, makeIssue, project } from '@/test/fixtures'
import type { BootstrapData, Notification, Release } from '@/types/flow'

import { archiveProjectUpdateReminders, mergeRefreshedIssues, mergeWorkspaceMetadata, metadataOnlyRealtimeEvent, newlyReleasedIssueIds, syncIssueProjectSummaries } from './workspace-metadata-refresh'

describe('metadata-only realtime events', () => {
  it('covers project, release, document and other metadata edits', () => {
    for (const type of ['project.milestone_deleted', 'project.update_created', 'project.commented', 'release.updated', 'release_pipeline.created', 'document.updated', 'cycle.updated', 'initiative.update_created', 'view.created', 'customer.updated', 'team.resource_pinned']) {
      expect(metadataOnlyRealtimeEvent({ type }), type).toBe(true)
    }
  })

  it('leaves events that can change issues or visibility on the full refresh', () => {
    for (const type of ['issue.updated', 'comment.created', 'document.comment_created', 'cycle.completed', 'cycle.started', 'project.deleted', 'label.updated', 'label.deleted', 'team.updated', 'workspace_member.updated', 'ask.decided', 'schedules.maintained', 'resync']) {
      expect(metadataOnlyRealtimeEvent({ type }), type).toBe(false)
    }
  })
})

describe('mergeWorkspaceMetadata', () => {
  const milestone = { id: 'milestone-1', name: 'Beta', projectId: project.id }
  const issue = makeIssue({ project: { id: project.id, name: project.name, icon: project.icon, color: project.color }, projectMilestoneId: milestone.id })
  const current = makeBootstrap({
    issues: [issue],
    projects: [{ ...project, milestones: [milestone] } as BootstrapData['projects'][number]],
    comments: { [issue.id]: [{ id: 'comment-1' } as never] },
    notifications: [{ id: 'notification-1' } as Notification],
  })

  it('keeps loaded issue records and refreshes their project references', () => {
    const next = makeBootstrap({ issues: [], projects: [{ ...project, name: 'Renamed', icon: 'Rocket', milestones: [] } as BootstrapData['projects'][number]], comments: {}, notifications: [], issueCollectionPaged: true })
    const merged = mergeWorkspaceMetadata(current, next)
    expect(merged.issues).toHaveLength(1)
    expect(merged.issues[0].project).toMatchObject({ id: project.id, name: 'Renamed', icon: 'Rocket' })
    expect(merged.issues[0].projectMilestoneId).toBeUndefined()
    expect(merged.comments).toBe(current.comments)
    expect(merged.notifications).toBe(current.notifications)
    expect(merged.issueCollectionPaged).toBeFalsy()
    expect(merged.projects[0].name).toBe('Renamed')
  })

  it('names the issues of releases that just became released', () => {
    const release = { id: 'release-1', status: 'inProgress', issueIds: ['issue-1', 'issue-2'] } as Release
    expect(newlyReleasedIssueIds({ releases: [release] }, { releases: [{ ...release, status: 'released' }] })).toEqual(['issue-1', 'issue-2'])
    expect(newlyReleasedIssueIds({ releases: [{ ...release, status: 'released' }] }, { releases: [{ ...release, status: 'released' }] })).toEqual([])
  })

  it('replaces refreshed issues without dropping unloaded ones', () => {
    const other = makeIssue({ id: 'issue-2', identifier: 'TST-2' })
    const data = makeBootstrap({ issues: [issue, other] })
    const refreshed = mergeRefreshedIssues(data, [{ ...issue, state: completed, version: 2, description: undefined as never, isSummary: true }])
    expect(refreshed.issues.map(item => item.id)).toEqual(['issue-1', 'issue-2'])
    expect(refreshed.issues[0].state.type).toBe('completed')
    expect(refreshed.issues[0].description).toBe(issue.description)
  })
})

describe('project realtime helpers', () => {
  it('archives pending update reminders for the project', () => {
    const reminder = { id: 'n1', projectId: project.id, type: 'projectUpdateReminder', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z' } as Notification
    const unrelated = { id: 'n2', projectId: 'other', type: 'projectUpdateReminder' } as Notification
    const data = archiveProjectUpdateReminders(makeBootstrap({ notifications: [reminder, unrelated] }), project.id, '2026-02-01T00:00:00Z')
    expect(data.notifications[0]).toMatchObject({ archivedAt: '2026-02-01T00:00:00Z', readAt: '2026-02-01T00:00:00Z' })
    expect(data.notifications[1].archivedAt).toBeUndefined()
  })

  it('updates issue project summaries when a project icon changes', () => {
    const issue = makeIssue({ project: { id: project.id, name: project.name, icon: 'Project', color: project.color } })
    expect(syncIssueProjectSummaries([issue], { ...project, icon: 'Rocket' })[0].project?.icon).toBe('Rocket')
    const unchanged = [issue]
    expect(syncIssueProjectSummaries(unchanged, project)).toBe(unchanged)
    const patched = applyRealtimePatch(makeBootstrap({ issues: [issue] }), { id: 'e1', type: 'project.updated', aggregateId: project.id, createdAt: '', payload: { entity: { ...project, icon: 'Rocket' } } } as never)
    expect(patched.handled && patched.data.issues[0].project?.icon).toBe('Rocket')
  })
})

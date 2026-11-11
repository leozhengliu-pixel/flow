import { describe, expect, it } from 'vitest'

import { applyRealtimePatch } from '@/store/apply-realtime-patch'
import { completed, label, makeBootstrap, makeIssue, project, started } from '@/test/fixtures'
import type { BootstrapData, IssueSLA, IssueUpdateInput, Notification, Release } from '@/types/flow'

import { applyLabelUpdate, archiveProjectUpdateReminders, eventRequiresSLARefresh, issueMutationAffectsSLA, mergeRefreshedIssues, mergeScopedIssues, mergeWorkspaceMetadata, metadataOnlyRealtimeEvent, newlyReleasedIssueIds, syncIssueProjectSummaries, syncIssueReferences, teamIssueScope } from './workspace-metadata-refresh'

describe('SLA metadata refresh', () => {
  it('refreshes every rule-matching and manual deadline input, including clears', () => {
    for (const input of [{ priority: 0 }, { teamId: 'team' }, { stateId: 'done' }, { labelIds: [] }, { projectId: '' }, { slaBreachesAt: '' }, { slaType: 'onlyBusinessDays' }] satisfies IssueUpdateInput[]) {
      expect(issueMutationAffectsSLA(input), JSON.stringify(input)).toBe(true)
    }
    for (const input of [{ title: 'Rename' }, { description: 'Edit' }, { assigneeId: '' }, { expectedVersion: 2 }, { priority: undefined }, {}]) {
      expect(issueMutationAffectsSLA(input), JSON.stringify(input)).toBe(false)
    }
  })

  it('refreshes self-contained issue events for enabled or retained SLA metadata', () => {
    const enabled = makeBootstrap({ settings: { sla: { enabled: true } } })
    for (const type of ['issue.created', 'issue.updated', 'issue.batch_updated', 'issue.deleted', 'issue.restored']) expect(eventRequiresSLARefresh({ type }, enabled), type).toBe(true)
    for (const type of ['comment.created', 'favorite.created', 'attachment.created']) expect(eventRequiresSLARefresh({ type }, enabled), type).toBe(false)
    const disabled = makeBootstrap({ settings: { sla: { enabled: false } }, issueSlas: [] })
    expect(eventRequiresSLARefresh({ type: 'issue.updated' }, disabled)).toBe(false)
    expect(eventRequiresSLARefresh({ type: 'issue.updated' }, { ...disabled, issueSlas: [{ id: 'retained' } as IssueSLA] })).toBe(true)
  })

  it('replaces stale SLA instances independently of retained issue rows', () => {
    const oldSla = { id: 'sla', issueId: 'issue-1', ruleId: 'rule', dueAt: '2026-11-10T12:00:00Z', status: 'active' } as IssueSLA
    const current = makeBootstrap({ issues: [makeIssue({ labels: [] })], issueSlas: [oldSla] })
    const updatedSla = { ...oldSla, dueAt: '2026-11-12T12:00:00Z' }
    const next = makeBootstrap({ issues: [], issueSlas: [updatedSla] })
    const merged = mergeWorkspaceMetadata(current, next)
    expect(merged.issueSlas).toEqual([updatedSla])
    expect(merged.issues).toEqual(current.issues)
    expect(merged.issues[0]).toBe(current.issues[0])
    expect(mergeWorkspaceMetadata(merged, { ...next, issueSlas: [] }).issueSlas).toEqual([])
  })
})

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

describe('targeted refresh helpers', () => {
  const team = makeIssue().team
  const other = { id: 'team-2', key: 'OPS', name: 'Ops', color: '#000' } as BootstrapData['teams'][number]

  it('syncs team, state and label copies and drops issues of teams that disappeared', () => {
    const kept = makeIssue()
    const hidden = makeIssue({ id: 'issue-2', identifier: 'OPS-1', team: other })
    const current = makeBootstrap({ teams: [team, other], issues: [kept, hidden] })
    const next = makeBootstrap({
      teams: [{ ...team, name: 'Renamed', key: 'REN' }],
      states: [{ ...started, name: 'Doing' }],
      labels: [],
      issues: [],
    })
    const [issue, ...rest] = syncIssueReferences(current.issues, current, { ...next, labels: [{ ...label, id: 'other' }] })
    expect(rest).toHaveLength(0)
    expect(issue.team).toMatchObject({ name: 'Renamed', key: 'REN' })
    expect(issue.state.name).toBe('Doing')
    expect(issue.labels).toEqual([])
    const unchanged = syncIssueReferences(current.issues, current, current)
    expect(unchanged).toBe(current.issues)
  })

  it('updates a label copy on every issue that carries it', () => {
    const data = makeBootstrap()
    const updated = applyLabelUpdate(data, { ...label, name: 'Renamed', archivedAt: '2026-09-01T00:00:00.000Z' })
    expect(updated.labels[0]).toMatchObject({ name: 'Renamed', archivedAt: '2026-09-01T00:00:00.000Z' })
    expect(updated.issues[0].labels[0]).toMatchObject({ name: 'Renamed' })
  })

  it('adds, replaces and removes the issues a scoped query owns', () => {
    const inTeam = makeIssue({ id: 'a', version: 1 })
    const gone = makeIssue({ id: 'b' })
    const elsewhere = makeIssue({ id: 'c', team: other })
    const data = makeBootstrap({ issues: [inTeam, gone, elsewhere] })
    const scope = teamIssueScope(team.id)
    expect(scope.filter).toEqual({ field: 'team', operator: 'in', values: [team.id] })
    const merged = mergeScopedIssues(data, [{ ...inTeam, title: 'Fresh', version: 2 }, makeIssue({ id: 'new' })], scope.owns)
    expect(merged.issues.map(issue => issue.id)).toEqual(['a', 'c', 'new'])
    expect(merged.issues[0].title).toBe('Fresh')
    expect(mergeScopedIssues(data, [])).toBe(data)
  })
})

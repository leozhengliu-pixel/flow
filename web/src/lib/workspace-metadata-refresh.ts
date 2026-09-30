import { deriveResourceCounts } from '@/lib/resource-counts'
import type { BootstrapData, Issue, IssueLabel, Project, RealtimeEvent } from '@/types/flow'

// Server mutations for these events never create, delete or rewrite issue,
// comment or activity records: they only change workspace metadata (projects,
// milestones, project and initiative updates, documents, releases, cycles,
// views, customers, templates, ...). A client holding the full issue
// collection can refresh the small metadata projection instead of downloading
// every issue again. Release completion automations can still move a
// release's issues; newlyReleasedIssueIds names them so callers can refetch
// just those.
const METADATA_EVENT = new RegExp('^(?:' + [
  'project\\.(?:updated|milestone_(?:created|updated|deleted)|milestones_reordered|resource_(?:created|updated|deleted)|commented|comment_(?:updated|resolved|unresolved|deleted|reaction_toggled|thread_subscription|attachment_created)|update_(?:created|updated|deleted|commented|reaction_toggled|attachment_created|attachment_deleted)|relation_(?:created|updated|deleted))',
  'initiative\\.(?:created|updated|deleted|commented|comment_(?:updated|deleted|reaction_toggled)|update_(?:created|updated|deleted|commented|reaction_toggled|attachment_created|attachment_deleted)|relation_(?:created|updated|deleted)|resource_(?:created|updated|deleted))',
  'cycle\\.(?:updated|capacity_updated|resource_(?:created|deleted)|calendar_token_requested)',
  'document\\.(?:created|updated|deleted|revision_restored|permissions_updated|permission_(?:updated|deleted)|draft_(?:created|updated|deleted|published))',
  'document_template\\.[a-z_]+', 'issue_template\\.[a-z_]+', 'project_status\\.[a-z_]+', 'project_template\\.deleted', 'project_update_settings\\.updated',
  'release\\.(?:created|updated|deleted|reordered|ci_event|note_(?:created|updated|deleted)|timestamps_updated)', 'release_pipeline\\.[a-z_]+',
  'customer\\.[a-z_]+', 'customer_taxonomy\\.[a-z_]+', 'customer_need\\.archive_toggled', 'customer_request\\.(?:created|updated|deleted|attachment_(?:created|deleted))',
  'view\\.[a-z_]+', 'label\\.created', 'label_group\\.created', 'issue_label\\.created',
  'team\\.resource_(?:pinned|updated|deleted|section_(?:created|updated|deleted))',
].join('|') + ')$')

export function metadataOnlyRealtimeEvent(event: Pick<RealtimeEvent, 'type'>): boolean {
  return METADATA_EVENT.test(event.type)
}

/**
 * Merge a metadata-only bootstrap (`/api/issue-records/bootstrap`) into a
 * snapshot that holds the full issue collection. Issue, comment, activity and
 * inbox records are kept; issue display references (project summary,
 * milestone, label copies and counts) are refreshed from the new metadata the
 * same way the server derives them for a full bootstrap.
 */
export function mergeWorkspaceMetadata(current: BootstrapData, next: BootstrapData): BootstrapData {
  const projects = new Map(next.projects.map(project => [project.id, project]))
  const issues = syncIssueReferences(current.issues, current, next).map(issue => {
    if (!issue.project) return issue
    const project = projects.get(issue.project.id)
    if (!project) return issue
    const milestoneRemoved = Boolean(issue.projectMilestoneId && project.milestones && !project.milestones.some(milestone => milestone.id === issue.projectMilestoneId))
    const summary = issue.project
    if (!milestoneRemoved && summary.name === project.name && summary.icon === project.icon && summary.color === project.color) return issue
    return { ...issue, project: { ...summary, name: project.name, icon: project.icon, color: project.color }, projectMilestoneId: milestoneRemoved ? undefined : issue.projectMilestoneId }
  })
  const data: BootstrapData = {
    ...next,
    issues,
    comments: current.comments,
    activities: current.activities,
    notifications: current.notifications,
    notificationDeliveries: current.notificationDeliveries,
    issueHistoryCursors: current.issueHistoryCursors,
    issueCollectionPaged: current.issueCollectionPaged,
    issueCollectionRevision: current.issueCollectionRevision,
    // Development workspaces synthesize members only in the full bootstrap.
    members: next.members?.length ? next.members : current.members,
    teamMembers: next.teamMembers?.length ? next.teamMembers : current.teamMembers,
  }
  return deriveResourceCounts(data)
}

/** Issues of releases that became released, which completion automations may have moved. */
export function newlyReleasedIssueIds(current: Pick<BootstrapData, 'releases'>, next: Pick<BootstrapData, 'releases'>): string[] {
  const previousStatus = new Map((current.releases ?? []).map(release => [release.id, release.status]))
  return [...new Set((next.releases ?? [])
    .filter(release => release.status === 'released' && previousStatus.get(release.id) !== 'released')
    .flatMap(release => release.issueIds ?? []))]
}

/** Replace loaded issues with refreshed copies, keeping detail fields a list projection omits. */
export function mergeRefreshedIssues(current: BootstrapData, refreshed: Issue[]): BootstrapData {
  if (!refreshed.length) return current
  const incoming = new Map(refreshed.map(issue => [issue.id, issue]))
  const issues = current.issues.map(previous => {
    const issue = incoming.get(previous.id)
    if (!issue || (previous.version ?? 0) > (issue.version ?? 0)) return previous
    if (issue.isSummary && !previous.isSummary) {
      return { ...previous, ...issue, isSummary: false, description: previous.description, descriptionState: previous.descriptionState, documentContent: previous.documentContent, reactions: previous.reactions, subscriberIds: previous.subscriberIds }
    }
    return issue
  })
  return deriveResourceCounts({ ...current, issues })
}

/** Mirror the server archiving pending update reminders when an update is posted. */
export function archiveProjectUpdateReminders(current: BootstrapData, projectId: string, now = new Date().toISOString()): BootstrapData {
  let changed = false
  const notifications = current.notifications.map(notification => {
    if (notification.projectId !== projectId || !notification.type.startsWith('projectUpdate') || notification.archivedAt) return notification
    changed = true
    return { ...notification, archivedAt: now, readAt: now, updatedAt: now }
  })
  return changed ? { ...current, notifications } : current
}

/** Keep issues' embedded project summary in step with an edited project. */
export function syncIssueProjectSummaries(issues: Issue[], project: Pick<Project, 'id' | 'name' | 'icon' | 'color'>): Issue[] {
  let changed = false
  const next = issues.map(issue => {
    const summary = issue.project
    if (summary?.id !== project.id || (summary.name === project.name && summary.icon === project.icon && summary.color === project.color)) return issue
    changed = true
    return { ...issue, project: { ...summary, name: project.name, icon: project.icon, color: project.color } }
  })
  return changed ? next : issues
}

/**
 * Keep issues' embedded team, workflow state and label copies in step with
 * refreshed metadata: renamed or recoloured records are copied in, deleted
 * labels are dropped, and issues of a team that disappeared from the
 * metadata (deleted, or a private team the viewer left) are removed.
 */
export function syncIssueReferences(issues: Issue[], current: Pick<BootstrapData, 'teams'>, next: Pick<BootstrapData, 'teams' | 'states' | 'labels'>): Issue[] {
  const teams = new Map(next.teams.map(team => [team.id, team]))
  const states = new Map(next.states.map(state => [state.id, state]))
  const labels = new Map(next.labels.map(label => [label.id, label]))
  const removedTeams = new Set(next.teams.length ? current.teams.filter(team => !teams.has(team.id)).map(team => team.id) : [])
  let changed = false
  const synced: Issue[] = []
  for (const issue of issues) {
    if (removedTeams.has(issue.team.id)) { changed = true; continue }
    const team = teams.get(issue.team.id)
    const state = states.get(issue.state.id)
    const teamChanged = Boolean(team && (team.name !== issue.team.name || team.key !== issue.team.key || team.icon !== issue.team.icon || team.color !== issue.team.color))
    const stateChanged = Boolean(state && (state.name !== issue.state.name || state.color !== issue.state.color || state.type !== issue.state.type))
    let labelsChanged = false
    const issueLabels = next.labels.length ? issue.labels.flatMap(label => {
      const fresh = labels.get(label.id)
      if (!fresh) { labelsChanged = true; return [] }
      if (fresh.name !== label.name || fresh.color !== label.color || fresh.archivedAt !== label.archivedAt) { labelsChanged = true; return [{ ...label, name: fresh.name, color: fresh.color, archivedAt: fresh.archivedAt }] }
      return [label]
    }) : issue.labels
    if (!teamChanged && !stateChanged && !labelsChanged) { synced.push(issue); continue }
    changed = true
    synced.push({ ...issue, team: teamChanged ? { ...issue.team, ...team } : issue.team, state: stateChanged ? { ...issue.state, ...state } : issue.state, labels: labelsChanged ? issueLabels : issue.labels })
  }
  return changed ? synced : issues
}

/** Replace one label's copy on every issue that carries it. */
export function applyLabelUpdate(current: BootstrapData, label: IssueLabel): BootstrapData {
  return {
    ...current,
    labels: current.labels.map(item => item.id === label.id ? { ...item, ...label } : item),
    issues: current.issues.map(issue => issue.labels.some(item => item.id === label.id)
      ? { ...issue, labels: issue.labels.map(item => item.id === label.id ? { ...item, name: label.name, color: label.color, archivedAt: label.archivedAt } : item) }
      : issue),
  }
}

/**
 * Merge the complete result of an issue query into a full issue collection:
 * returned issues are added or replaced, and loaded issues the query owns
 * (`owns`) that it no longer returns are removed.
 */
export function mergeScopedIssues(current: BootstrapData, refreshed: Issue[], owns?: (issue: Issue) => boolean): BootstrapData {
  const returned = new Set(refreshed.map(issue => issue.id))
  const kept = owns ? current.issues.filter(issue => returned.has(issue.id) || !owns(issue)) : current.issues
  const base = kept.length === current.issues.length ? current : { ...current, issues: kept }
  const merged = mergeRefreshedIssues(base, refreshed)
  const known = new Set(merged.issues.map(issue => issue.id))
  const added = refreshed.filter(issue => !known.has(issue.id))
  if (added.length) return deriveResourceCounts({ ...merged, issues: [...merged.issues, ...added] })
  return merged === base && base !== current ? deriveResourceCounts(base) : merged
}

/** Issues an action may have rewritten: a record query plus the loaded issues it owns. */
export type IssueRefreshScope = { filter: Record<string, unknown>; owns?: (issue: Issue) => boolean }

export function teamIssueScope(teamId: string): IssueRefreshScope {
  return { filter: { field: 'team', operator: 'in', values: [teamId] }, owns: issue => issue.team.id === teamId }
}

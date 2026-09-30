import { deriveResourceCounts } from '@/lib/resource-counts'
import type { BootstrapData, Issue, Project, RealtimeEvent } from '@/types/flow'

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
  const issues = current.issues.map(issue => {
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

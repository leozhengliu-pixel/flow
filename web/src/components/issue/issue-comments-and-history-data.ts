/**
 * LS-0344 IssueCommentsAndHistoryData — hydrateRelations pack for timeline.
 * Pulls PR comments, agent sessions, labels/projects onto history payloads (REST).
 */
import { fetchIssueHistory, type IssueHistoryPage } from '@/lib/api'
import type { BootstrapData, Issue } from '@/types/flow'
import { activityIds } from '@/components/activity/issue-activity-model'

export type IssueHistoryRelation =
  | 'sourceComment'
  | 'sourcePullRequestComment'
  | 'agentSessions'
  | 'labels'
  | 'projects'

export interface IssueCommentsAndHistoryData {
  issueId: string
  comments: IssueHistoryPage['comments']
  activities: IssueHistoryPage['activities']
  commentsCursor?: string
  activitiesCursor?: string
  /** Hydrated relation maps keyed by entity id. */
  relations: {
    labels: BootstrapData['labels']
    projects: BootstrapData['projects']
    agentSessionIds: string[]
    pullRequestCommentIds: string[]
  }
}

export interface HydrateIssueCommentsAndHistoryArgs {
  issue: Pick<Issue, 'id' | 'labels' | 'project' | 'agentSessionId'>
  data: Pick<BootstrapData, 'labels' | 'projects'>
  signal?: AbortSignal
  cursors?: { commentsCursor?: string; activitiesCursor?: string }
  /** Relation keys to hydrate (default: all). */
  hydrateRelations?: IssueHistoryRelation[]
}

export async function hydrateIssueCommentsAndHistory(
  args: HydrateIssueCommentsAndHistoryArgs,
): Promise<IssueCommentsAndHistoryData> {
  const wanted = new Set(
    args.hydrateRelations ?? [
      'sourceComment',
      'sourcePullRequestComment',
      'agentSessions',
      'labels',
      'projects',
    ],
  )
  const page = await fetchIssueHistory(args.issue.id, args.signal, args.cursors)

  const labelIds = new Set((args.issue.labels ?? []).map(label => label.id))
  const projectIds = new Set([args.issue.project?.id].filter(Boolean))
  for (const activity of page.activities) {
    const metadata = activity.metadata ?? {}
    for (const key of ['labels', 'addedLabelIds', 'removedLabelIds']) for (const id of activityIds(metadata[key])) labelIds.add(id)
    for (const key of ['project', 'projectBefore']) if (metadata[key]) projectIds.add(metadata[key])
  }
  const labels = wanted.has('labels')
    ? args.data.labels.filter(label => labelIds.has(label.id))
    : []
  const projects = wanted.has('projects')
    ? args.data.projects.filter(project => projectIds.has(project.id))
    : []

  const agentSessionIds =
    wanted.has('agentSessions') && args.issue.agentSessionId
      ? [args.issue.agentSessionId]
      : []

  const pullRequestCommentIds = wanted.has('sourcePullRequestComment')
    ? page.activities
        .map(activity => {
          const record = activity as { pullRequestCommentId?: string; source?: string }
          return (
            record.pullRequestCommentId ||
            (record.source === 'pull_request' ? activity.id : undefined)
          )
        })
        .filter((id): id is string => Boolean(id))
    : []

  return {
    issueId: args.issue.id,
    comments: page.comments,
    activities: page.activities,
    commentsCursor: page.commentsCursor,
    activitiesCursor: page.activitiesCursor,
    relations: {
      labels,
      projects,
      agentSessionIds,
      pullRequestCommentIds,
    },
  }
}

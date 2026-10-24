import type { BootstrapProjection } from '@/lib/api'
import type { BootstrapData } from '@/types/flow'

/**
 * An issue opened from an already-loaded workspace has its own detail and
 * history endpoints. Replacing the workspace snapshot with the issue-detail
 * projection would discard project detail data needed when the user returns.
 */
export function canReuseWorkspaceForIssueDetail({
  current,
  requestedWorkspaceKey,
  viewerId,
  projection,
}: {
  current?: {
    resourceDetailsOmitted?: BootstrapData['resourceDetailsOmitted']
    workspace: Pick<BootstrapData['workspace'], 'urlKey'>
    viewer: Pick<BootstrapData['viewer'], 'id'>
  } | null
  requestedWorkspaceKey?: string
  viewerId?: string
  projection?: BootstrapProjection
}): boolean {
  return Boolean(
    projection === 'issue-detail' &&
      current &&
      !current.resourceDetailsOmitted &&
      current.workspace.urlKey === requestedWorkspaceKey &&
      current.viewer.id === viewerId,
  )
}

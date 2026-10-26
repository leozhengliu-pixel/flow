import type { BootstrapProjection } from '@/lib/api'
import type { BootstrapData } from '@/types/flow'
import { workspaceBootstrapPhase } from '@/lib/navigation-cache'

/** Identifies one bootstrap request: viewer, workspace and the projection that was asked for ('full' when none). */
export function bootstrapRequestKey(viewerId: string, workspaceKey: string, projection?: BootstrapProjection): string {
  return `${viewerId}:${workspaceKey}:${projection ?? 'full'}`
}

type CurrentWorkspace = {
  resourceDetailsOmitted?: BootstrapData['resourceDetailsOmitted']
  workspace: Pick<BootstrapData['workspace'], 'urlKey'>
  viewer: Pick<BootstrapData['viewer'], 'id'>
}

/**
 * An issue opened from an already-loaded workspace has its own detail and
 * history endpoints. Replacing the workspace snapshot with the issue-detail
 * projection would discard project detail data needed when the user returns.
 *
 * Only a snapshot that a completed *full* bootstrap produced qualifies:
 * `loadedRequestKey` is the key of the last bootstrap that finished. A snapshot
 * hydrated from the navigation cache (no finished request yet) is unverified, and
 * the project-list projection empties states, projects and templates the issue
 * page needs, so neither may stand in for the issue-detail bootstrap.
 */
export function canReuseWorkspaceForIssueDetail({
  current,
  requestedWorkspaceKey,
  viewerId,
  projection,
  loadedRequestKey,
}: {
  current?: CurrentWorkspace | null
  requestedWorkspaceKey?: string
  viewerId?: string
  projection?: BootstrapProjection
  loadedRequestKey?: string
}): boolean {
  return Boolean(
    projection === 'issue-detail' &&
      current &&
      requestedWorkspaceKey &&
      viewerId &&
      !current.resourceDetailsOmitted &&
      current.workspace.urlKey === requestedWorkspaceKey &&
      current.viewer.id === viewerId &&
      loadedRequestKey === bootstrapRequestKey(viewerId, requestedWorkspaceKey),
  )
}

/**
 * What the workspace bootstrap effect does for the current route:
 * - 'reuse': keep the loaded full snapshot (the issue page reads its own record); the loaded
 *   request key is left alone, so returning to the page the snapshot was loaded for is a 'skip'.
 * - 'skip': the snapshot already satisfies this request.
 * - 'fetch': load (or share the in-flight load of) the requested projection.
 */
export function bootstrapNavigationDecision({
  current,
  requestedWorkspaceKey,
  viewerId,
  projection,
  loadedRequestKey,
  inFlightKey,
}: {
  current?: CurrentWorkspace | null
  requestedWorkspaceKey: string
  viewerId: string
  projection?: BootstrapProjection
  loadedRequestKey?: string
  inFlightKey?: string | null
}): 'reuse' | 'skip' | 'fetch' {
  if (canReuseWorkspaceForIssueDetail({ current, requestedWorkspaceKey, viewerId, projection, loadedRequestKey })) return 'reuse'
  const requestKey = bootstrapRequestKey(viewerId, requestedWorkspaceKey, projection)
  return workspaceBootstrapPhase(current?.workspace.urlKey, requestedWorkspaceKey, inFlightKey, requestKey) === 'skip' && loadedRequestKey === requestKey
    ? 'skip'
    : 'fetch'
}

/**
 * The projection a realtime resync should fetch. While an issue is open on top of a reused full
 * snapshot, refetching the issue-detail projection would overwrite the project and document
 * details the snapshot holds, and the page the user returns to would never reload them.
 */
export function bootstrapRefreshProjection({
  routeProjection,
  loadedRequestKey,
  viewerId,
  workspaceKey,
}: {
  routeProjection?: BootstrapProjection
  loadedRequestKey?: string
  viewerId: string
  workspaceKey: string
}): BootstrapProjection | undefined {
  return routeProjection === 'issue-detail' && loadedRequestKey === bootstrapRequestKey(viewerId, workspaceKey) ? undefined : routeProjection
}

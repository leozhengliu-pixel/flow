import type { BootstrapProjection } from '@/lib/api'
import type { AppRoute } from '@/lib/app-routes'
import type { BootstrapData } from '@/types/flow'
import { workspaceBootstrapPhase } from '@/lib/navigation-cache'

export function planningBootstrapProjection(route: AppRoute): BootstrapProjection | undefined {
  if (import.meta.env.VITE_PAGED_ISSUES === 'true') return undefined
  if (route.kind === 'initiative') return 'initiative-detail'
  if (route.kind === 'project' || route.kind === 'project-saved-view') {
    return `project-detail:${encodeURIComponent(route.projectSlugId)}`
  }
  return undefined
}

/** Identifies one bootstrap request: viewer, workspace and the projection that was asked for ('full' when none). */
export function bootstrapRequestKey(viewerId: string, workspaceKey: string, projection?: BootstrapProjection): string {
  return `${viewerId}:${workspaceKey}:${projection ?? 'full'}`
}

type CurrentWorkspace = {
  resourceDetailsOmitted?: BootstrapData['resourceDetailsOmitted']
  workspace: Pick<BootstrapData['workspace'], 'urlKey'>
  viewer: Pick<BootstrapData['viewer'], 'id'>
}

function isPlanningProjection(projection?: string): projection is 'initiative-detail' | `project-detail:${string}` {
  return projection === 'initiative-detail' || /^project-detail:[^:]+$/.test(projection ?? '')
}

function completedWorkspaceScope(viewerId: string, workspaceKey: string, loadedRequestKey?: string) {
  const prefix = `${viewerId}:${workspaceKey}:`
  if (!loadedRequestKey?.startsWith(prefix)) return undefined
  const scope = loadedRequestKey.slice(prefix.length)
  return scope === 'full' || isPlanningProjection(scope) ? scope : undefined
}

export function loadedPlanningDirectory(viewerId?: string, workspaceKey?: string, loadedRequestKey?: string): boolean {
  return Boolean(viewerId && workspaceKey && isPlanningProjection(completedWorkspaceScope(viewerId, workspaceKey, loadedRequestKey)))
}

/**
 * Issue detail and history load separately. Retaining a confirmed workspace
 * directory keeps planning data available for Back. A full directory also
 * satisfies planning routes without another bootstrap.
 */
export function canReuseWorkspaceBootstrap({
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
  if (!current || !requestedWorkspaceKey || !viewerId || current.resourceDetailsOmitted ||
      current.workspace.urlKey !== requestedWorkspaceKey || current.viewer.id !== viewerId) return false
  const scope = completedWorkspaceScope(viewerId, requestedWorkspaceKey, loadedRequestKey)
  return projection === 'issue-detail' ? scope !== undefined : isPlanningProjection(projection) && scope === 'full'
}

/**
 * What the workspace bootstrap effect does for the current route:
 * - 'reuse': keep a confirmed workspace directory and its loaded request key.
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
  if (canReuseWorkspaceBootstrap({ current, requestedWorkspaceKey, viewerId, projection, loadedRequestKey })) return 'reuse'
  const requestKey = bootstrapRequestKey(viewerId, requestedWorkspaceKey, projection)
  return workspaceBootstrapPhase(current?.workspace.urlKey, requestedWorkspaceKey, inFlightKey, requestKey) === 'skip' && loadedRequestKey === requestKey
    ? 'skip'
    : 'fetch'
}

/**
 * A realtime resync preserves the scope of a reused directory, so returning
 * to the page that loaded it still has the data it needs.
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
  const scope = completedWorkspaceScope(viewerId, workspaceKey, loadedRequestKey)
  if (scope === 'full' && (routeProjection === 'issue-detail' || isPlanningProjection(routeProjection))) return undefined
  if (routeProjection === 'issue-detail' && isPlanningProjection(scope)) return scope
  return routeProjection
}

import { parseAppRoute, workspaceRootPath, type AppRoute } from './app-routes'
import type { BootstrapData } from '@/types/flow'

type LocationContext = { pathname: string; search: string; hash?: string; state: unknown }
const maxAncestors = 12

export function workspacePath(value: unknown, workspace: string): string | undefined {
  if (typeof value !== 'string' || value.length > 8192 || !value.startsWith(`${workspaceRootPath(workspace)}/`)) return
  try {
    const url = new URL(value, 'https://flow.invalid')
    const route = parseAppRoute(url.pathname, url.search)
    if (url.origin !== 'https://flow.invalid' || !('workspaceSlug' in route) || route.workspaceSlug !== workspace || route.kind === 'not-found') return
    return url.pathname + url.search + url.hash
  } catch { return }
}

export function detailIdentity(route: AppRoute): string | undefined {
  switch (route.kind) {
    case 'issue': return `issue:${route.identifier.toUpperCase()}`
    case 'project': case 'project-saved-view': return `project:${route.projectSlugId}`
    case 'initiative': return `initiative:${route.initiativeSlugId}`
    case 'document': return `document:${route.documentSlugId}`
    case 'customer': return `customer:${route.customerSlugId}`
    case 'review': return `review:${route.reviewSlug}`
    case 'cycle': return `cycle:${route.teamKey}:${route.cycleId}`
    case 'cycle-upcoming': return `cycle:${route.teamKey}:upcoming`
    case 'release': return `release:${route.pipelineSlug}:${route.releaseSlug}`
    case 'release-pipeline': return `pipeline:${route.pipelineSlug}`
    case 'loop-editor': return `loop:${route.loopId ?? route.draftId ?? 'new'}`
    case 'meeting': return `meeting:${route.meetingId}`
    case 'member-profile': return `member:${route.username}`
    case 'agent': return route.chatSlug ? `agent:${route.chatSlug}` : undefined
    case 'dashboards': return route.dashboardId ? `dashboard:${route.dashboardId}` : undefined
    case 'automation-detail': case 'automation-runs': return `automation:${route.automationId}`
    case 'workspace-saved-view': case 'team-saved-view': case 'projects-saved-view': case 'team-projects-saved-view': return `view:${route.viewId}`
    case 'settings': return 'settings'
    default: return
  }
}

function stateObject(state: unknown): Record<string, unknown> {
  return state && typeof state === 'object' && !Array.isArray(state) ? state as Record<string, unknown> : {}
}

export function navigationTrail(state: unknown, workspace: string): string[] {
  const value = stateObject(state)
  const candidates = Array.isArray(value.navigationTrail) ? value.navigationTrail.slice(-maxAncestors) : []
  const trail = candidates.map(path => workspacePath(path, workspace)).filter((path): path is string => Boolean(path))
  const legacy = workspacePath(value.returnTo, workspace)
  if (legacy && trail.at(-1) !== legacy) trail.push(legacy)
  return trail.slice(-maxAncestors)
}

export function navigationReturnPath(state: unknown, workspace: string, fallback: string): string {
  return navigationTrail(state, workspace).at(-1) ?? fallback
}

/** Per-history-entry context: tab changes preserve it; entering a child records
 * its parent; returning to an ancestor removes that ancestor from the trail. */
export function nextNavigationState(current: LocationContext, destination: string, explicit?: unknown, replace = false): unknown {
  if (explicit === null) return null
  const targetUrl = new URL(destination, 'https://flow.invalid')
  const target = parseAppRoute(targetUrl.pathname, targetUrl.search)
  const source = parseAppRoute(current.pathname, current.search)
  if (!('workspaceSlug' in target) || !target.workspaceSlug || !('workspaceSlug' in source) || source.workspaceSlug !== target.workspaceSlug) return explicit
  const workspace = target.workspaceSlug
  const before = stateObject(current.state)
  const after = stateObject(explicit)
  let trail = navigationTrail(current.state, workspace)
  const sourceIdentity = detailIdentity(source), targetIdentity = detailIdentity(target)
  const currentPath = current.pathname + current.search + (current.hash ?? '')
  const targetPath = targetUrl.pathname + targetUrl.search + targetUrl.hash
  const ancestor = trail.lastIndexOf(targetPath)
  const sameDetail = sourceIdentity !== undefined && sourceIdentity === targetIdentity
  const siblingIssue = source.kind === 'issue' && target.kind === 'issue'
  if (ancestor >= 0) trail = trail.slice(0, ancestor)
  else if (sameDetail || siblingIssue || replace) { /* Preserve list origin during tab/canonical transitions. */ }
  else if (targetIdentity) {
    const parent = workspacePath(currentPath, workspace)
    if (parent && parent !== targetPath) trail.push(parent)
  } else trail = []
  const requested = workspacePath(after.returnTo, workspace)
  if (requested && requested !== targetPath && trail.at(-1) !== requested) trail.push(requested)
  trail = trail.slice(-maxAncestors)
  const extras = sameDetail || siblingIssue ? before : {}
  const sequence = Array.isArray(after.issueSequence) ? issueSequenceIDs(after) : issueSequenceIDs(before)
  const sequenceOrigin = Array.isArray(after.issueSequence) ? trail.at(-1) : workspacePath(before.issueSequenceOrigin, workspace)
  const retainSequence = targetIdentity && sequence.length && (target.kind !== 'issue' || sequenceOrigin === trail.at(-1))
  return { ...extras, ...after, issueSequence: retainSequence ? sequence : undefined, issueSequenceOrigin: retainSequence ? sequenceOrigin : undefined, navigationTrail: trail, returnTo: trail.at(-1) }
}

export function issueSequenceIDs(state: unknown): string[] {
  const ids = stateObject(state).issueSequence
  return Array.isArray(ids) ? [...new Set(ids.slice(0, 1000).filter((id): id is string => typeof id === 'string' && id.length <= 191))] : []
}

export function boundedIssueSequence(ids: string[], selected: string): string[] {
  const unique = [...new Set(ids)]
  const index = unique.indexOf(selected)
  if (index < 0) return [selected]
  return unique.slice(Math.max(0, index - 499), Math.max(0, index - 499) + 1000)
}

export function reviewsOriginView(state: unknown, workspace: string): 'created' | 'for-you' {
  const path = navigationTrail(state, workspace).findLast(path => parseAppRoute(new URL(path, 'https://flow.invalid').pathname).kind === 'reviews')
  const route = path ? parseAppRoute(new URL(path, 'https://flow.invalid').pathname) : undefined
  return route?.kind === 'reviews' ? route.view : 'for-you'
}

export function sidebarOriginPath(state: unknown, currentPath: string, workspace: string): string | undefined {
  const current = parseAppRoute(currentPath)
  if (!detailIdentity(current)) return
  return navigationTrail(state, workspace).findLast(path => {
    const route = parseAppRoute(new URL(path, 'https://flow.invalid').pathname)
    return !detailIdentity(route) || ['workspace-saved-view','team-saved-view','projects-saved-view','team-projects-saved-view'].includes(route.kind)
  })
}

export function navigationLabel(path: string, data: BootstrapData): { label: string; entity?: boolean } | undefined {
  const safe = workspacePath(path, data.workspace.urlKey)
  if (!safe) return
  const url = new URL(safe, 'https://flow.invalid'), route = parseAppRoute(url.pathname, url.search)
  let name: string | undefined
  if (route.kind === 'issue') name = data.issues.find(issue => issue.identifier.toUpperCase() === route.identifier.toUpperCase())?.identifier ?? route.identifier
  else if (route.kind === 'project' || route.kind === 'project-saved-view') name = data.projects.find(project => project.slugId === route.projectSlugId || project.id === route.projectSlugId)?.name
  else if (route.kind === 'initiative') name = data.initiatives.find(initiative => initiative.slugId === route.initiativeSlugId)?.name
  else if (route.kind === 'document') name = data.documents.find(document => document.slugId === route.documentSlugId)?.title
  else if (route.kind === 'customer') name = data.customers.find(customer => route.customerSlugId.endsWith(customer.id.slice(-12)))?.name
  else if ('viewId' in route) name = data.savedViews.find(view => view.id === route.viewId || view.slugId === route.viewId)?.name
  if (name) return { label: name, entity: true }
  const labels: Partial<Record<AppRoute['kind'], string>> = {
    documents: 'Documents', 'team-documents': 'Documents', 'team-overview': 'Home',
    projects: 'Projects', 'team-projects': 'Projects', initiatives: 'Initiatives', 'team-initiatives': 'Initiatives',
    'team-issues': 'Issues', 'workspace-issues': 'Issues', 'my-issues': 'My issues',
    inbox: 'Inbox', search: 'Search', reviews: 'Reviews', review: 'Reviews',
    'workspace-customers': 'Customers', 'workspace-views': 'Views', 'team-views': 'Views',
    'team-cycles': 'Cycles', cycle: 'Cycles', 'cycle-upcoming': 'Cycles',
    release: 'Releases', 'release-pipeline': 'Releases', drafts: 'Drafts', agent: 'Agent', loops: 'Loops',
  }
  return labels[route.kind] ? { label: labels[route.kind]! } : undefined
}

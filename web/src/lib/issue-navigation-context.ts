import { myIssuesPath, parseAppRoute, teamHomePath, teamIssuesPath } from './app-routes'
import type { BootstrapData, Issue, Project } from '@/types/flow'
import { findBySlugRef } from './resource-slug'
import { navigationReturnPath, navigationTrail, projectsListOriginPath, workspacePath } from './navigation-context'

/** `team` / `project`: the crumb is drawn with that entity's icon like the reference header.
 * `issue`: an issue page this one was opened from (shown as its identifier, its title in a tooltip).
 * `tooltip`: hover label (the reference titles "Projects" as "All projects"). */
export interface IssueBreadcrumb { label: string; href: string; entity?: boolean; tooltip?: string; team?: { color?: string; icon?: string }; project?: Project; issue?: Pick<Issue, 'identifier' | 'title'> }

/** The list (or other page) the issue was opened from, or undefined when it was opened directly by URL
 * or from another issue: the reference header then shows only "<ID> <title>", no "Team › Issues". */
export function issueListContextPath(state: unknown, workspace: string): string | undefined {
  const value = workspacePath(navigationReturnPath(state, workspace, ''), workspace)
  if (!value) return undefined
  const url = new URL(value, 'https://flow.invalid')
  const route = parseAppRoute(url.pathname, url.search)
  return 'workspaceSlug' in route && route.workspaceSlug === workspace && route.kind !== 'issue' && route.kind !== 'not-found' ? value : undefined
}

/** Where Esc / the last crumb leads: the page the issue was opened from — a list, a project, or the issue
 * (e.g. the parent) it was opened from — else the issue's team list. */
export function issueReturnPath(state: unknown, workspace: string, issue?: Issue) {
  return issueListContextPath(state, workspace) ?? previousIssuePath(state, workspace, issue) ?? (issue ? teamIssuesPath(workspace, issue.team.key) : myIssuesPath(workspace))
}

function previousIssuePath(state: unknown, workspace: string, issue?: Issue) {
  const value = navigationTrail(state, workspace).at(-1)
  if (!value) return
  const route = parseAppRoute(new URL(value, 'https://flow.invalid').pathname)
  return route.kind === 'issue' && route.identifier.toUpperCase() !== issue?.identifier.toUpperCase() ? value : undefined
}

/**
 * The reference header shows the breadcrumb of the page the issue was opened from, then the issue:
 * - from a project (any tab): `Projects › ◆ Project › ID title` when the project itself was opened from a
 *   projects list, else `◆ Project › ID title`; the project crumb links back to the tab the issue came from;
 * - from another issue (a sub-issue opened from its parent): that issue's root crumb, then its identifier;
 * - from a list: the list's crumbs (`Team › Issues`, `My issues`, …).
 * More than two crumbs collapse to the first and the last (`Projects › FLO-29 › FLO-31`).
 * `context` is the navigation trail (oldest first) or, for older callers, just the list path.
 * Without one (opened directly by URL) there are no crumbs before the issue.
 */
export function issueBreadcrumbs(data: BootstrapData, issue: Issue, context?: string | string[]): IssueBreadcrumb[] {
  const workspace = data.workspace.urlKey
  const trail = (Array.isArray(context) ? context : context ? [context] : [])
    .map(path => workspacePath(path, workspace)).filter((path): path is string => Boolean(path))
  const current = issue.identifier.toUpperCase()
  // A trail entry for this very issue (a reload, a canonical slug) is not an ancestor.
  while (trail.length && issueRouteIdentifier(trail.at(-1)!) === current) trail.pop()
  const crumbs = trailCrumbs(data, issue, trail)
  return crumbs.length > 2 ? [crumbs[0], crumbs.at(-1)!] : crumbs
}

function issueRouteIdentifier(path: string) {
  const route = parseAppRoute(new URL(path, 'https://flow.invalid').pathname)
  return route.kind === 'issue' ? route.identifier.toUpperCase() : undefined
}

function trailCrumbs(data: BootstrapData, issue: Issue, trail: string[]): IssueBreadcrumb[] {
  const href = trail.at(-1)
  if (!href) return []
  const workspace = data.workspace.urlKey
  const url = new URL(href, 'https://flow.invalid')
  const route = parseAppRoute(url.pathname, url.search)
  if (route.kind === 'issue') {
    const source = data.issues.find(item => item.identifier.toUpperCase() === route.identifier.toUpperCase())
    const before = trailCrumbs(data, issue, trail.slice(0, -1))
    return [...before, { label: source?.identifier ?? route.identifier.toUpperCase(), href, entity: true, tooltip: source?.title, issue: source }]
  }
  if (route.kind === 'project') {
    const project = findBySlugRef(data.projects, route.projectSlugId)
    if (project) {
      const list = projectsListOriginPath({ navigationTrail: trail.slice(0, -1) }, workspace)
      const crumb: IssueBreadcrumb = { label: project.name, href, entity: true, project }
      return list ? [{ label: 'Projects', href: list, tooltip: 'All projects' }, crumb] : [crumb]
    }
  }
  return listCrumbs(data, issue, href, route)
}

function listCrumbs(data: BootstrapData, issue: Issue, href: string, route: ReturnType<typeof parseAppRoute>): IssueBreadcrumb[] {
  const workspace = data.workspace.urlKey
  const fallback = teamIssuesPath(workspace, issue.team.key)
  if (route.kind === 'team-issues' || route.kind === 'team-board' || route.kind === 'team-triage') {
    const team = data.teams.find(team => team.key.toLowerCase() === route.teamKey.toLowerCase()) ?? issue.team
    return [{ label: team.name, href: teamHomePath(workspace, team.key), entity: true, team: { color: team.color, icon: team.icon } }, { label: route.kind === 'team-triage' ? 'Triage' : 'Issues', href }]
  }
  if (route.kind === 'my-issues') return [{ label: 'My issues', href }]
  if (route.kind === 'inbox') return [{ label: 'Inbox', href }]
  if (route.kind === 'search') return [{ label: 'Search', href }]
  if (route.kind === 'workspace-issues') return [{ label: 'Issues', href }]
  if (route.kind === 'cycle' || route.kind === 'cycle-upcoming' || route.kind === 'team-cycles') {
    const team = data.teams.find(team => team.key.toLowerCase() === route.teamKey.toLowerCase()) ?? issue.team
    const cycle = route.kind === 'cycle' ? data.cycles.find(cycle => cycle.id === route.cycleId || route.cycleId === 'active' && cycle.teamId === team.id && cycle.status === 'current') : undefined
    return [{ label: team.name, href: teamHomePath(workspace, team.key), entity: true, team: { color: team.color, icon: team.icon } }, { label: cycle?.name || 'Cycles', href, entity: Boolean(cycle?.name) }]
  }
  if (route.kind === 'team-archive') return [{ label: 'Archive', href }]
  if (route.kind === 'customer') {
    const customer = data.customers.find(customer => route.customerSlugId.endsWith(customer.id.slice(-12)))
    if (customer) return [{ label: customer.name, href, entity: true }]
  }
  if (route.kind === 'review') {
    const review = data.reviews.find(review => review.slugId === route.reviewSlug)
    if (review) return [{ label: review.title, href, entity: true }]
  }
  if (route.kind === 'member-profile') {
    const user = data.users.find(user => user.name === route.username || user.id === route.username)
    if (user) return [{ label: user.displayName, href, entity: true }]
  }
  if (route.kind === 'release') {
    const release = data.releases.find(release => release.slugId === route.releaseSlug || release.id === route.releaseSlug)
    if (release) return [{ label: release.name, href, entity: true }]
  }
  if (route.kind === 'document') {
    const document = data.documents.find(document => document.slugId === route.documentSlugId)
    if (document) return [{ label: document.title, href, entity: true }]
  }
  if (route.kind === 'workspace-saved-view' || route.kind === 'team-saved-view' || route.kind === 'project-saved-view') {
    const view = data.savedViews.find(view => view.id === route.viewId || view.slugId === route.viewId)
    if (view) return [{ label: view.name, href, entity: true }]
  }
  return [{ label: issue.team.name, href: teamHomePath(workspace, issue.team.key), entity: true }, { label: 'Issues', href: fallback }]
}

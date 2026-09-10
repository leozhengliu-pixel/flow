import { myIssuesPath, parseAppRoute, projectPath, teamHomePath, teamIssuesPath } from './app-routes'
import type { BootstrapData, Issue } from '@/types/flow'
import { navigationReturnPath, workspacePath } from './navigation-context'

export interface IssueBreadcrumb { label: string; href: string; entity?: boolean }

export function issueReturnPath(state: unknown, workspace: string, issue?: Issue) {
  const value = workspacePath(navigationReturnPath(state, workspace, ''), workspace)
  if (value) {
    const url = new URL(value, 'https://flow.invalid')
    const route = parseAppRoute(url.pathname, url.search)
    if ('workspaceSlug' in route && route.workspaceSlug === workspace && route.kind !== 'issue' && route.kind !== 'not-found') return value
  }
  return issue ? teamIssuesPath(workspace, issue.team.key) : myIssuesPath(workspace)
}

export function issueBreadcrumbs(data: BootstrapData, issue: Issue, returnPath?: string): IssueBreadcrumb[] {
  const workspace = data.workspace.urlKey
  const fallback = teamIssuesPath(workspace, issue.team.key)
  const href = returnPath ? issueReturnPath({ returnTo: returnPath }, workspace, issue) : fallback
  const url = new URL(href, 'https://flow.invalid')
  const route = parseAppRoute(url.pathname, url.search)
  if (route.kind === 'team-issues' || route.kind === 'team-board' || route.kind === 'team-triage') {
    const team = data.teams.find(team => team.key.toLowerCase() === route.teamKey.toLowerCase()) ?? issue.team
    return [{ label: team.name, href: teamHomePath(workspace, team.key), entity: true }, { label: route.kind === 'team-triage' ? 'Triage' : 'Issues', href }]
  }
  if (route.kind === 'my-issues') return [{ label: 'My issues', href }]
  if (route.kind === 'inbox') return [{ label: 'Inbox', href }]
  if (route.kind === 'search') return [{ label: 'Search', href }]
  if (route.kind === 'workspace-issues') return [{ label: 'Issues', href }]
  if (route.kind === 'cycle' || route.kind === 'cycle-upcoming' || route.kind === 'team-cycles') {
    const team = data.teams.find(team => team.key.toLowerCase() === route.teamKey.toLowerCase()) ?? issue.team
    const cycle = route.kind === 'cycle' ? data.cycles.find(cycle => cycle.id === route.cycleId || route.cycleId === 'active' && cycle.teamId === team.id && cycle.status === 'current') : undefined
    return [{ label: team.name, href: teamHomePath(workspace, team.key), entity: true }, { label: cycle?.name || 'Cycles', href, entity: Boolean(cycle?.name) }]
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
  const project = route.kind === 'project' || route.kind === 'project-saved-view' ? data.projects.find(project => project.slugId === route.projectSlugId || project.id === route.projectSlugId) : issue.project && data.projects.find(project => project.id === issue.project?.id)
  if (project && (route.kind === 'project' || route.kind === 'project-saved-view')) return [{ label: project.name, href, entity: true }]
  if (!returnPath && project) return [{ label: project.name, href: projectPath(workspace, project, 'issues'), entity: true }]
  return [{ label: issue.team.name, href: teamHomePath(workspace, issue.team.key), entity: true }, { label: 'Issues', href: fallback }]
}

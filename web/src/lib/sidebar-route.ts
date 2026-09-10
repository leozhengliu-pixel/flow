import { type AppRoute, agentPath, customersPath, documentsPath, draftsPath, inboxPath, initiativesPath, loopsPath, membersPath, myIssuesPath, projectsPath, pulsePath, releasePipelinesPath, reviewsPath, teamCyclesPath, teamHomePath, teamInitiativesPath, teamIssuesPath, teamProjectsPath, teamsPath, teamViewsPath, workspaceViewsPath } from './app-routes'

export function sidebarRoutePath(route: AppRoute): string | undefined {
  if (!('workspaceSlug' in route) || !route.workspaceSlug) return
  const w = route.workspaceSlug
  switch (route.kind) {
    case 'inbox': return inboxPath(w)
    case 'reviews': case 'review': return reviewsPath(w)
    case 'my-issues': return myIssuesPath(w)
    case 'pulse': return pulsePath(w)
    case 'drafts': return draftsPath(w)
    case 'agent': return agentPath(w)
    case 'initiatives': case 'initiative': return initiativesPath(w)
    case 'projects': case 'project': case 'project-saved-view': case 'projects-saved-view': case 'projects-new-view': return projectsPath(w)
    case 'documents': case 'document': return documentsPath(w)
    case 'workspace-members': case 'member-profile': return membersPath(w)
    case 'workspace-customers': case 'customer': return customersPath(w)
    case 'workspace-teams': case 'new-team': return teamsPath(w)
    case 'releases': case 'release-pipeline': case 'release': case 'release-note': return releasePipelinesPath(w)
    case 'loops': case 'loop-editor': return loopsPath(w)
    case 'team-overview': case 'team-documents': case 'team-members': case 'team-loops': case 'team-resources': case 'team-links': case 'team-updates': case 'team-update': return teamHomePath(w, route.teamKey)
    case 'team-issues': case 'team-triage': case 'team-board': case 'team-archive': return teamIssuesPath(w, route.teamKey)
    case 'team-cycles': case 'cycle': case 'cycle-upcoming': return teamCyclesPath(w, route.teamKey)
    case 'team-projects': case 'team-projects-saved-view': case 'team-projects-new-view': return teamProjectsPath(w, route.teamKey)
    case 'team-initiatives': return teamInitiativesPath(w, route.teamKey)
    case 'team-views': case 'team-views-new': case 'team-saved-view': return teamViewsPath(w, route.teamKey)
    case 'workspace-views': case 'workspace-views-new': case 'workspace-saved-view': return workspaceViewsPath(w)
    default: return
  }
}

import { parseAppRoute, type AppRoute } from './app-routes'
import * as pages from './route-pages'

type PageName = keyof typeof pages
// Exhaustive so new route kinds cannot silently miss code preloading.
const routePages = {
  root: [], 'workspace-root': [], 'not-found': [],
  'workspace-onboarding': ['WorkspaceOnboarding'], inbox: ['InboxAppPage'], search: ['WorkspaceSearchPage'],
  diary: ['WorkspaceSecondaryPage'], meeting: ['WorkspaceSecondaryPage'], automations: ['WorkspaceSecondaryPage'],
  'automation-new': ['WorkspaceSecondaryPage'], 'automation-detail': ['WorkspaceSecondaryPage'], 'automation-runs': ['WorkspaceSecondaryPage'],
  pulse: ['PulsePage'], 'my-issues': ['MyIssuesPage'], reviews: ['ReviewsPage'], review: ['ReviewsPage'],
  'workspace-issues': ['IssueExplorerPage'], 'team-issues': ['IssueExplorerPage'],
  'team-overview': ['TeamOverviewPage'], 'team-documents': ['TeamOverviewPage'], 'team-members': ['TeamOverviewPage'], 'team-loops': ['TeamOverviewPage'],
  'team-board': ['WorkspaceSecondaryPage'], 'team-triage': ['WorkspaceSecondaryPage'], 'team-updates': ['WorkspaceSecondaryPage'],
  'team-update': ['WorkspaceSecondaryPage'], 'team-resources': ['WorkspaceSecondaryPage'], 'team-links': ['WorkspaceSecondaryPage'],
  'team-cycles': ['CyclesPage'], cycle: ['CycleDetailPage'], 'cycle-upcoming': ['CycleDetailPage'],
  'team-initiatives': ['InitiativesPage'], initiatives: ['InitiativesPage'], initiative: ['InitiativeDetailPage'],
  'workspace-saved-view': ['IssueExplorerPage'], 'team-saved-view': ['IssueExplorerPage'],
  'workspace-views': ['ViewsPage'], 'team-views': ['ViewsPage'],
  'workspace-views-new': [], 'team-views-new': [],
  'workspace-members': ['WorkspaceDirectoryPage'], 'workspace-customers': ['WorkspaceDirectoryPage'], 'workspace-teams': ['WorkspaceDirectoryPage'],
  'member-profile': ['MemberProfilePage'], customer: ['CustomerDetailPage'], documents: ['DocumentsIndexPage'], document: ['DocumentPage'],
  analytics: ['AnalyticsDashboardPage'], dashboards: ['DashboardsPage'],
  'release-note': ['WorkspaceSecondaryPage'], label: ['WorkspaceSecondaryPage'], drafts: ['WorkspaceOperationsPage'],
  agent: ['AgentPage'], releases: ['WorkspaceOperationsPage'], 'release-pipeline': ['WorkspaceOperationsPage'], release: ['WorkspaceOperationsPage'], asks: ['WorkspaceOperationsPage'],
  loops: ['LoopsPage'], 'loop-editor': ['LoopsPage'], 'team-archive': ['TeamArchivePage'], 'new-team': ['TeamCreatePage'], settings: ['SettingsPage'],
  projects: ['ProjectsPage'], 'team-projects': ['ProjectsPage'], 'projects-new-view': ['ProjectsPage'], 'team-projects-new-view': ['ProjectsPage'],
  'projects-saved-view': ['ProjectsPage'], 'team-projects-saved-view': ['ProjectsPage'],
  project: ['ProjectDetailPage'], 'project-saved-view': ['ProjectDetailPage'], issue: ['DetailPane'],
} satisfies Record<AppRoute['kind'], PageName[]>

export function pagesForRoute(route: AppRoute): PageName[] {
  if (route.kind === 'workspace-views-new' || route.kind === 'team-views-new') return [route.resource === 'projects' ? 'ProjectsPage' : 'IssueExplorerPage']
  return routePages[route.kind]
}

export async function preloadRoute(path: string) {
  const url = new URL(path, window.location.href)
  if (url.origin !== window.location.origin) return
  const route = parseAppRoute(url.pathname, url.search)
  await Promise.all(pagesForRoute(route).map(name => pages[name].preload()))
  if (route.kind === 'settings') {
    const { preloadSettingsPage } = await import('@/components/settings/settings-page')
    await preloadSettingsPage(route.page, route)
  }
}

/** Exclude downloads, external/new-tab links, hashes, and routes owned by the server. */
export function clientNavigationTarget(anchor: HTMLAnchorElement, current: URL): string | undefined {
  if (anchor.hasAttribute('download') || (anchor.target && anchor.target !== '_self') || anchor.hasAttribute('data-native-navigation')) return
  const href = anchor.getAttribute('href')
  if (!href || href.startsWith('#')) return
  let url: URL
  try { url = new URL(href, current) } catch { return }
  if (url.origin !== current.origin) return
  if (/^\/(?:api|oauth|login|signup|register|forgot-password|reset-password|verify-email|invite)(?:\/|$)/.test(url.pathname)) return
  if (url.pathname === current.pathname && url.search === current.search && url.hash) return
  const route = parseAppRoute(url.pathname, url.search)
  if (route.kind === 'not-found' || route.kind === 'root') return
  return url.pathname + url.search + url.hash
}

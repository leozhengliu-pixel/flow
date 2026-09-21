import {
  agentPath,
  inboxPath,
  myIssuesPath,
  reviewsPath,
  searchPath,
  teamIssuesPath,
  teamTriagePath,
  type TeamIssuesRouteView,
} from '@/lib/app-routes'

/** LS-0563 — viewKind / origin descriptors for SplitView issue hosts. */
export type SplitViewOriginKind =
  | 'myIssues'
  | 'customView'
  | 'teamView'
  | 'reviews'
  | 'inbox'
  | 'search'
  | 'triage'
  | 'agents'
  | 'issueView'

export type SplitViewOrigin =
  | { type: 'myIssues'; view?: string }
  | { type: 'customView'; customViewId: string; path?: string; label?: string }
  | { type: 'teamView'; teamKey: string; viewKind: TeamIssuesRouteView | string }
  | { type: 'reviews' }
  | { type: 'inbox' }
  | { type: 'search' }
  | { type: 'triage'; teamKey: string }
  | { type: 'agents'; chatSlug?: string }
  | { type: 'issueView'; teamKey?: string }

export interface SplitViewOriginLink {
  label: string
  rootPath: string
  viewKind: SplitViewOriginKind
  breadcrumb: {
    id: string
    label: string
    path: string
    noShrink?: boolean
  }
}

const TEAM_VIEW_LABELS: Record<string, string> = {
  active: 'Active',
  backlog: 'Backlog',
  all: 'All issues',
}

export function isTeamIssuesViewKind(value: string): value is TeamIssuesRouteView {
  return value === 'active' || value === 'backlog' || value === 'all'
}

export function teamIssuesViewLabel(viewKind: string) {
  return TEAM_VIEW_LABELS[viewKind] ?? 'Issues'
}

export function teamIssuesViewPath(workspaceSlug: string, teamKey: string, viewKind: string) {
  if (isTeamIssuesViewKind(viewKind)) return teamIssuesPath(workspaceSlug, teamKey, viewKind)
  return teamIssuesPath(workspaceSlug, teamKey, 'all')
}

export function resolveSplitViewOrigin(
  workspaceSlug: string,
  origin: SplitViewOrigin | null | undefined,
): SplitViewOriginLink | undefined {
  if (!origin) return undefined

  switch (origin.type) {
    case 'myIssues': {
      const path = myIssuesPath(workspaceSlug)
      return {
        label: 'My issues',
        rootPath: path,
        viewKind: 'myIssues',
        breadcrumb: { id: 'my-issues', label: 'My issues', path, noShrink: true },
      }
    }
    case 'customView': {
      const path = origin.path ?? `#view/${origin.customViewId}`
      const label = origin.label ?? 'View'
      return {
        label,
        rootPath: path,
        viewKind: 'customView',
        breadcrumb: { id: origin.customViewId, label, path, noShrink: true },
      }
    }
    case 'teamView': {
      const label = teamIssuesViewLabel(origin.viewKind)
      const path = teamIssuesViewPath(workspaceSlug, origin.teamKey, origin.viewKind)
      return {
        label,
        rootPath: path,
        viewKind: 'teamView',
        breadcrumb: { id: `team-issues:${origin.teamKey}`, label, path, noShrink: true },
      }
    }
    case 'reviews': {
      const path = reviewsPath(workspaceSlug)
      return {
        label: 'Reviews',
        rootPath: path,
        viewKind: 'reviews',
        breadcrumb: { id: 'reviews', label: 'Reviews', path, noShrink: true },
      }
    }
    case 'inbox': {
      const path = inboxPath(workspaceSlug)
      return {
        label: 'Inbox',
        rootPath: path,
        viewKind: 'inbox',
        breadcrumb: { id: 'inbox', label: 'Inbox', path, noShrink: true },
      }
    }
    case 'search': {
      const path = searchPath(workspaceSlug)
      return {
        label: 'Search',
        rootPath: path,
        viewKind: 'search',
        breadcrumb: { id: 'search', label: 'Search', path, noShrink: true },
      }
    }
    case 'triage': {
      const path = teamTriagePath(workspaceSlug, origin.teamKey)
      return {
        label: 'Triage',
        rootPath: path,
        viewKind: 'triage',
        breadcrumb: { id: 'triage', label: 'Triage', path, noShrink: true },
      }
    }
    case 'agents': {
      const path = agentPath(workspaceSlug, origin.chatSlug)
      return {
        label: 'Agents',
        rootPath: path,
        viewKind: 'agents',
        breadcrumb: { id: 'agents', label: 'Agents', path, noShrink: true },
      }
    }
    case 'issueView': {
      if (!origin.teamKey) return undefined
      const path = teamIssuesPath(workspaceSlug, origin.teamKey, 'all')
      return {
        label: 'Issues',
        rootPath: path,
        viewKind: 'issueView',
        breadcrumb: { id: 'issue-view', label: 'Issues', path, noShrink: true },
      }
    }
    default:
      return undefined
  }
}

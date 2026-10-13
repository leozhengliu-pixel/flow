import { useEffect, useRef } from 'react'
import { teamIssuesPath, workspaceRootPath, type AppRoute } from '@/lib/app-routes'
import { prefilledIssueContext } from '@/lib/prefilled-issue-url'
import type { MyIssuesCreateContext } from '@/components/my-issues/my-issues-list'
import type { BootstrapData } from '@/types/flow'

/**
 * Handles `/:workspace/new?…` and `/:workspace/team/:KEY/new?…`: opens the
 * create-issue composer prefilled from the query, then replaces the URL with the
 * team's issues (or the workspace home) so a refresh never reopens it.
 */
export function usePrefilledIssueRoute({ route, data, location, navigateTo, openCreateIssue }: {
  route: AppRoute
  data: BootstrapData | null
  location: { key: string; search: string }
  navigateTo: (to: string, options?: { replace?: boolean }) => void
  openCreateIssue: (context?: MyIssuesCreateContext) => void
}) {
  const handled = useRef('')
  useEffect(() => {
    if (route.kind !== 'new-issue' || !data || data.workspace.urlKey !== route.workspaceSlug) return
    if (handled.current === location.key) return
    handled.current = location.key
    const context = prefilledIssueContext(location.search, data, { pathTeamKey: route.teamKey })
    const team = route.teamKey ? data.teams.find(item => item.key.toLowerCase() === route.teamKey!.toLowerCase()) : undefined
    openCreateIssue(context)
    navigateTo(team ? teamIssuesPath(data.workspace.urlKey, team.key, 'active') : workspaceRootPath(data.workspace.urlKey), { replace: true })
  }, [data, location.key, location.search, navigateTo, openCreateIssue, route])
}

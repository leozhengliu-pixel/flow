import type { AppRoute } from '@/lib/app-routes'
import type { MyIssuesCreateContext } from '@/components/my-issues/my-issues-list'
import type { BootstrapData } from '@/types/flow'
import { findBySlugRef } from '@/lib/resource-slug'
import { teamForWorkflowState } from '@/lib/team-workflow'

/**
 * Context for the global "C" create-issue shortcut. Like the reference app, pressing C on a
 * project page (any tab or saved project view) prefills that project, and on a team's pages
 * (issues, board, cycles, views…) it opens the dialog in that team.
 */
export function createIssueShortcutContext(route: AppRoute, data: Pick<BootstrapData, 'projects' | 'resourceDetailsOmitted' | 'teams'> | null | undefined): MyIssuesCreateContext | undefined {
  if (!data || data.resourceDetailsOmitted) return undefined
  if (route.kind === 'project' || route.kind === 'project-saved-view') {
    const project = findBySlugRef(data.projects, route.projectSlugId)
    return project ? { projectId: project.id, teamId: project.teamIds[0] } : undefined
  }
  const teamKey = 'teamKey' in route && typeof route.teamKey === 'string' ? route.teamKey.toUpperCase() : undefined
  const team = teamKey ? data.teams.find(candidate => candidate.key.toUpperCase() === teamKey && !candidate.retiredAt) : undefined
  return team ? { teamId: team.id } : undefined
}

/**
 * Context for creating an issue from a project page. A status preset (a status group's
 * "+") picks the project team whose workflow has that status, so a multi-team project
 * never pairs one team with another team's status; otherwise the first project team.
 */
export function projectCreateIssueContext(data: Pick<BootstrapData, 'projects' | 'states' | 'teamSettings'>, projectId: string, projectMilestoneId?: string, context?: MyIssuesCreateContext): MyIssuesCreateContext {
  const teamIds = data.projects.find(project => project.id === projectId)?.teamIds ?? []
  const teamId = context?.teamId ?? teamForWorkflowState(data, teamIds, context?.stateId) ?? teamIds[0]
  return { ...context, projectId, projectMilestoneId, teamId }
}

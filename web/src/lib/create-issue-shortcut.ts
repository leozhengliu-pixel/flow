import type { AppRoute } from '@/lib/app-routes'
import type { MyIssuesCreateContext } from '@/components/my-issues/my-issues-list'
import type { BootstrapData } from '@/types/flow'

/**
 * Context for the global "C" create-issue shortcut. Like Linear, pressing C on a
 * project page (any tab or saved project view) prefills that project.
 */
export function createIssueShortcutContext(route: AppRoute, data: Pick<BootstrapData, 'projects' | 'resourceDetailsOmitted'> | null | undefined): MyIssuesCreateContext | undefined {
  if (!data || data.resourceDetailsOmitted) return undefined
  if (route.kind !== 'project' && route.kind !== 'project-saved-view') return undefined
  const project = data.projects.find(item => item.slugId === route.projectSlugId)
  return project ? { projectId: project.id, teamId: project.teamIds[0] } : undefined
}

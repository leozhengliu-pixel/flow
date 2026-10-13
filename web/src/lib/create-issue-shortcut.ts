import type { AppRoute } from '@/lib/app-routes'
import type { MyIssuesCreateContext } from '@/components/my-issues/my-issues-list'
import type { BootstrapData } from '@/types/flow'
import { findBySlugRef } from '@/lib/resource-slug'

/**
 * Context for the global "C" create-issue shortcut. Like the reference app, pressing C on a
 * project page (any tab or saved project view) prefills that project.
 */
export function createIssueShortcutContext(route: AppRoute, data: Pick<BootstrapData, 'projects' | 'resourceDetailsOmitted'> | null | undefined): MyIssuesCreateContext | undefined {
  if (!data || data.resourceDetailsOmitted) return undefined
  if (route.kind !== 'project' && route.kind !== 'project-saved-view') return undefined
  const project = findBySlugRef(data.projects, route.projectSlugId)
  return project ? { projectId: project.id, teamId: project.teamIds[0] } : undefined
}

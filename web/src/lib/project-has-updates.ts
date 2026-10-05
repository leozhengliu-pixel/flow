import type { Project, ProjectUpdate } from '@/types/flow'

/**
 * Whether a project has any update. Uses loaded updates when present; paged
 * workspaces may not have loaded them, so fall back to the project's health
 * ("noUpdate" until the first update is posted).
 */
export function projectHasUpdates(project: Pick<Project, 'id' | 'health'>, projectUpdates: Record<string, ProjectUpdate[]> | undefined) {
  const updates = projectUpdates?.[project.id]
  return updates ? updates.length > 0 : project.health !== 'noUpdate'
}

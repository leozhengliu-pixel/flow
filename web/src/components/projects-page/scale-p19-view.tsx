import { createContext } from 'react'
import type { ProjectDataGroup, ProjectPageItem } from './projects-data-view'
import type { ScaleP19WindowMetadata } from '@/lib/scale-p19-window-api'

export const ScaleP19TimelineContext = createContext<string[] | undefined>(undefined)

export function scaleP19Groups(groups: ProjectDataGroup[], projects: ProjectPageItem[], metadata?: Partial<ScaleP19WindowMetadata>): ProjectDataGroup[] {
  if (!metadata?.groups) return groups
  const ordered = new Map(projects.map((project, index) => [project.id, index]))
  const adapt = (group: ProjectDataGroup, parentId?: string): ProjectDataGroup => {
    const total = metadata.groups!.find(item => item.id === group.id && item.parentId === parentId)?.total
    return { ...group, windowTotal: total, projects: [...group.projects].sort((a, b) => ordered.get(a.id)! - ordered.get(b.id)!), subgroups: group.subgroups?.map(subgroup => adapt(subgroup, group.id)) }
  }
  return groups.map(group => adapt(group))
}

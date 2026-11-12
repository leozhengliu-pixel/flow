import { cacheStringBytes } from '@/lib/scale-p08'
import type { ProjectFilter } from '@/components/projects-page/projects-filter-model'
import type { ProjectInsightFilter, ProjectInsightMode } from '@/components/projects-page/projects-insights-sidebar'

export type ScaleP19ProjectHandoff = {
  filters: ProjectFilter[]
  extraFilters: ProjectFilter[]
  selectedIds: string[]
  sidebarOpen: boolean
  insightMode: ProjectInsightMode
  insightFilter: ProjectInsightFilter
}

let pending: { scope: string; expires: number; value: ScaleP19ProjectHandoff } | undefined

/** One transient UI handoff lets a legacy bootstrap preserve the user's current view. */
export function writeScaleP19ProjectHandoff(scope: string, value: ScaleP19ProjectHandoff) {
  pending = cacheStringBytes(JSON.stringify(value)) <= 4 * 1024 * 1024 ? { scope, value, expires: Date.now() + 30_000 } : undefined
}

export function readScaleP19ProjectHandoff(scope: string) {
  if (!pending || pending.expires < Date.now()) { pending = undefined; return undefined }
  return pending.scope === scope ? pending.value : undefined
}

export function clearScaleP19ProjectHandoff(scope: string) { if (pending?.scope === scope) pending = undefined }

import { cacheStringBytes } from '@/lib/realtime-cache-limits'
import type { ProjectFilter } from '@/components/projects-page/projects-filter-model'
import type { ProjectInsightFilter, ProjectInsightMode } from '@/components/projects-page/projects-insights-sidebar'

export type ProjectWindowHandoff = {
  filters: ProjectFilter[]
  extraFilters: ProjectFilter[]
  selectedIds: string[]
  sidebarOpen: boolean
  insightMode: ProjectInsightMode
  insightFilter: ProjectInsightFilter
}

let pending: { scope: string; expires: number; value: ProjectWindowHandoff } | undefined

/** One transient UI handoff lets a legacy bootstrap preserve the user's current view. */
export function writeProjectWindowHandoff(scope: string, value: ProjectWindowHandoff) {
  pending = cacheStringBytes(JSON.stringify(value)) <= 4 * 1024 * 1024 ? { scope, value, expires: Date.now() + 30_000 } : undefined
}

export function readProjectWindowHandoff(scope: string) {
  if (!pending || pending.expires < Date.now()) { pending = undefined; return undefined }
  return pending.scope === scope ? pending.value : undefined
}

export function clearProjectWindowHandoff(scope: string) { if (pending?.scope === scope) pending = undefined }

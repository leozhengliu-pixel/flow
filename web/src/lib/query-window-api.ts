import { ApiError, request } from '@/lib/api-client'
import type { ProjectQueryPage } from '@/lib/api'
import type { Initiative, InitiativeUpdate, ProjectRelation, ProjectUpdate } from '@/types/flow'
import type { ProjectsDisplaySettings } from '@/components/projects-page/projects-display-model'

export const QUERY_WINDOWS_CAPABILITY = 'query-windows-v1'
export type ServerWindowQuery = {
  schema: 1
  teamIds?: string[]
  filters?: Array<{ field: string; operator: string; values: string[] }>
  search?: string
  display?: Partial<ProjectsDisplaySettings>
  leadTeamId?: string
  insight?: { kind: string; value: string } | null
  locale?: string
  dateLocale?: string
  now?: string
  localMonth?: string
  view?: string
  initiativeFilters?: Record<string, string | number>
  filterMode?: 'all' | 'any'
  sort?: string
  grouping?: string
  showNested?: boolean
  showParents?: boolean
  showTeamInitiatives?: boolean
  showContributing?: boolean
  collapsed?: string[]
}
export type InitiativeProjectAggregate = { projects: number; completed: number; needingUpdate: number; hasUpdates: boolean }
export type InitiativeWindowEntry = { key: string; kind: 'group' | 'initiative'; id?: string; label?: string; count?: number; entityName?: boolean; depth: number; childCount: number; contextOnly?: boolean }
export type QueryWindowMetadata = {
  schema: 1; revision: string; authzEpoch: string; offset: number
  versions: Record<string, string>
  counts?: Record<string, Record<string, number>>
  groups?: Array<{ id: string; name: string; parentId?: string; total: number }>
  timeline?: string[]
  latestUpdates?: Record<string, string>
  projectRelations?: ProjectRelation[]
  insights?: Record<string, Array<{ value: string; label: string; count: number; className?: string; lead?: { id: string; name: string; avatarUrl?: string } }>>
}
export type ProjectServerWindowPage = ProjectQueryPage & QueryWindowMetadata
export type InitiativeServerWindowPage = QueryWindowMetadata & { items: Initiative[]; entries?: InitiativeWindowEntry[]; aggregates?: Record<string, InitiativeProjectAggregate>; total: number; hasMore: boolean; nextCursor?: string }

export async function readQueryWindow<T extends ProjectServerWindowPage | InitiativeServerWindowPage>(workspace: string, kind: 'projects' | 'initiatives', query: ServerWindowQuery, cursor?: string, signal?: AbortSignal): Promise<T> {
  const params = new URLSearchParams({ kind, window: JSON.stringify(query), limit: '100' })
  if (cursor) params.set('cursor', cursor)
  const page = await request<T>(`/api/sync/records?${params}`, { signal, headers: { 'X-Workspace-Key': workspace } })
  validateQueryWindowPage(page, cursor)
  if (!page.versions || page.items.some(item => typeof page.versions[item.id] !== 'string' || !/^\d+$/.test(page.versions[item.id]))) throw new ApiError('Window record versions unavailable', 501)
  return page
}

export function validateQueryWindowPage(page: { schema: number; revision: string; authzEpoch: string; items: unknown[]; hasMore: boolean; nextCursor?: string; total: number; offset: number }, cursor?: string) {
  if (page.schema !== 1 || typeof page.revision !== 'string' || !/^\d+$/.test(page.revision) || typeof page.authzEpoch !== 'string' || !page.authzEpoch || !Array.isArray(page.items) || page.items.length > 100 || !Number.isSafeInteger(page.total) || page.total < 0 || !Number.isSafeInteger(page.offset) || page.offset < 0 || typeof page.hasMore !== 'boolean' || page.hasMore && (!page.nextCursor || page.nextCursor === cursor) || new TextEncoder().encode(JSON.stringify(page)).byteLength > 256 * 1024) throw new ApiError('Query window unavailable', 501)
}

export type UpdateWindowPage = { schema: 1; items: ProjectUpdate[]; details?: Record<string, boolean>; total: number; offset: number; revision: string; authzEpoch: string; hasMore: boolean; nextCursor?: string }
export async function readUpdateWindow<T extends ProjectUpdate | InitiativeUpdate = ProjectUpdate>(workspace: string, projectId: string, cursor: string | undefined, signal: AbortSignal, kind: 'projects' | 'initiatives' = 'projects'): Promise<Omit<UpdateWindowPage, 'items'> & { items: T[] }> {
  const endpoint = `/api/${kind}/${encodeURIComponent(projectId)}/updates`
  const params = new URLSearchParams({ window: '1', limit: '20' })
  if (cursor) params.set('cursor', cursor)
  const init = { signal, headers: { 'X-Workspace-Key': workspace } }
  const page = await request<Omit<UpdateWindowPage, 'items'> & { items: T[] }>(`${endpoint}?${params}`, init)
  validateQueryWindowPage(page, cursor)
  const items: T[] = []
  for (const item of page.items) {
    if (!page.details?.[item.id]) { items.push(item); continue }
    const chunks: Uint8Array[] = []
    let next: string | undefined, bytes = 0
    do {
      const query = new URLSearchParams({ window: '1', update: item.id })
      if (next) query.set('cursor', next)
      const fragment = await request<{ schema: number; revision: string; authzEpoch: string; encoding: string; data: string; offset: number; hasMore: boolean; nextCursor?: string }>(`${endpoint}?${query}`, init)
      if (fragment.schema !== 1 || fragment.revision !== page.revision || fragment.authzEpoch !== page.authzEpoch || fragment.encoding !== 'base64-json' || fragment.offset !== bytes || fragment.data.length > 48 * 1024 || fragment.hasMore && (!fragment.nextCursor || fragment.nextCursor === next)) throw new ApiError('Update changed while loading', 409)
      const chunk = Uint8Array.from(atob(fragment.data), character => character.charCodeAt(0))
      chunks.push(chunk); bytes += chunk.byteLength
      if (bytes > 8 * 1024 * 1024) throw new ApiError('Update exceeds the window budget', 501)
      next = fragment.hasMore ? fragment.nextCursor : undefined
    } while (next)
    const raw = new Uint8Array(bytes)
    let offset = 0
    for (const chunk of chunks) { raw.set(chunk, offset); offset += chunk.length }
    const value = JSON.parse(new TextDecoder().decode(raw)) as T
    if (value.id !== item.id) throw new ApiError('Update identity changed', 409)
    items.push(value)
  }
  return { ...page, items }
}

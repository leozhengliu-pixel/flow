import { fetchBootstrap, getDocument } from '@/lib/api'
import { ApiError, request } from '@/lib/api-client'
import { findTrashedDocument } from '@/components/documents/document-trash'
import { fetchSurfaceBootstrap, syncClientFor, syncV2Enabled, SyncProtocolUnavailable, type SyncClient, type SyncSurface } from '@/lib/sync-client'
import type { AppRoute } from '@/lib/app-routes'
import type { BootstrapData, DocumentRevision, FlowDocument, SyncPageRequest, TrashEntry, User, WorkspaceMember } from '@/types/flow'
import type { QueryWindow } from '@/lib/query-window-store'
import { findDocumentBySlugSuffix } from '@/lib/document-slug'
import { modelRecordKind } from '@/lib/scale-p13-wire'

export type ResourceSurface = 'documents' | 'trash' | 'people' | 'pickers'

export function resourceWindowEnabled(surface: ResourceSurface): boolean {
  if (!syncV2Enabled()) return false
  const flags = {
    documents: import.meta.env.VITE_DOCUMENT_WINDOWS,
    trash: import.meta.env.VITE_TRASH_WINDOWS,
    people: import.meta.env.VITE_PEOPLE_WINDOWS,
    pickers: import.meta.env.VITE_PICKER_WINDOWS,
  }
  return flags[surface] === 'true'
}

export function resourceSyncSurface(route: AppRoute): SyncSurface | undefined {
  if (route.kind === 'document') return 'document-detail'
  if (route.kind === 'issue') return 'issue-detail'
  if (route.kind === 'team-documents' && resourceWindowEnabled('documents')) return 'document-directory'
  if (route.kind === 'team-archive' && route.tab.startsWith('recently-deleted') && resourceWindowEnabled('trash')) return 'trash-directory'
  if ((route.kind === 'workspace-members' || route.kind === 'settings' && route.page === 'members') && resourceWindowEnabled('people')) return 'people-directory'
  return undefined
}

/** A loaded page is not an authoritative set of everything matching a query. */
export function resourceSelection(window: QueryWindow | undefined, selectedIds?: readonly string[]) {
  const loadedIds = (window?.keys ?? []).map(key => (JSON.parse(key) as [string, string])[1])
  const selected = selectedIds ? [...new Set(selectedIds)] : undefined
  return {
    loadedIds,
    selectedIds: selected,
    completeIds: window?.completeness === 'complete' ? loadedIds : undefined,
    allSelected: selected !== undefined && window?.completeness === 'complete' && loadedIds.length > 0 && loadedIds.every(id => selected.includes(id)),
  }
}

export function resourceWindowDocuments(data: BootstrapData, documents: FlowDocument[]): FlowDocument[] {
  const client = syncClientFor(data)
  const window = client?.surface === 'document-directory' ? client.windows.get({ kind: 'document' }) : undefined
  if (window?.completeness !== 'complete') return documents
  const ids = new Set(resourceSelection(window).loadedIds)
  return documents.filter(document => ids.has(document.id))
}

/** Local grouping, counts and select-all require the complete summary set, not just the first page. */
export async function loadResourceSummaries(data: BootstrapData, query: SyncPageRequest, signal?: AbortSignal): Promise<BootstrapData> {
  const client = syncClientFor(data)
  if (!client) return data
  let cursor: string | undefined
  for (let pages = 0; pages < 100; pages++) {
    signal?.throwIfAborted()
    const window = await client.readWindow({ ...query, limit: 100, maxBytes: 256 * 1024, cursor })
    signal?.throwIfAborted()
    for (const token of window.keys) {
      const [kind, id] = JSON.parse(token) as [string, string]
      if (kind !== modelRecordKind(query.kind)) throw new SyncProtocolUnavailable('Resource page returned another collection')
      const record = client.get({ kind, id })
      if (!record?.value || record.completeness === 'omitted' || record.completeness === 'deleted') throw new SyncProtocolUnavailable('Resource summary requires the legacy adapter')
    }
    if (window.completeness === 'complete') return client.adapt(data)
    if (!window.next || window.next === cursor) throw new SyncProtocolUnavailable('Resource window did not advance')
    cursor = window.next
  }
  throw new SyncProtocolUnavailable('Complete selection exceeds the resource window budget')
}

export async function fetchResourceSurfaceBootstrap(workspaceKey: string, surface: SyncSurface | undefined, legacy: () => Promise<BootstrapData>, signal?: AbortSignal, quiet?: boolean): Promise<BootstrapData> {
  // This base has no trash page endpoint. Keep payloads, counts and restore links
  // authoritative until the server supplies that read, rather than paging a partial local array.
  if (surface === 'trash-directory') return legacy()
  const data = await fetchSurfaceBootstrap(workspaceKey, surface, legacy, signal, quiet)
  const client = syncClientFor(data)
  if (!client || surface !== 'document-directory' && surface !== 'people-directory') return data
  const kind = surface === 'document-directory' ? 'document' : 'user'
  // The existing directory components also use these complete relationship sets.
  // A server without them must keep its legacy semantics until targeted reads are available.
  const required = surface === 'document-directory'
    ? ['projects', 'initiatives', 'cycles', 'users', 'members', 'teamMembers', 'favorites', 'subscriptions']
    : ['members', 'departedMembers', 'invitations', 'teamMembers']
  if (!client.manifest.collections[kind] || required.some(field => !Array.isArray(client.manifest.core[field as keyof BootstrapData]))) {
    client.dispose()
    return legacy()
  }
  try {
    const loaded = await loadResourceSummaries(data, { kind }, signal)
    await client.catchUp()
    if (resourceWindowStale(client)) throw new SyncProtocolUnavailable('Resource selection changed during initialization')
    return client.adapt(loaded)
  }
  catch (error) {
    client.dispose()
    if (signal?.aborted || error instanceof ApiError && [401, 403].includes(error.status)) throw error
    return legacy()
  }
}

export function resourceWindowStale(client: SyncClient): boolean {
  const kind = client.surface === 'document-directory' ? 'document' : client.surface === 'people-directory' ? 'user' : undefined
  return Boolean(kind && client.windows.get({ kind })?.completeness === 'stale')
}

export function documentBodyLoaded(data: BootstrapData, document: FlowDocument): boolean {
  if (!Object.hasOwn(document, 'content') || typeof document.content !== 'string') return false
  if (document.deletedAt) {
    const entry = data.trash.find(item => item.resourceType === 'document' && item.resourceId === document.id)
    let payload = entry?.payload
    if (typeof payload === 'string') { try { payload = JSON.parse(payload) } catch { return false } }
    return Boolean(payload && typeof payload === 'object' && 'content' in payload && typeof payload.content === 'string')
  }
  const client = syncClientFor(data)
  if (!client) return !data.resourceDetailsOmitted
  const key = { kind: 'document', id: document.id }
  const record = client.get(key)
  return record?.completeness === 'detail' || record?.completeness === 'stale' && Boolean(client.getDetail(key))
}

export function resourceTrashedDocument(trash: TrashEntry[] | undefined, slug: string) {
  let match = findTrashedDocument(trash, slug)
  if (!match && resourceWindowEnabled('trash')) {
    const candidates = (trash ?? []).flatMap(entry => {
      const candidate = findTrashedDocument([entry], entry.resourceId)
      return candidate ? [candidate] : []
    })
    const document = findDocumentBySlugSuffix(candidates.map(item => item.document), slug)
    match = document ? candidates.find(item => item.document.id === document.id) : undefined
  }
  return match && (resourceWindowEnabled('documents') || resourceWindowEnabled('trash'))
    ? { ...match, document: { ...match.document, deletedAt: match.entry.deletedAt } }
    : match
}

export async function resourceDocumentForAction(data: BootstrapData, document: FlowDocument): Promise<FlowDocument> {
  if (!resourceWindowEnabled('documents') || documentBodyLoaded(data, document)) return document
  const controller = new AbortController()
  const client = syncClientFor(data)
  const signal = client?.requestSignal(controller.signal) ?? controller.signal
  try {
    const record = client ? await client.readDetail({ kind: 'document', id: document.id }, signal, true) : undefined
    if (client && (!record || record.completeness === 'deleted')) throw new ApiError('Document not found', 404)
    // A legal body can exceed the batch budget; its existing detail endpoint remains complete.
    const loaded = record?.completeness === 'detail' ? record.value as unknown as FlowDocument : await getDocument(document.id, signal, data.workspace.urlKey)
    signal.throwIfAborted()
    if (!Object.hasOwn(loaded, 'content') || loaded.deletedAt) throw new Error('Document body is unavailable')
    return loaded
  } catch (error) {
    if (client && !client.disposed && error instanceof ApiError && [401, 403, 404].includes(error.status)) client.requestReset()
    // The action supplies its existing translated failure message.
    throw new Error('', { cause: error })
  } finally { controller.abort() }
}

/** Old servers expose the deleted document by slug, but its restore associations live in the legacy trash projection. */
export async function loadResourceDocument(data: BootstrapData, slug: string, signal: AbortSignal) {
  const client = syncClientFor(data)
  let document: FlowDocument
  try {
    document = client ? await client.loadDocument(slug, signal) : await getDocument(slug, signal, data.workspace.urlKey)
  } catch (error) {
    if (!resourceWindowEnabled('trash') || client?.disposed || !(error instanceof Error) || error instanceof ApiError && error.status !== 404) throw error
    document = await getDocument(slug, signal, data.workspace.urlKey)
    if (!document.deletedAt) throw error
  }
  if (document.deletedAt && resourceWindowEnabled('trash')) {
    const candidate = resourceTrashedDocument(data.trash, document.id)
    const known = candidate && documentBodyLoaded(data, candidate.document) ? candidate : undefined
    const trash = known ? undefined : await loadResourceTrash(data.workspace.urlKey, signal)
    const trashed = known ?? resourceTrashedDocument(trash, document.id)
    if (!trashed) throw new ApiError('Document not found', 404)
    if (!documentBodyLoaded({ ...data, trash: [trashed.entry] }, trashed.document)) throw new ApiError('Document not found', 404)
    return { document: trashed.document, entry: trashed.entry }
  }
  return { document, entry: undefined }
}

export async function loadResourceTrash(workspaceKey: string, signal: AbortSignal): Promise<TrashEntry[]> {
  return (await fetchBootstrap(workspaceKey, undefined, signal)).trash
}

export async function loadResourceMembers(data: BootstrapData, signal: AbortSignal): Promise<WorkspaceMember[]> {
  const client = syncClientFor(data)
  if (!client || Array.isArray(client.manifest.core.members)) return data.members
  return (await fetchBootstrap(data.workspace.urlKey, undefined, signal)).members
}

export function loadResourceRevisions(workspaceKey: string, documentId: string, signal: AbortSignal): Promise<DocumentRevision[]> {
  return request<{ nodes: DocumentRevision[] }>(`/api/documents/${encodeURIComponent(documentId)}/history?limit=200`, { signal, headers: { 'X-Workspace-Key': workspaceKey } }).then(page => page.nodes ?? [])
}

export type PeopleWindow = { nodes: User[]; pageInfo: { hasNextPage: boolean; endCursor?: string | null } }

/** Authorized REST connections remain the compatibility transport for people. */
export function searchResourcePeople(workspaceKey: string, query: string, signal: AbortSignal, after?: string): Promise<PeopleWindow> {
  const params = new URLSearchParams({ first: '50' })
  if (after) params.set('after', after)
  if (query.trim()) params.set('filter', JSON.stringify({ or: ['name', 'displayName', 'username', 'email'].map(field => ({ [field]: { containsIgnoreCase: query.trim() } })) }))
  return request(`/api/users?${params}`, { signal, headers: { 'X-Workspace-Key': workspaceKey } })
}

export async function loadResourcePeople(workspaceKey: string, signal: AbortSignal, client?: SyncClient): Promise<User[]> {
  if (client && !Array.isArray(client.manifest.core.members)) return (await fetchBootstrap(workspaceKey, undefined, signal)).users
  if (client?.manifest.collections.user) return (await loadResourceSummaries(client.adapt(), { kind: 'user' }, signal)).users
  const people = new Map<string, User>()
  let after: string | undefined
  for (let pages = 0; pages < 100; pages++) {
    const page = await searchResourcePeople(workspaceKey, '', signal, after)
    for (const user of page.nodes) people.set(user.id, user)
    if (!page.pageInfo.hasNextPage) return [...people.values()]
    if (!page.pageInfo.endCursor || page.pageInfo.endCursor === after) throw new Error('People window did not advance')
    after = page.pageInfo.endCursor
  }
  // Do not turn a capped search pool into a complete people directory.
  const data = await fetchBootstrap(workspaceKey, undefined, signal)
  return data.users
}

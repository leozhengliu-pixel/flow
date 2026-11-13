import { fetchBootstrap, getDocument } from '@/lib/api'
import { ApiError, request } from '@/lib/api-client'
import { findTrashedDocument } from '@/components/documents/document-trash'
import { fetchSurfaceBootstrap, syncClientFor, syncV2Enabled, SyncProtocolUnavailable, SyncResetRequired, type SyncClient, type SyncSurface } from '@/lib/sync-client'
import type { AppRoute } from '@/lib/app-routes'
import type { BootstrapData, Comment, DocumentRevision, FlowDocument, SyncPageRequest, TrashEntry, User, WorkspaceMember } from '@/types/flow'
import type { QueryWindow } from '@/lib/query-window-store'
import { findDocumentBySlugSuffix } from '@/lib/document-slug'
import { modelRecordKind } from '@/lib/scale-p13-wire'
import { createResourceClient, fetchResourceDocument, readResourceCollection, readResourceDirectory, resourceEndpointUnavailable } from '@/lib/scale-p20-transport'

export type ResourceSurface = 'documents' | 'trash' | 'people' | 'pickers'
const documentRefreshes = new WeakMap<SyncClient, Promise<BootstrapData>>()

export function resourceRefreshPending(client?: SyncClient): boolean {
  return Boolean(client && documentRefreshes.has(client))
}

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
  const enabled = surface === 'document-detail' && (resourceWindowEnabled('documents') || resourceWindowEnabled('trash')) || surface === 'document-directory' && resourceWindowEnabled('documents') || surface === 'trash-directory' && resourceWindowEnabled('trash') || surface === 'people-directory' && resourceWindowEnabled('people')
  if (enabled) {
    for (let attempt = 0; attempt < 3; attempt++) {
      let client: SyncClient | undefined
      try {
        client = await createResourceClient(workspaceKey, surface!, signal, quiet)
        const activeSignal = client.requestSignal(signal)
        let data = client.adapt()
        const directory = await readResourceDirectory(data, activeSignal)
        data = { ...data, ...directory }
        // Users include presentation fields not carried by membership records.
        for (const kind of ['user', 'project', 'initiative', 'cycle']) data = await loadResourceSummaries(data, { kind }, activeSignal)
        if (surface === 'document-directory') data = await loadResourceSummaries(data, { kind: 'document' }, activeSignal)
        if (surface === 'trash-directory') data = { ...data, trash: await readResourceCollection<TrashEntry>(workspaceKey, 'trash', activeSignal, '', client.manifest.authzEpoch) }
        await client.catchUp()
        return client.adapt(data)
      } catch (error) {
        client?.dispose()
        if (signal?.aborted || error instanceof ApiError && [401, 403].includes(error.status)) throw error
        if (attempt < 2 && (error instanceof SyncResetRequired && !(error instanceof SyncProtocolUnavailable) || error instanceof ApiError && error.status === 409)) continue
        if (!(error instanceof SyncProtocolUnavailable) && !resourceEndpointUnavailable(error)) throw error
        return legacy()
      }
    }
  }
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

/** Replace an expired scope atomically, keeping the mounted editor until its authorized replacement is ready. */
export function refreshResourceDocument(data: BootstrapData, documentId: string, signal: AbortSignal): Promise<BootstrapData> {
  const previous = syncClientFor(data)
  const existing = previous && documentRefreshes.get(previous)
  if (existing) return existing
  const pending = refreshResourceDocumentScope(data, documentId, signal).finally(() => { if (previous && documentRefreshes.get(previous) === pending) documentRefreshes.delete(previous) })
  if (previous) documentRefreshes.set(previous, pending)
  return pending
}

async function refreshResourceDocumentScope(data: BootstrapData, documentId: string, signal: AbortSignal): Promise<BootstrapData> {
  const next = await fetchResourceSurfaceBootstrap(data.workspace.urlKey, 'document-detail', () => fetchBootstrap(data.workspace.urlKey, undefined, signal), signal, true)
  const client = syncClientFor(next)
  try {
    if (next.viewer.id !== data.viewer.id || next.workspace.id !== data.workspace.id) throw new SyncResetRequired('Document scope changed during refresh')
    const loaded = await loadResourceDocument(next, documentId, signal)
    signal.throwIfAborted()
    if (loaded.entry) return { ...next, trash: [loaded.entry, ...next.trash.filter(entry => entry.id !== loaded.entry!.id)] }
    const comments = await loadResourceComments(data.workspace.urlKey, documentId, signal)
    signal.throwIfAborted()
    return client ? { ...client.adapt(next), comments: { ...next.comments, [documentId]: comments } } : { ...next, documents: [loaded.document, ...next.documents.filter(document => document.id !== documentId)], comments: { ...next.comments, [documentId]: comments } }
  } catch (error) { client?.dispose(); throw error }
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
  if (!(resourceWindowEnabled('documents') || resourceWindowEnabled('trash')) || documentBodyLoaded(data, document)) return document
  const controller = new AbortController()
  const client = syncClientFor(data)
  const signal = client?.requestSignal(controller.signal) ?? controller.signal
  try {
    if (document.deletedAt) return (await loadResourceDocument(data, document.id, signal)).document
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
    document = client ? await client.loadDocument(slug, signal) : await fetchResourceDocument(data.workspace.urlKey, slug, signal)
  } catch (error) {
    if (!resourceWindowEnabled('trash') || client?.disposed || !(error instanceof Error) || error instanceof ApiError && error.status !== 404) throw error
    document = await fetchResourceDocument(data.workspace.urlKey, slug, signal)
    if (!document.deletedAt) throw error
  }
  if (document.deletedAt && resourceWindowEnabled('trash')) {
    const candidate = resourceTrashedDocument(data.trash, document.id)
    const known = candidate && documentBodyLoaded(data, candidate.document) ? candidate : undefined
    const trash = known ? undefined : await loadResourceTrash(data.workspace.urlKey, signal, document.id)
    const trashed = known ?? resourceTrashedDocument(trash, document.id)
    if (!trashed) throw new ApiError('Document not found', 404)
    if (!documentBodyLoaded({ ...data, trash: [trashed.entry] }, trashed.document)) throw new ApiError('Document not found', 404)
    return { document: trashed.document, entry: trashed.entry }
  }
  return { document, entry: undefined }
}

export function loadResourceComments(workspaceKey: string, documentId: string, signal: AbortSignal): Promise<Comment[]> {
  return request(`/api/documents/${encodeURIComponent(documentId)}/comments`, { signal, headers: { 'X-Workspace-Key': workspaceKey } })
}

export async function loadResourceTrash(workspaceKey: string, signal: AbortSignal, resourceId?: string): Promise<TrashEntry[]> {
  try {
    if (resourceId) return [await request<TrashEntry>(`/api/resource-windows/trash?resourceId=${encodeURIComponent(resourceId)}`, { signal, headers: { 'X-Workspace-Key': workspaceKey } })]
    return await readResourceCollection<TrashEntry>(workspaceKey, 'trash', signal)
  } catch (error) {
    if (!resourceEndpointUnavailable(error)) throw error
    return (await fetchBootstrap(workspaceKey, undefined, signal)).trash
  }
}

export async function loadResourceMembers(data: BootstrapData, signal: AbortSignal): Promise<WorkspaceMember[]> {
  const client = syncClientFor(data)
  if (client?.manifest.capabilities.includes('resource-windows') || client && Array.isArray(client.manifest.core.members)) return data.members
  try { return await readResourceCollection<WorkspaceMember>(data.workspace.urlKey, 'members', signal, '', data.sync?.authzEpoch) }
  catch (error) { if (!resourceEndpointUnavailable(error)) throw error; return (await fetchBootstrap(data.workspace.urlKey, undefined, signal)).members }
}

export function loadResourceRevisions(workspaceKey: string, documentId: string, signal: AbortSignal): Promise<DocumentRevision[]> {
  return request<{ nodes: DocumentRevision[] }>(`/api/documents/${encodeURIComponent(documentId)}/history?projection=summary&limit=200`, { signal, headers: { 'X-Workspace-Key': workspaceKey } }).then(page => page.nodes ?? [])
}

export function loadResourceRevision(workspaceKey: string, documentId: string, revisionId: string, signal: AbortSignal): Promise<DocumentRevision> {
  return request(`/api/documents/${encodeURIComponent(documentId)}/history/${encodeURIComponent(revisionId)}`, { signal, headers: { 'X-Workspace-Key': workspaceKey } })
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
  try { return await readResourceCollection<User>(workspaceKey, 'users', signal, '', client?.manifest.authzEpoch) }
  catch (error) { if (!resourceEndpointUnavailable(error)) throw error }
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

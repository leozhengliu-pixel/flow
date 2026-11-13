import { request, ApiError } from '@/lib/api-client'
import { modelChanges, modelManifest, modelRecordBatch, modelRecordPage, wireRecordKind, type SyncWireRecord } from '@/lib/scale-p13-wire'
import { SyncClient, SyncProtocolUnavailable, SyncResetRequired, type SyncSurface } from '@/lib/sync-client'
import type { BootstrapData, FlowDocument, SyncChanges, SyncManifest, SyncRecordBatch, SyncRecordPage } from '@/types/flow'

export const resourceEndpointUnavailable = (error: unknown) => error instanceof ApiError && [404, 405, 501].includes(error.status)

export function fetchResourceDocument(workspace: string, ref: string, signal: AbortSignal): Promise<FlowDocument> {
  return request(`/api/documents/${encodeURIComponent(ref)}?history=summary`, { headers: { 'X-Workspace-Key': workspace }, signal })
}

export async function createResourceClient(workspace: string, surface: SyncSurface, signal?: AbortSignal, quiet?: boolean) {
  const headers = { 'X-Workspace-Key': workspace }
  const manifest = modelManifest(await request<SyncManifest>('/api/sync/manifest?surface=resource-window', { headers, signal, quiet }))
  if (manifest.schema !== 2 || !manifest.capabilities.includes('resource-windows')) throw new SyncProtocolUnavailable('Resource windows unavailable')
  const client = new SyncClient(manifest, workspace, {
    page: (query, signal) => {
      const params = new URLSearchParams({ surface: 'resource-window', kind: wireRecordKind(query.kind), limit: String(query.limit ?? 100) })
      if (query.cursor) params.set('cursor', query.cursor)
      return request<Omit<SyncRecordPage, 'items'> & { items: SyncWireRecord[] }>(`/api/sync/records?${params}`, { headers, signal }).then(modelRecordPage)
    },
    batch: (keys, signal) => request<Omit<SyncRecordBatch, 'items'> & { items: SyncWireRecord[] }>('/api/sync/batch?surface=resource-window', {
      method: 'POST', body: JSON.stringify({ keys: keys.map(key => ({ ...key, kind: wireRecordKind(key.kind) })), projection: 'detail' }),
      headers: { ...headers, 'Content-Type': 'application/json' }, signal,
    }).then(batch => modelRecordBatch(batch, 'detail')),
    changes: (after, signal) => request<SyncChanges>(`/api/sync/changes?surface=resource-window&after=${encodeURIComponent(after)}`, { headers, signal }).then(modelChanges),
    document: (ref, signal) => fetchResourceDocument(workspace, ref, signal),
  })
  client.surface = surface
  return client
}

export async function readResourceCollection<T>(workspace: string, collection: string, signal: AbortSignal, query = '', expectedEpoch?: string): Promise<T[]> {
  const items: T[] = []
  let cursor = '', revision: string | undefined, epoch = expectedEpoch
  for (;;) {
    signal.throwIfAborted()
    const params = new URLSearchParams({ collection, limit: '50' })
    if (cursor) params.set('cursor', cursor)
    if (query.trim()) params.set('q', query.trim())
    const endpoint = collection === 'trash' ? 'trash' : 'members'
    const page = await request<Omit<SyncRecordPage, 'items'> & { items: SyncWireRecord[] }>(`/api/resource-windows/${endpoint}?${params}`, { headers: { 'X-Workspace-Key': workspace }, signal })
    signal.throwIfAborted()
    revision ??= page.revision
    epoch ??= page.authzEpoch
    if (revision !== page.revision || epoch !== page.authzEpoch) throw new SyncResetRequired('Resource directory changed during read')
    for (const item of page.items) {
      if (item.omitted || !item.data) throw new SyncProtocolUnavailable('Incomplete resource directory')
      items.push(item.data as T)
    }
    if (!page.hasMore) return items
    if (!page.next || page.next === cursor) throw new SyncProtocolUnavailable('Resource directory did not advance')
    cursor = page.next
  }
}

export async function readResourceDirectory(data: BootstrapData, signal: AbortSignal): Promise<Pick<BootstrapData, 'members' | 'teamMembers' | 'invitations' | 'departedMembers'>> {
  const workspace = data.workspace.urlKey, epoch = data.sync?.authzEpoch
  // These complete, paged identity sets preserve the existing local filters,
  // counts and bulk-selection semantics without transporting workspace bodies.
  const members = await readResourceCollection<BootstrapData['members'][number]>(workspace, 'members', signal, '', epoch)
  const teamMembers = await readResourceCollection<BootstrapData['teamMembers'][number]>(workspace, 'teamMembers', signal, '', epoch)
  const invitations = await readResourceCollection<BootstrapData['invitations'][number]>(workspace, 'invitations', signal, '', epoch)
  const departedMembers = await readResourceCollection<NonNullable<BootstrapData['departedMembers']>[number]>(workspace, 'departedMembers', signal, '', epoch)
  return { members, teamMembers, invitations, departedMembers }
}

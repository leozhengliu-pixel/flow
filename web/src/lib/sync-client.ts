import { fetchSyncBatch, fetchSyncChanges, fetchSyncManifest, fetchSyncRecords, getDocument } from '@/lib/api'
import { ApiError } from '@/lib/api-client'
import { QueryWindowStore } from '@/lib/query-window-store'
import { RecordLoader, recordKey } from '@/lib/record-loader'
import { cacheStringBytes } from '@/lib/scale-p08'
import { modelChanges, modelRecordKind, syncCollections, SyncWireVersionError } from '@/lib/scale-p13-wire'
import { scaleP19CoreSupported, scaleP19InitiativeCatalogSupported, scaleP19ManifestMatches, scaleP19WindowCoreSupported } from '@/lib/scale-p19-surfaces'
import { scaleP19NeedsCore, scaleP19WindowChanges } from '@/lib/scale-p19-sync'
import { releaseScaleP19Bytes, retainScaleP19Core } from '@/lib/scale-p19-budget'
import type { BootstrapData, FlowDocument, RecordKey, SyncChanges, SyncManifest, SyncPageRequest, SyncRecord, SyncRecordBatch, SyncRecordPage, WorkspaceSyncState } from '@/types/flow'

export type SyncSurface = 'issue-detail' | 'document-detail' | 'document-directory' | 'people-directory' | 'trash-directory' | 'projects-page' | 'initiatives-page'

export function syncV2Enabled() {
  return import.meta.env.VITE_SYNC_V2 === 'true'
}

export function syncBootstrapRequestKey(base: string, surface?: SyncSurface) {
  return syncV2Enabled() && surface ? `${base}:sync:${surface}` : base
}
export const SYNC_DETAIL_MAX_BYTES = 32 * 1024 * 1024
export const SYNC_SUMMARY_MAX_BYTES = 16 * 1024 * 1024

export { syncCollections } from '@/lib/scale-p13-wire'

const clients = new WeakMap<WorkspaceSyncState, SyncClient>()
export const syncClientFor = (data?: BootstrapData | null) => data?.sync ? clients.get(data.sync) : undefined
export const syncScopeMatches = (data: BootstrapData | null | undefined, client: SyncClient | undefined) => syncClientFor(data) === client && !client?.disposed

/** Decimal strings compare without Number's 53-bit rounding. Unknown version formats require a new baseline. */
export function compareSyncVersions(left: string, right: string): number {
  if (!/^\d+$/.test(left) || !/^\d+$/.test(right)) throw new SyncResetRequired('Invalid sync version')
  left = left.replace(/^0+(?=\d)/, '')
  right = right.replace(/^0+(?=\d)/, '')
  return left.length === right.length ? left === right ? 0 : left > right ? 1 : -1 : left.length > right.length ? 1 : -1
}

export class SyncResetRequired extends Error {}
export class SyncProtocolUnavailable extends SyncResetRequired {}

type Transport = {
  batch: (keys: RecordKey[], signal: AbortSignal) => Promise<SyncRecordBatch>
  page: (query: SyncPageRequest, signal: AbortSignal) => Promise<SyncRecordPage>
  changes: (after: string, signal: AbortSignal) => Promise<SyncChanges>
}
type CachedRecord = SyncRecord & { appliedRevision?: number; loadedDetail?: { version: string; value: Record<string, unknown> } }

export function migratedSyncSignal(type: string, surface?: SyncSurface) {
  if ((surface === 'projects-page' || surface === 'initiatives-page') && (/^(?:project|initiative|project_update|initiative_update|project_relation|initiative_relation|user_settings|workspace_preferences|project_status|project_template|team|label|label_group|favorite|subscription)\./.test(type) || type === 'pulse.summary_scheduled')) return true
  return /^(?:resync$|workspace\.resync_required$|issue\.(?:created|updated|deleted|batch_updated)$|document\.(?:created|updated|deleted)$)/.test(type)
}

// These are adapter defaults, not declarations that the server returned an empty collection.
const arrayFields = 'users teams customers states labels issues cycles issueTemplates projectTemplates documentTemplates documents documentContentDrafts customerRequests customerStatuses customerTiers releases releasePipelines releaseNotes releaseHistory customEmojis asks loops slaRules issueSlas slaEvents drafts favorites favoriteFolders subscriptions auditLog trash importJobs exportJobs migrationJobs projects projectRelations projectStatuses initiatives initiativeRelations savedViews notifications notificationDeliveries pushSubscriptions triageResponsibilities triageRoutingRules triageAssignments workflowDefinitions workflowRuns emailIntakeAddresses emailIntakeMessages teamResourceSections teamPinnedResources agentActivities aiConversations aiPromptProgress issueSuggestions labelGroups apiKeys oauthApplications oauthAuthorizations webhooks integrationConnections identityProviders integrationDeliveries gitAutomationStates targetBranches reviews agentSessions agentSkills members teamMembers invitations'.split(' ')
const objectFields = 'cycleSettings teamSettings projectUpdates initiativeUpdates comments activities notificationPreferences userSettings workspaceSettings'.split(' ')

export class SyncClient {
  readonly manifest: SyncManifest
  surface?: SyncSurface
  legacyRequired = false
  readonly windows = new QueryWindowStore()
  readonly loader: RecordLoader
  private transport: Transport
  private controller = new AbortController()
  private records = new Map<string, CachedRecord>()
  private sizes = new Map<string, number>()
  private details = new Map<string, number>()
  private pinned = new Map<string, number>()
  private detailBytes = 0
  private summaryBytes = 0
  private revision = 0
  private coreRevision = 0
  private pendingCore?: SyncManifest
  private changed = new Map<string, number>()
  private listeners = new Set<() => void>()
  private cursor: string
  private targetHead: string
  private collections: SyncManifest['collections']
  private closed = false
  private catchingUp?: Promise<void>

  constructor(manifest: SyncManifest, workspaceKey: string, transport?: Transport) {
    if (manifest.schema !== 2 || !manifest.authzEpoch || manifest.workspaceId !== manifest.core.workspace.id || manifest.viewerId !== manifest.core.viewer.id || manifest.core.workspace.urlKey !== workspaceKey) throw new Error('Invalid sync manifest scope')
    compareSyncVersions(manifest.head, manifest.head)
    this.manifest = manifest
    this.collections = Object.fromEntries(Object.entries(manifest.collections).map(([rawKind, value]) => {
      const kind = modelRecordKind(rawKind)
      const field = syncCollections[kind as keyof typeof syncCollections]
      return [kind, { ...value, complete: value.complete === true && Boolean(field && Array.isArray(manifest.core[field])) }]
    }))
    this.cursor = manifest.head
    this.targetHead = manifest.head
    this.transport = transport ?? {
      batch: (keys, signal) => fetchSyncBatch(workspaceKey, keys, 'detail', signal),
      page: (query, signal) => fetchSyncRecords(workspaceKey, query, signal),
      changes: (after, signal) => fetchSyncChanges(workspaceKey, after, signal),
    }
    this.loader = new RecordLoader({ authzEpoch: manifest.authzEpoch, fetch: this.transport.batch, onEpochChange: () => this.requestReset() })
  }

  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener) } }
  get head() { return this.cursor }
  get needsCatchUp() { return compareSyncVersions(this.cursor, this.targetHead) < 0 }
  get disposed() { return this.closed }
  requestSignal(signal?: AbortSignal) { return signal ? AbortSignal.any([signal, this.controller.signal]) : this.controller.signal }
  get(key: RecordKey) { return this.records.get(recordKey({ ...key, kind: modelRecordKind(key.kind) })) }
  getDetail(key: RecordKey) { return this.get(key)?.loadedDetail?.value }
  releaseSummaries(keys: RecordKey[]) {
    if (this.closed) return
    let released = false
    for (const key of keys) {
      const kind = modelRecordKind(key.kind), token = recordKey({ ...key, kind })
      const record = this.records.get(token)
      if (!record || this.pinned.has(token) || record.loadedDetail || !['summary', 'omitted'].includes(record.completeness)) continue
      this.summaryBytes -= this.sizes.get(token) ?? 0
      this.sizes.delete(token)
      this.records.delete(token)
      this.changed.set(kind, this.revision + 1)
      released = true
    }
    if (released) { this.revision++; for (const listener of this.listeners) listener() }
  }
  retainDetail(key: RecordKey) {
    const token = recordKey({ ...key, kind: modelRecordKind(key.kind) })
    this.pinned.set(token, (this.pinned.get(token) ?? 0) + 1)
    return () => {
      const count = (this.pinned.get(token) ?? 1) - 1
      if (count) this.pinned.set(token, count)
      else this.pinned.delete(token)
    }
  }

  async readDetail(key: RecordKey, signal?: AbortSignal, preserveSummary = false): Promise<SyncRecord | undefined> {
    this.assertActive()
    key = { ...key, kind: modelRecordKind(key.kind) }
    const token = recordKey(key)
    const previous = this.records.get(token)
    if (previous?.completeness === 'deleted') return undefined
    if (previous?.completeness === 'detail') {
      const bytes = this.details.get(token)!
      this.details.delete(token); this.details.set(token, bytes)
      return previous
    }
    let record: SyncRecord | undefined
    try { record = await this.loader.load(key, signal) }
    catch (error) { this.failRequest(error) }
    this.assertActive()
    if (!record) return undefined
    if (preserveSummary && previous?.completeness === 'summary' && record.completeness === 'omitted') return record
    this.install([record])
    return this.records.get(token)
  }

  async readWindow(query: SyncPageRequest) {
    this.assertActive()
    query = { ...query, kind: modelRecordKind(query.kind) }
    const pending = this.windows.begin(query)
    try {
      const page = await this.transport.page(query, this.controller.signal)
      this.checkEpoch(page.authzEpoch)
      if (this.windows.get(query) !== pending) throw new DOMException('Window superseded', 'AbortError')
      // Validate the continuation before installing any rows from an incompatible revision.
      this.windows.apply(query, page)
      this.install(page.items)
      return this.windows.get(query)!
    } catch (error) { if (this.windows.get(query) === pending) this.windows.fail(query, error); this.failRequest(error) }
  }

  async loadDocument(slug: string, signal?: AbortSignal): Promise<FlowDocument> {
    const known = [...this.records.values()].find(item => item.key.kind === 'document' && (item.key.id === slug || item.value?.slugId === slug))
    // The existing endpoint resolves earlier slugs. Batch reads then supply the authoritative record version.
    const activeSignal = signal ? AbortSignal.any([signal, this.controller.signal]) : this.controller.signal
    const resolved = known ? undefined : await getDocument(slug, activeSignal, this.manifest.core.workspace.urlKey)
    this.assertActive()
    if (resolved?.deletedAt) throw new SyncResetRequired('Deleted document requires the legacy trash adapter')
    const key = known?.key ?? { kind: 'document', id: resolved!.id }
    const release = this.retainDetail(key)
    activeSignal.addEventListener('abort', release, { once: true })
    let record: SyncRecord | undefined
    try { record = await this.readDetail(key, activeSignal) }
    catch (error) { activeSignal.removeEventListener('abort', release); release(); throw error }
    if (!record || record.completeness === 'deleted') throw new ApiError('Document not found', 404)
    if (record.completeness !== 'detail') throw new SyncResetRequired('Document detail requires the legacy adapter')
    if (record.value?.deletedAt) throw new SyncResetRequired('Deleted document requires the legacy trash adapter')
    return record.value as unknown as FlowDocument
  }

  catchUp(): Promise<void> {
    if (this.catchingUp) return this.catchingUp
    this.catchingUp = this.drainChanges().catch(error => this.failRequest(error)).finally(() => { this.catchingUp = undefined })
    return this.catchingUp
  }

  /** Live frames reuse the changes HTTP shape; legacy business signals still trigger bounded catch-up. */
  applyFrame(payload: unknown): boolean {
    if (!payload || typeof payload !== 'object') return false
    const frame = payload as { schema?: number; authzEpoch?: string; transaction?: import('@/types/flow').SyncTransaction }
    if (frame.schema === 2 && typeof frame.authzEpoch === 'string' && frame.transaction && typeof frame.transaction.seq === 'string' && typeof frame.transaction.txId === 'string' && Array.isArray(frame.transaction.upserts) && Array.isArray(frame.transaction.removals) && Array.isArray(frame.transaction.invalidations)) {
      if (compareSyncVersions(frame.transaction.seq, this.cursor) <= 0) return true
      const page = modelChanges({ next: frame.transaction.seq, head: frame.transaction.seq, authzEpoch: frame.authzEpoch, transactions: [frame.transaction], hasMore: false, resyncRequired: false })
      if (this.p19Windows && scaleP19NeedsCore(page)) return false
      try { this.applyChanges(page) }
      catch (error) { this.failRequest(error) }
      return true
    }
    const page = payload as SyncChanges
    if (!Array.isArray(page.transactions) || typeof page.next !== 'string' || typeof page.head !== 'string' || typeof page.authzEpoch !== 'string' || typeof page.hasMore !== 'boolean' || typeof page.resyncRequired !== 'boolean') return false
    if (this.p19Windows && scaleP19NeedsCore(modelChanges(page))) return false
    this.applyChanges(modelChanges(page))
    return true
  }

  applyChanges(page: SyncChanges) {
    this.checkEpoch(page.authzEpoch)
    if (page.resyncRequired) { this.requestReset(); throw new SyncResetRequired('Sync baseline expired') }
    if (compareSyncVersions(page.next, this.cursor) < 0 || compareSyncVersions(page.next, page.head) > 0 || page.hasMore && page.next === this.cursor) throw new Error('Invalid sync cursor')
    if (this.p19Windows) {
      if (scaleP19NeedsCore(page) && !this.pendingCore) { this.requestReset(); throw new SyncResetRequired('Core refresh required') }
      page = scaleP19WindowChanges(page)
    }
    const migratedKinds = this.surface === 'initiatives-page' || this.surface === 'projects-page' && this.manifest.capabilities.includes('query-windows-v1') ? ['issue', 'document', 'project', 'initiative'] : this.surface === 'projects-page' ? ['issue', 'document', 'project'] : this.surface === 'people-directory' ? ['issue', 'document', 'user'] : ['issue', 'document']
    if (this.surface && page.transactions.some(transaction => [...transaction.upserts, ...transaction.removals].some(item => !migratedKinds.includes(modelRecordKind(item.key.kind))) || transaction.invalidations.some(kind => !migratedKinds.includes(modelRecordKind(kind))))) {
      this.requireLegacy('Change collection requires a legacy surface adapter')
    }
    const rows: SyncRecord[] = []
    const invalidations: string[] = []
    for (const transaction of page.transactions) {
      if (compareSyncVersions(transaction.seq, page.next) > 0) throw new Error('Transaction exceeds scanned cursor')
      rows.push(...transaction.upserts, ...transaction.removals.map(item => ({ ...item, completeness: 'deleted' as const })))
      invalidations.push(...transaction.invalidations)
    }
    const directoryKind = this.surface === 'document-directory' ? 'document' : this.surface === 'people-directory' ? 'user' : undefined
    if (directoryKind && rows.some(row => modelRecordKind(row.key.kind) === directoryKind)) invalidations.push(directoryKind)
    if (this.pendingCore) {
      const next = this.pendingCore
      this.pendingCore = undefined
      for (const [kind, field] of Object.entries(syncCollections)) if (JSON.stringify(this.manifest.core[field]) !== JSON.stringify(next.core[field])) this.changed.set(kind, this.revision + 1)
      this.manifest.core = next.core
      this.coreRevision = this.revision + 1
      this.collections = { ...this.collections, ...next.collections }
    }
    this.install(rows, invalidations, page.next)
    if (compareSyncVersions(page.head, this.targetHead) > 0) this.targetHead = page.head
  }

  private async drainChanges() {
    this.assertActive()
    for (let pages = 0; ; pages++) {
      if (pages >= 100) throw new SyncResetRequired('Sync catch-up exceeds page budget')
      const page = await this.transport.changes(this.cursor, this.controller.signal)
      this.checkEpoch(page.authzEpoch)
      if (this.p19Windows && scaleP19NeedsCore(page)) {
        const manifest = await fetchSyncManifest(this.manifest.core.workspace.urlKey, this.controller.signal, undefined, this.surface as 'projects-page' | 'initiatives-page')
        this.checkEpoch(manifest.authzEpoch)
        if (!scaleP19ManifestMatches(this.manifest, manifest) || !scaleP19WindowCoreSupported(manifest) || !retainScaleP19Core(this, manifest.core)) this.requireLegacy('Core refresh unavailable')
        if (compareSyncVersions(page.next, this.cursor) < 0) continue
        this.pendingCore = manifest
      }
      this.applyChanges(page)
      if (!page.hasMore) return
    }
  }

  /** Validate an entire page before publishing it, so no observer sees half a transaction. */
  install(rows: SyncRecord[], invalidations: string[] = [], cursor = this.cursor) {
    this.assertActive()
    rows = rows.map(row => ({ ...row, key: { ...row.key, kind: modelRecordKind(row.key.kind) } }))
    invalidations = invalidations.map(modelRecordKind)
    for (const row of rows) {
      compareSyncVersions(row.version, row.version)
      if (!Object.hasOwn(syncCollections, row.key.kind)) this.requireLegacy('Record collection requires a legacy adapter')
      if (!row.key.id || !['summary', 'detail', 'stale', 'omitted', 'deleted'].includes(row.completeness) || !['deleted', 'omitted'].includes(row.completeness) && (!row.value || row.value.id !== row.key.id)) throw new SyncResetRequired('Unsupported sync record')
    }
    for (const value of invalidations) if (!Object.hasOwn(syncCollections, value)) this.requireLegacy('Invalidation requires a legacy adapter')
    this.revision++
    for (const incoming of rows) {
      const token = recordKey(incoming.key)
      const previous = this.records.get(token)
      const order = previous ? compareSyncVersions(incoming.version, previous.version) : 1
      const refreshDetail = incoming.detailStale && incoming.completeness === 'summary' && previous?.completeness === 'detail'
      if (order < 0 || order === 0 && !refreshDetail && (previous!.completeness === 'deleted' || previous!.completeness === incoming.completeness || incoming.completeness !== 'deleted' && (previous!.completeness === 'detail' && incoming.completeness !== 'detail' || previous!.completeness === 'stale' && incoming.completeness === 'summary'))) continue
      // A newer summary keeps omitted fields, but the retained body is explicitly stale.
      const next: CachedRecord = { ...incoming, value: incoming.completeness === 'deleted' ? undefined : incoming.completeness === 'detail' ? { ...incoming.value } : { ...previous?.value, ...incoming.value }, completeness: previous && ['detail', 'stale'].includes(previous.completeness) && incoming.completeness === 'summary' && (order > 0 || incoming.detailStale) ? 'stale' as const : incoming.completeness }
      next.appliedRevision = this.revision
      next.loadedDetail = next.completeness === 'detail' ? { version: next.version, value: next.value! } : ['deleted', 'omitted'].includes(next.completeness) ? undefined : previous?.loadedDetail
      this.summaryBytes -= this.sizes.get(token) ?? 0
      this.detailBytes -= this.details.get(token) ?? 0
      this.details.delete(token)
      this.records.set(token, next)
      const size = cacheStringBytes(JSON.stringify(next))
      this.sizes.set(token, size)
      this.summaryBytes += size
      if (next.completeness === 'detail' || next.completeness === 'stale') { this.details.set(token, size); this.detailBytes += size }
      this.changed.set(incoming.key.kind, this.revision)
    }
    for (const kind of invalidations) {
      this.windows.invalidate(kind)
      this.collections[kind] = { ...this.collections[kind], complete: false }
      this.changed.set(kind, this.revision)
      for (const [key, record] of this.records) if (record.key.kind === kind && record.completeness !== 'deleted') this.records.set(key, { ...record, completeness: 'stale', appliedRevision: this.revision })
    }
    // Details are evicted to omitted records; a mounted detail can immediately load again.
    let cachedDetailBytes = [...this.details].reduce((sum, [key, bytes]) => sum + (this.pinned.has(key) ? 0 : bytes), 0)
    while (cachedDetailBytes > SYNC_DETAIL_MAX_BYTES) {
      const candidate = [...this.details].find(([token]) => !this.pinned.has(token))
      if (!candidate) break
      const [token, bytes] = candidate
      const previous = this.records.get(token)!
      this.details.delete(token)
      this.detailBytes -= bytes
      cachedDetailBytes -= bytes
      this.summaryBytes -= this.sizes.get(token) ?? 0
      let next: CachedRecord = { key: previous.key, version: previous.version, completeness: 'omitted' }
      if (this.surface === 'document-directory' && previous.key.kind === 'document' && previous.value) {
        const { content: _content, contentState: _state, contentData: _data, revisions: _revisions, ...value } = previous.value
        next = { ...next, completeness: 'summary', value }
      }
      this.records.set(token, next)
      const size = cacheStringBytes(JSON.stringify(next))
      this.sizes.set(token, size)
      this.summaryBytes += size
      this.changed.set(previous.key.kind, this.revision)
    }
    if (this.summaryBytes - this.detailBytes > SYNC_SUMMARY_MAX_BYTES || this.records.size > 20_000) { this.requestReset(); throw new SyncResetRequired('Sync record cache exceeds budget') }
    this.cursor = cursor
    for (const listener of this.listeners) listener()
  }

  adapt(current?: BootstrapData | null): BootstrapData {
    this.assertActive()
    const same = current && syncClientFor(current) === this
    const data = same ? { ...current } : {
      ...Object.fromEntries(arrayFields.map(key => [key, []])),
      ...Object.fromEntries(objectFields.map(key => [key, {}])),
      ...this.manifest.core,
      resourceDetailsOmitted: false,
      issueCollectionPaged: true,
    } as BootstrapData
    if (same && this.coreRevision > (current.sync?.revision ?? -1)) for (const [field, value] of Object.entries(this.manifest.core)) if (!(Object.values(syncCollections) as string[]).includes(field)) Object.assign(data, { [field]: value })
    for (const [kind, field] of Object.entries(syncCollections)) {
      if (same && (this.changed.get(kind) ?? 0) <= (current.sync?.revision ?? -1)) continue
      const core = this.manifest.core[field] as { id: string }[] | undefined
      const valuesById = new Map((core ?? []).map(item => [item.id, item]))
      for (const item of this.records.values()) {
        if (item.key.kind !== kind) continue
        if (kind === 'issue' && (item.appliedRevision ?? 0) > (same ? current.sync?.revision ?? -1 : -1)) {
          if (Object.hasOwn(data.issueHistoryCursors ?? {}, item.key.id)) {
            data.issueHistoryCursors = { ...data.issueHistoryCursors }; delete data.issueHistoryCursors[item.key.id]
          }
          if (item.completeness === 'deleted') {
            data.comments = { ...data.comments }; delete data.comments[item.key.id]
            data.activities = { ...data.activities }; delete data.activities[item.key.id]
          }
        }
        if (!item.value || ['deleted', 'omitted'].includes(item.completeness)) { valuesById.delete(item.key.id); continue }
        const previousDocument = kind === 'document' && item.completeness === 'stale' && same ? current.documents.find(document => document.id === item.key.id) : undefined
        // Keep the mounted editor's full model while its newer body loads, including any acknowledged local save.
        const documentDetail = item.completeness === 'stale' && kind === 'document' ? previousDocument && Object.hasOwn(previousDocument, 'content') ? previousDocument : item.loadedDetail?.value : undefined
        const value = documentDetail ?? (kind === 'issue' && item.completeness !== 'detail' ? { ...item.value, isSummary: item.completeness === 'summary', needsDetailRefresh: true } : item.value)
        const header = this.p19Windows && kind === 'initiative' ? core?.find(header => header.id === item.key.id) : undefined
        valuesById.set(item.key.id, (header ? { ...value, ...header } : value) as { id: string })
      }
      const values = [...valuesById.values()]
      // Complete core arrays retain their server projection until a record/window for that collection arrives.
      if (values.length || this.changed.has(kind) || !Array.isArray(this.manifest.core[field])) Object.assign(data, { [field]: values })
    }
    const sync: WorkspaceSyncState = { schema: 2, surface: this.surface, authzEpoch: this.manifest.authzEpoch, head: this.cursor, capabilities: this.manifest.capabilities, collections: { ...this.collections }, revision: this.revision }
    clients.set(sync, this)
    data.sync = sync
    return data
  }

  dispose() {
    if (this.closed) return
    this.closed = true
    releaseScaleP19Bytes(this)
    this.controller.abort()
    this.loader.dispose()
    this.windows.clear()
    this.records.clear()
    this.pinned.clear()
    this.details.clear()
    this.sizes.clear()
  }

  requestReset(legacy = false) {
    this.legacyRequired = legacy
    this.dispose()
    for (const listener of [...this.listeners]) listener()
  }

  private assertActive() { if (this.closed) throw new SyncResetRequired('Sync context expired') }
  private get p19Windows() { return (this.surface === 'projects-page' || this.surface === 'initiatives-page') && this.manifest.capabilities.includes('query-windows-v1') }
  private requireLegacy(message: string): never { this.requestReset(true); throw new SyncProtocolUnavailable(message) }
  private failRequest(error: unknown): never {
    if (error instanceof SyncWireVersionError) this.requireLegacy(error.message)
    if (error instanceof ApiError && [404, 405, 501].includes(error.status)) {
      this.requestReset(true)
      throw new SyncProtocolUnavailable('Sync endpoint unavailable')
    }
    if (error instanceof ApiError && [401, 403].includes(error.status)) {
      this.requestReset()
      throw new SyncResetRequired('Sync access revoked')
    }
    throw error
  }
  private checkEpoch(epoch: string) {
    this.assertActive()
    if (epoch !== this.manifest.authzEpoch) { this.requestReset(); throw new SyncResetRequired('Authorization epoch changed') }
  }
}

/** Unconverted surfaces retain their complete legacy bootstrap. */
export async function fetchSurfaceBootstrap(workspaceKey: string, surface: SyncSurface | undefined, legacy: () => Promise<BootstrapData>, signal?: AbortSignal, quiet?: boolean): Promise<BootstrapData> {
  if (!syncV2Enabled() || !surface) return legacy()
  if (surface === 'projects-page' && import.meta.env.VITE_PROJECT_QUERY_WINDOWS !== 'true' || surface === 'initiatives-page' && import.meta.env.VITE_INITIATIVE_QUERY_WINDOWS !== 'true') return legacy()
  let manifest: SyncManifest
  try { manifest = await fetchSyncManifest(workspaceKey, signal, quiet, surface === 'projects-page' || surface === 'initiatives-page' ? surface : undefined) }
  catch (error) {
    if (error instanceof ApiError && [404, 405, 501].includes(error.status)) return legacy()
    throw error
  }
  if (manifest.schema !== 2 || !manifest.capabilities?.includes('sync-v2')) return legacy()
  if (!scaleP19WindowCoreSupported(manifest) && (surface === 'initiatives-page' && !scaleP19InitiativeCatalogSupported(manifest.core, manifest.collections) || surface === 'projects-page' && !scaleP19CoreSupported(manifest.core, manifest.collections))) return legacy()
  const client = new SyncClient(manifest, workspaceKey)
  client.surface = surface
  if ((surface === 'projects-page' || surface === 'initiatives-page') && !retainScaleP19Core(client, manifest.core)) { client.dispose(); return legacy() }
  const abort = () => client.dispose()
  signal?.addEventListener('abort', abort, { once: true })
  try {
    if (signal?.aborted) throw new DOMException('Aborted', 'AbortError')
    // H0 stays fixed while pages load; catch-up starts at H0, never a later observed head.
    await client.catchUp()
    return client.adapt()
  } catch (error) {
    client.dispose()
    if (error instanceof SyncProtocolUnavailable && !signal?.aborted) return legacy()
    throw error
  }
  finally { signal?.removeEventListener('abort', abort) }
}

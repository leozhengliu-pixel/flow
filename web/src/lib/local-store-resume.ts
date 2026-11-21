import type { AccountBootstrap, AuthSession, BootstrapData, SyncChanges } from '@/types/flow'
import type { AppRoute } from './app-routes'
import { ApiError, requestRetryingBusy } from './api-client'
import { localSnapshotBuild } from './local-snapshot'
import { localStoreBootstrap } from './local-store-bootstrap'
import { clearUnlistedLocalStores, isLocalStoreQuotaError, LocalStoreDB, localStoreGeneration, retainedLocalAuthority, retainedLocalHead, retainedLocalWorkspaceId, retainLocalStoreAuthority, LOCAL_STORE_BUDGET, type LocalCollections, type LocalMeta, type LocalResume } from './local-store-db'
import { readRememberedWorkspace } from './navigation-cache'
import {
  LOCAL_STORE_HEADER, LOCAL_STORE_ISSUE_CAPABILITY, LocalStoreRefreshRequired, LocalStoreReset, decimal,
  localKinds, localRowBytes, localRowFingerprint, localStoreAuthority, localStoreEnabled, planLocalChanges,
  projectLocalCollection, projectLocalIssueRecord, type LocalChange, type LocalRow, type LocalStoreAuthority,
} from './local-store-model'
import { modelChanges, modelRecordPage, type SyncWireRecord } from './scale-p13-wire'

interface Scope { viewerId: string; workspaceId: string; workspaceKey: string }
type Checkpoint = Pick<LocalResume, 'revision' | 'eventCursor'>
type IssueManifest = LocalStoreAuthority & Checkpoint & { issueCount?: number; outbox: boolean }
type JournalPage = SyncChanges & Checkpoint
const confirmed = new WeakMap<BootstrapData, { checkpoint: LocalMeta; seeded: boolean }>()
export const localStoreLoadedWorkspace = (data: BootstrapData) => confirmed.get(data)

export function localStoreRoute(route: AppRoute) {
  if (!localStoreEnabled() || import.meta.env.VITE_PAGED_ISSUES === 'true') return false
  if (route.kind === 'my-issues') return route.view !== 'activity' && route.view !== 'shared'
  return ['workspace-issues', 'team-issues', 'team-board'].includes(route.kind)
}

const directoryFields = [
  'workspace', 'viewer', 'viewerRole', 'chatPlatform', 'trashRetentionDays', 'workspaceSettings', 'userSettings',
  'users', 'members', 'teamMembers', 'teams', 'teamSettings', 'teamParents', 'states', 'labels', 'labelGroups',
  'projects', 'projectStatuses', 'projectDisplayDefault', 'initiatives', 'cycles', 'cycleSettings', 'customers',
  'customerStatuses', 'customerTiers', 'savedViews', 'favorites', 'favoriteFolders', 'subscriptions',
  'threadSubscriptions', 'teamResourceSections', 'teamPinnedResources', 'releases', 'releasePipelines',
  'issueTemplates', 'customEmojis', 'inboxUnreadCount', 'reviewCount',
] as const satisfies readonly (keyof BootstrapData)[]

function listDirectory(data: BootstrapData) {
  // Keep the shapes of empty collections that the existing list UI expects.
  const directory = Object.fromEntries(Object.entries(data).map(([key, value]) => [key,
    Array.isArray(value) ? [] : value && typeof value === 'object' ? {} : undefined,
  ])) as unknown as BootstrapData
  Object.assign(directory, Object.fromEntries(directoryFields.map(key => [key, data[key]])))
  directory.documents = (data.documents ?? []).map(document => ({ ...document, content: '', contentState: undefined, contentData: undefined }))
  directory.issueCollectionPaged = false
  return directory
}

function endpoint(scope: Scope, route: string, args: Record<string, string>, signal?: AbortSignal) {
  const params = new URLSearchParams({ surface: 'local-store', mode: 'issues', workspaceId: scope.workspaceId, ...args })
  return {
    url: `/api/sync/${route}?${params}`,
    init: { headers: { 'X-Workspace-Key': scope.workspaceKey, [LOCAL_STORE_HEADER]: scope.viewerId }, signal, quiet: true, cache: 'no-store' as const },
  }
}

function readCheckpoint(value: Partial<Checkpoint>) {
  if (typeof value.revision !== 'string' || !/^\d+:\d+$/.test(value.revision) || !decimal(value.eventCursor)) {
    throw new LocalStoreReset('Missing issue checkpoint')
  }
  return { revision: value.revision, eventCursor: value.eventCursor }
}

export async function readLocalIssueManifest(scope: Scope, counts: boolean, signal?: AbortSignal): Promise<IssueManifest> {
  const { url, init } = endpoint(scope, 'manifest', counts ? { counts: '1' } : {}, signal)
  const manifest = await requestRetryingBusy<Partial<Checkpoint> & { schema?: number; capabilities?: string[]; issueCount?: number }>(url, init)
  const authority = localStoreAuthority(manifest)
  if (manifest.schema !== 2 || !manifest.capabilities?.includes(LOCAL_STORE_ISSUE_CAPABILITY) || !authority ||
      authority.workspaceId !== scope.workspaceId || authority.viewerId !== scope.viewerId ||
      counts && (!Number.isSafeInteger(manifest.issueCount) || manifest.issueCount! < 0)) {
    throw new LocalStoreReset('Issue manifest unavailable')
  }
  return { ...authority, ...readCheckpoint(manifest), issueCount: manifest.issueCount, outbox: manifest.capabilities.includes('local-store-outbox-v1') }
}

export async function readLocalIssueChanges(scope: Scope, authority: LocalStoreAuthority, signal?: AbortSignal): Promise<JournalPage> {
  const { url, init } = endpoint(scope, 'changes', { after: authority.head, authzEpoch: authority.authzEpoch }, signal)
  const wire = await requestRetryingBusy<SyncChanges & Checkpoint>(url, init)
  if (!Array.isArray(wire?.transactions)) throw new LocalStoreReset('Invalid issue changes')
  if (wire.resyncRequired || wire.authzEpoch !== authority.authzEpoch) throw new LocalStoreRefreshRequired('Issue authority or retention changed')
  return { ...modelChanges(wire), ...readCheckpoint(wire) }
}

export function planIssueChanges(page: JournalPage, meta: LocalMeta) {
  const writes = planLocalChanges(page, meta.head, meta.authzEpoch, true)
  if (!meta.resume || page.revision.split(':')[0] !== meta.resume.revision.split(':')[0] || writes.some(change => change.kind !== 'issue')) {
    throw new LocalStoreRefreshRequired('Workspace directory changed')
  }
  if (page.next === meta.head && page.revision !== meta.resume.revision) throw new LocalStoreRefreshRequired('Issue revision changed outside the journal')
  return writes
}

async function loadIssues(scope: Scope, manifest: IssueManifest, signal?: AbortSignal) {
  const rows = new Map<string, LocalRow>()
  let bytes = 0
  let cursor = ''
  for (let pageNumber = 0; pageNumber < 10_000; pageNumber++) {
    const { url, init } = endpoint(scope, 'records', {
      kind: 'issues', authzEpoch: manifest.authzEpoch, revision: manifest.revision, limit: '100', ...(cursor ? { cursor } : {}),
    }, signal)
    const wire = await requestRetryingBusy<{
      items: SyncWireRecord[]
      authzEpoch: string; revision: string; hasMore: boolean; next: string
    }>(url, init)
    if (!Array.isArray(wire.items) || wire.authzEpoch !== manifest.authzEpoch || wire.revision !== manifest.revision) throw new LocalStoreReset('Issue bootstrap changed while paging')
    const page = modelRecordPage(wire)
    for (const { key, value, version, completeness } of page.items) {
      if (key.kind !== 'issue' || rows.has(key.id) || !value || completeness !== 'summary') throw new LocalStoreReset('Invalid issue bootstrap page')
      const row = projectLocalIssueRecord(key.id, value, manifest.head, version)
      rows.set(key.id, row)
      bytes += localRowBytes(row)
    }
    if (bytes > LOCAL_STORE_BUDGET) throw new LocalStoreReset('Issue bootstrap exceeds budget')
    if (!page.hasMore) return rows
    if (!page.next || page.next === cursor) throw new LocalStoreReset('Issue bootstrap did not advance')
    cursor = page.next
  }
  throw new LocalStoreReset('Issue bootstrap exceeded page limit')
}

async function renderWorkspace(db: LocalStoreDB, checkpoint: LocalMeta, seeded = false) {
  return workspaceView(db, checkpoint, await db.readIssueWorkspace(checkpoint), seeded)
}

function workspaceView(db: LocalStoreDB, checkpoint: LocalMeta, view: Awaited<ReturnType<LocalStoreDB['readIssueWorkspace']>>, seeded = false) {
  if (!view || !db.valid) return undefined
  const data = { ...view.directory, issues: view.issues }
  confirmed.set(data, { checkpoint, seeded })
  return data
}

async function loadDirectory(scope: Scope, manifest: IssueManifest, signal?: AbortSignal) {
  const { fetchBootstrap } = await import('./api')
  const data = await fetchBootstrap(scope.workspaceKey, undefined, signal, true)
  const authority = localStoreBootstrap(data)?.authority
  if (!authority || authority.viewerId !== scope.viewerId || authority.workspaceId !== scope.workspaceId || authority.authzEpoch !== manifest.authzEpoch) {
    throw new LocalStoreRefreshRequired('Directory authority changed')
  }
  return listDirectory(data)
}

async function seedWorkspace(scope: Scope, started: number, signal?: AbortSignal) {
  const manifest = await readLocalIssueManifest(scope, false, signal)
  signal?.throwIfAborted()
  const current = await retainLocalStoreAuthority(manifest, scope.workspaceKey, started)
  if (current === undefined) return undefined
  const [directory, issues] = await Promise.all([loadDirectory(scope, manifest, signal), loadIssues(scope, manifest, signal)])
  const page = await readLocalIssueChanges(scope, manifest, signal)
  if (page.next !== manifest.head || page.revision !== manifest.revision) throw new LocalStoreRefreshRequired('Workspace changed during bootstrap')
  const db = await LocalStoreDB.open(manifest, scope.workspaceKey, current)
  if (!db) return undefined
  try {
    const collections: LocalCollections = new Map(localKinds.map(kind => [kind,
      kind === 'issue' ? issues : projectLocalCollection(directory, kind, manifest.head),
    ]))
    let bytes = JSON.stringify(directory).length * 2
    let issueFingerprint = 0
    for (const [kind, rows] of collections) for (const row of rows.values()) {
      if (kind === 'issue') {
        const fingerprint = localRowFingerprint(row)
        issueFingerprint ^= fingerprint.hash
        bytes += fingerprint.bytes
      } else bytes += localRowBytes(row)
    }
    if (bytes > LOCAL_STORE_BUDGET) return undefined
    const now = Date.now()
    const previous = await db.readMeta()
    const metadata = {
      build: localSnapshotBuild(), baseline: crypto.randomUUID(), workspaceKey: scope.workspaceKey,
      bootstrapAt: now, lastAccessAt: now, fullCollections: ['issue' as const], bytes,
      resume: { schema: 2 as const, revision: page.revision, eventCursor: page.eventCursor, issueCount: issues.size, issueFingerprint },
    }
    const checkpoint = await db.writeWithinBudget(bytes - (previous?.bytes ?? 0), budget => {
      signal?.throwIfAborted()
      return db.seed(manifest, metadata, collections, previous, directory, budget)
    })
    issues.clear()
    if (checkpoint) {
      db.rememberHead(checkpoint)
      return await renderWorkspace(db, checkpoint, true)
    }
  } finally { db.close() }
}

export async function catchUpLocalIssues(db: LocalStoreDB, meta: LocalMeta, signal?: AbortSignal, budget = LOCAL_STORE_BUDGET, firstPage?: Promise<JournalPage | undefined>) {
  const writes = new Map<string, LocalChange>()
  let checkpoint = meta
  for (let pageNumber = 0; pageNumber < 100; pageNumber++) {
    const prefetched = pageNumber === 0 ? await firstPage : undefined
    const page = prefetched ?? await readLocalIssueChanges(meta, checkpoint, signal)
    const delta = planIssueChanges(page, checkpoint)
    for (const change of delta) writes.set(change.id, change)
    const stamp = { revision: page.revision, eventCursor: page.eventCursor }
    checkpoint = { ...checkpoint, head: page.next, resume: { ...meta.resume!, ...stamp } }
    if (page.hasMore) continue
    signal?.throwIfAborted()
    const changes = [...writes.values()]
    if (!changes.length && page.next === meta.head && page.revision === meta.resume?.revision && page.eventCursor === meta.resume.eventCursor) {
      db.rememberHead(meta)
      return db.valid ? { checkpoint: meta, changes } : undefined
    }
    const growth = changes.reduce((sum, change) => sum + (change.row ? localRowBytes(change.row) : 0), 0)
    let started = performance.now()
    let committed: LocalMeta | undefined
    try {
      committed = await db.writeWithinBudget(growth, available => {
        signal?.throwIfAborted()
        started = performance.now()
        return db.applyIssues(meta, page.next, stamp, changes, available)
      }, budget)
    } catch (error) {
      if (!isLocalStoreQuotaError(error)) throw error
      throw new LocalStoreReset('Unable to write the local issue store after eviction')
    }
    if (committed) db.rememberHead(committed)
    if (committed && changes.length) performance.measure('flow:local-store:delta-write', { start: started })
    return committed ? { checkpoint: committed, changes } : undefined
  }
  throw new LocalStoreRefreshRequired('Issue catch-up exceeded page limit')
}

async function resumeWorkspace(scope: Scope, signal?: AbortSignal) {
  // The localStorage hint may lag a commit. Only use its response when the
  // real IndexedDB checkpoint independently agrees with the requested one.
  const hint = retainedLocalHead(scope.viewerId, scope.workspaceId, scope.workspaceKey)
  const firstPage = hint && readLocalIssueChanges(scope, hint, signal).catch(error => ({ error }))
  const db = await LocalStoreDB.openRetained(scope.viewerId, scope.workspaceId, scope.workspaceKey, localStoreGeneration())
  if (!db) return undefined
  let baseline: LocalMeta | undefined
  try {
    for (let attempt = 0; attempt < 3 && db.valid; attempt++) {
      signal?.throwIfAborted()
      const startup = await db.readIssueStartup()
      baseline = startup?.meta
      if (baseline?.resume?.schema !== 2) break
      const reused = attempt === 0 && hint?.head === baseline.head && hint.authzEpoch === baseline.authzEpoch ? firstPage?.then(page => {
        if ('error' in page) throw page.error
        return page
      }) : undefined
      const caughtUp = await catchUpLocalIssues(db, baseline, signal, LOCAL_STORE_BUDGET, reused)
      const restored = caughtUp && (caughtUp.checkpoint === baseline && startup?.view
        ? workspaceView(db, baseline, startup.view) : await renderWorkspace(db, caughtUp.checkpoint))
      if (restored) return restored
    }
  } catch (error) {
    if (signal?.aborted || error instanceof ApiError && [401, 404].includes(error.status)) throw error
    if (error instanceof LocalStoreReset || isLocalStoreQuotaError(error)) {
      // A new manifest decides whether to replace the baseline or revoke the
      // database. The outbox belongs to the authorization, not the baseline.
      if (baseline && db.valid) await db.invalidateReadModel(baseline)
      performance.mark('flow:local-store:resync')
    } else if (error instanceof ApiError && [400, 403].includes(error.status)) {
      await db.discard()
      performance.mark('flow:local-store:resync')
    }
  } finally { db.close() }
}

let startupResume: { scope: Scope; valid: () => boolean; request: Promise<BootstrapData | undefined> } | undefined

export function preloadLocalWorkspace(workspaceKey: string) {
  if (typeof indexedDB === 'undefined') return
  const remembered = readRememberedWorkspace()
  if (remembered?.workspaceKey !== workspaceKey) return
  const workspaceId = retainedLocalWorkspaceId(remembered.viewerId, workspaceKey)
  if (!workspaceId) return
  // The journal authenticates the session and epoch. The account still has
  // to confirm the viewer and workspace before the app can use the view.
  const scope = { viewerId: remembered.viewerId, workspaceId, workspaceKey }
  const index = retainedLocalAuthority(scope.viewerId, workspaceId, workspaceKey)
  if (!index) return
  const generation = localStoreGeneration()
  const valid = () => generation === localStoreGeneration() &&
    retainedLocalAuthority(scope.viewerId, workspaceId, workspaceKey)?.token === index.token
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 5_000)
  const request = resumeWorkspace(scope, controller.signal).finally(() => clearTimeout(timer))
  void request.catch(() => {})
  startupResume = { scope, valid, request }
  return true
}

export async function authorizedPreload(account: AccountBootstrap, workspaceKey: string) {
  const early = startupResume
  if (!early || early.scope.viewerId !== account.viewer.id || early.scope.workspaceKey !== workspaceKey ||
      !account.workspaces.some(({ workspace }) => workspace.id === early.scope.workspaceId && workspace.urlKey === workspaceKey)) return undefined
  const result = await early.request.catch(() => undefined)
  const checkpoint = result && localStoreLoadedWorkspace(result)?.checkpoint
  if (!result || !checkpoint?.resume || !early.valid()) return undefined
  return { data: result, checkpoint, valid: early.valid, consume: () => {
    if (startupResume !== early || !early.valid()) return false
    startupResume = undefined
    performance.mark('flow:local-store:resume')
    return true
  } }
}

export async function prepareLocalStartup(ready: Promise<{ session: AuthSession; account: AccountBootstrap }>, workspaceKey: string) {
  if (!startupResume) return undefined
  const { session, account } = await ready
  await clearUnlistedLocalStores(session.user.id, account.workspaces.map(item => item.workspace))
  if (session.user.id !== account.viewer.id) return undefined
  const resume = await authorizedPreload(account, workspaceKey)
  return resume && { session, account, ...resume }
}

export type LocalStartup = NonNullable<Awaited<ReturnType<typeof prepareLocalStartup>>>

export function localStoreCursor(checkpoint?: LocalMeta) {
  if (!checkpoint?.resume) return undefined
  return { viewerId: checkpoint.viewerId, workspaceKey: checkpoint.workspaceKey, cursor: checkpoint.resume.eventCursor, baseline: checkpoint.baseline }
}

export async function loadLocalWorkspace(account: AccountBootstrap | null, workspaceKey: string, signal?: AbortSignal) {
  const workspace = account?.workspaces.find(item => item.workspace.urlKey === workspaceKey)?.workspace
  if (!account || !workspace || typeof indexedDB === 'undefined') return undefined
  const scope = { viewerId: account.viewer.id, workspaceId: workspace.id, workspaceKey }
  const early = startupResume
  startupResume = undefined
  let restored: BootstrapData | undefined
  try {
    if (early && early.scope.viewerId === scope.viewerId && early.scope.workspaceId === scope.workspaceId && early.scope.workspaceKey === workspaceKey) {
      const result = await early.request
      if (early.valid()) restored = result
    } else restored = await resumeWorkspace(scope, signal)
  } catch (error) {
    if (signal?.aborted || error instanceof ApiError && error.status === 401) throw error
    if (error instanceof ApiError && error.status === 404) return undefined
  }
  signal?.throwIfAborted()
  if (restored) {
    performance.mark('flow:local-store:resume')
    return restored
  }
  for (let attempt = 0; attempt < 3; attempt++) {
    try { return await seedWorkspace(scope, localStoreGeneration(), signal) }
    catch (error) {
      if (signal?.aborted || error instanceof ApiError && error.status === 401) throw error
      // An intervening write can invalidate a page without revoking access.
      if (!(error instanceof LocalStoreRefreshRequired || error instanceof ApiError && error.status === 409)) return undefined
    }
  }
}

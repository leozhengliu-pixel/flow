import type { AccountBootstrap, AuthSession, BootstrapData } from '@/types/flow'
import { readRememberedWorkspace, toNavigationSnapshot } from '@/lib/navigation-cache'
import { registerSessionCacheClearer } from '@/lib/session-cache-registry'
import { boundedBrowserCacheEnabled, NAVIGATION_CACHE_MAX_BYTES } from '@/lib/scale-p08'

/**
 * The local workspace snapshot the app paints from on a repeat visit, before the session, account and
 * workspace bootstrap requests answer. It holds what the last confirmed load showed (the session, the account
 * and the navigation snapshot of the workspace, see toNavigationSnapshot), never anything newer than a bootstrap
 * the server returned to the same viewer.
 *
 * Correctness rules:
 * - A record is keyed by viewer and workspace and carries the snapshot schema and the app build that wrote it.
 *   Any mismatch, an expired session, or an account that no longer lists the workspace ignores (and deletes) it.
 * - A synchronous index in localStorage names the records that may be read. Clearing (logout, account switch,
 *   access loss: every clearNavigationCache) drops the index entry at once, so a record whose asynchronous
 *   IndexedDB delete has not finished can never be painted again.
 * - Records larger than the size cap are not written; without IndexedDB or localStorage nothing is cached and
 *   the app starts the way it does without a snapshot.
 */
export const LOCAL_SNAPSHOT_SCHEMA = 1
export const LOCAL_SNAPSHOT_MAX_BYTES = 8 * 1024 * 1024
/** A confirmed workspace is written again at most this often while it changes; hiding the page flushes it. */
export const LOCAL_SNAPSHOT_SAVE_INTERVAL_MS = 30_000

const databaseName = 'flow-local-snapshot'
const storeName = 'snapshots'
const indexKey = 'flow:local-snapshot'

export interface LocalSnapshot {
  viewerId: string
  workspaceKey: string
  savedAt: number
  session: AuthSession
  account: AccountBootstrap
  data: BootstrapData
}

interface LocalSnapshotRecord {
  key: string
  token: string
  schema: number
  build: string
  viewerId: string
  workspaceKey: string
  savedAt: number
  /** JSON of { session, account, data }: one string to size, store and parse. */
  json: string
}

/**
 * The build that wrote a snapshot: the entry script's path. In a production build its name carries a hash that
 * covers the names of every chunk it imports, so each deploy reads none of the previous build's snapshots.
 */
export function localSnapshotBuild(): string {
  const entry = typeof document === 'undefined' ? undefined : document.querySelector<HTMLScriptElement>('script[type="module"][src]')?.src
  try {
    return new URL(entry || import.meta.url).pathname
  } catch {
    return entry || import.meta.url
  }
}

export function localSnapshotKey(viewerId: string, workspaceKey: string) {
  return `${encodeURIComponent(viewerId)}:${encodeURIComponent(workspaceKey)}`
}

function snapshotCap() {
  return boundedBrowserCacheEnabled() ? NAVIGATION_CACHE_MAX_BYTES : LOCAL_SNAPSHOT_MAX_BYTES
}

function readIndex(): Record<string, string> | undefined {
  try {
    const value = JSON.parse(localStorage.getItem(indexKey) ?? '{}') as unknown
    return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, string> : {}
  } catch {
    return undefined
  }
}

function writeIndex(index: Record<string, string>) {
  try {
    if (Object.keys(index).length) localStorage.setItem(indexKey, JSON.stringify(index))
    else localStorage.removeItem(indexKey)
    return true
  } catch {
    return false
  }
}

let databasePromise: Promise<IDBDatabase | undefined> | undefined

function openDatabase(): Promise<IDBDatabase | undefined> {
  if (databasePromise) return databasePromise
  databasePromise = new Promise(resolve => {
    if (typeof indexedDB === 'undefined') return resolve(undefined)
    let request: IDBOpenDBRequest
    try {
      request = indexedDB.open(databaseName, 1)
    } catch {
      return resolve(undefined)
    }
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(storeName)) request.result.createObjectStore(storeName, { keyPath: 'key' })
    }
    request.onsuccess = () => {
      const database = request.result
      // A later schema (another tab on a newer build) must be able to upgrade: let go of the connection.
      database.onversionchange = () => { database.close(); databasePromise = undefined }
      resolve(database)
    }
    request.onerror = () => resolve(undefined)
    request.onblocked = () => resolve(undefined)
  })
  void databasePromise.then(database => { if (!database) databasePromise = undefined })
  return databasePromise
}

function transact<T>(database: IDBDatabase, mode: IDBTransactionMode, run: (store: IDBObjectStore) => IDBRequest<T> | void): Promise<T | undefined> {
  return new Promise(resolve => {
    let result: T | undefined
    try {
      const transaction = database.transaction(storeName, mode)
      const request = run(transaction.objectStore(storeName))
      if (request) request.onsuccess = () => { result = request.result }
      transaction.oncomplete = () => resolve(result)
      transaction.onerror = transaction.onabort = () => resolve(undefined)
    } catch {
      resolve(undefined)
    }
  })
}

let generation = 0

/** Bumped by every clear: a save that started before it must not land after it. */
export function localSnapshotGeneration() {
  return generation
}

function matches(key: string, viewerId?: string, workspaceKey?: string) {
  const [viewer, workspace] = key.split(':')
  return (!viewerId || viewer === encodeURIComponent(viewerId)) && (!workspaceKey || workspace === encodeURIComponent(workspaceKey))
}

/** Drops the snapshots of a viewer (or one workspace of a viewer, or all). Effective at once; the IndexedDB delete follows. */
export function clearLocalSnapshots(viewerId?: string, workspaceKey?: string): Promise<void> {
  generation++
  const index = readIndex()
  if (index) {
    for (const key of Object.keys(index)) if (matches(key, viewerId, workspaceKey)) delete index[key]
    writeIndex(index)
  }
  return openDatabase().then(async database => {
    if (!database) return
    if (!viewerId) {
      await transact(database, 'readwrite', store => store.clear())
      return
    }
    const keys = await transact(database, 'readonly', store => store.getAllKeys()) ?? []
    const doomed = keys.map(String).filter(key => matches(key, viewerId, workspaceKey))
    if (doomed.length) await transact(database, 'readwrite', store => { for (const key of doomed) store.delete(key) })
  })
}

registerSessionCacheClearer((viewerId, workspaceKey) => { void clearLocalSnapshots(viewerId, workspaceKey) })

/** Whether a parsed snapshot may be shown for this viewer and workspace at this time. */
export function localSnapshotUsable(snapshot: Partial<LocalSnapshot> | undefined, viewerId: string, workspaceKey: string, now = Date.now()): snapshot is LocalSnapshot {
  if (!snapshot || !viewerId || !workspaceKey) return false
  const { session, account, data } = snapshot
  const expiresAt = Date.parse(session?.expiresAt ?? '')
  return Boolean(
    snapshot.viewerId === viewerId && snapshot.workspaceKey === workspaceKey
    && session?.user?.id === viewerId && Number.isFinite(expiresAt) && expiresAt > now
    && account?.viewer?.id === viewerId && Array.isArray(account.workspaces) && account.workspaces.some(item => item.workspace?.urlKey === workspaceKey)
    && data?.viewer?.id === viewerId && data.workspace?.urlKey === workspaceKey && Array.isArray(data.issues),
  )
}

export async function readLocalSnapshot(viewerId: string, workspaceKey: string, now = Date.now()): Promise<LocalSnapshot | undefined> {
  if (!viewerId || !workspaceKey) return undefined
  const key = localSnapshotKey(viewerId, workspaceKey)
  const token = readIndex()?.[key]
  if (!token) return undefined
  const database = await openDatabase()
  if (!database) return undefined
  const record = await transact<LocalSnapshotRecord>(database, 'readonly', store => store.get(key))
  // The index may have been cleared while the read was in flight.
  if (!record || readIndex()?.[key] !== token) return undefined
  let snapshot: LocalSnapshot | undefined
  if (record.token === token && record.schema === LOCAL_SNAPSHOT_SCHEMA && record.build === localSnapshotBuild()
    && record.viewerId === viewerId && record.workspaceKey === workspaceKey
    && typeof record.json === 'string' && record.json.length * 2 <= snapshotCap()) {
    try {
      const parsed = JSON.parse(record.json) as Pick<LocalSnapshot, 'session' | 'account' | 'data'>
      snapshot = { ...parsed, viewerId, workspaceKey, savedAt: record.savedAt }
    } catch { snapshot = undefined }
  }
  if (localSnapshotUsable(snapshot, viewerId, workspaceKey, now)) return snapshot
  void clearLocalSnapshots(viewerId, workspaceKey)
  return undefined
}

export type LocalSnapshotSaveResult = 'saved' | 'invalid' | 'too-large' | 'unavailable' | 'superseded'

/** Writes the navigation snapshot of a confirmed workspace load. Callers pass only server-confirmed data. */
export async function saveLocalSnapshot(input: { session: AuthSession; account: AccountBootstrap; data: BootstrapData }, now = Date.now()): Promise<LocalSnapshotSaveResult> {
  const started = generation
  const viewerId = input.data?.viewer?.id
  const workspaceKey = input.data?.workspace?.urlKey
  const candidate = { ...input, viewerId, workspaceKey, savedAt: now } as LocalSnapshot
  if (!localSnapshotUsable(candidate, viewerId, workspaceKey, now) || input.data.sync) return 'invalid'
  let json: string
  try {
    json = JSON.stringify({ session: input.session, account: input.account, data: toNavigationSnapshot(input.data) })
  } catch { return 'invalid' }
  const key = localSnapshotKey(viewerId, workspaceKey)
  const index = readIndex()
  if (!index) return 'unavailable'
  if (json.length * 2 > snapshotCap()) {
    // An older, smaller snapshot would now be out of date: drop it too.
    if (index[key]) await clearLocalSnapshots(viewerId, workspaceKey)
    return 'too-large'
  }
  const database = await openDatabase()
  if (!database) return 'unavailable'
  if (started !== generation) return 'superseded'
  const token = `${now.toString(36)}-${Math.random().toString(36).slice(2)}`
  const record: LocalSnapshotRecord = { key, token, schema: LOCAL_SNAPSHOT_SCHEMA, build: localSnapshotBuild(), viewerId, workspaceKey, savedAt: now, json }
  await transact(database, 'readwrite', store => store.put(record))
  // The index names the record only once it is stored, and never after a clear that ran meanwhile.
  if (started !== generation) {
    await transact(database, 'readwrite', store => store.delete(key))
    return 'superseded'
  }
  const latest = readIndex()
  if (!latest || !writeIndex({ ...latest, [key]: token })) return 'unavailable'
  return 'saved'
}

let startup: LocalSnapshot | undefined

/** The workspace a startup snapshot may be read for: the URL's workspace, or the remembered one on the root route. */
export function startupSnapshotTarget(pathname: string): { viewerId: string; workspaceKey: string; root: boolean } | undefined {
  const remembered = readRememberedWorkspace()
  if (!remembered) return undefined
  const segments = pathname.split('/').filter(Boolean)
  if (!segments.length) return { viewerId: remembered.viewerId, workspaceKey: remembered.workspaceKey, root: true }
  let first: string
  try { first = decodeURIComponent(segments[0]) } catch { return undefined }
  // Sign-in, invitation, OAuth and onboarding pages never paint workspace data.
  if (['join', 'login', 'signup', 'verify-email', 'forgot-password', 'reset-password', 'invite', 'oauth', 'auth', 'connect', 'mobile-auth'].includes(first)) return undefined
  if (segments[1] === 'welcome') return undefined
  return { viewerId: remembered.viewerId, workspaceKey: first, root: false }
}

/**
 * Reads the snapshot for the page being opened, before the first render. Gives up after `timeoutMs` (a slow or
 * stuck IndexedDB must not delay startup) and resolves either way.
 */
export async function prepareStartupSnapshot(pathname: string, { enabled = true, timeoutMs = 250, now = Date.now() } = {}): Promise<LocalSnapshot | undefined> {
  startup = undefined
  const target = enabled ? startupSnapshotTarget(pathname) : undefined
  if (!target) return undefined
  const read = readLocalSnapshot(target.viewerId, target.workspaceKey, now).catch(() => undefined)
  const snapshot = await Promise.race([read, new Promise<undefined>(resolve => setTimeout(resolve, timeoutMs))])
  if (!snapshot) return undefined
  if (target.root) {
    // The root page opens the account's last workspace; only that one may be painted ahead of the account.
    const next = snapshot.account.workspaces.find(item => item.workspace.urlKey === snapshot.account.lastWorkspaceKey)?.workspace ?? snapshot.account.workspaces[0]?.workspace
    if (next?.urlKey !== snapshot.workspaceKey) return undefined
  }
  startup = snapshot
  return snapshot
}

export function startupSnapshot(): LocalSnapshot | undefined {
  return startup
}

export function discardStartupSnapshot() {
  startup = undefined
}

/**
 * Saves the latest confirmed workspace at most once per interval while it changes, when the browser is idle, and
 * at once when the page is hidden. `read` is called when the save runs, so a pending save never writes data that a
 * later update (or a clear) replaced.
 */
export function createLocalSnapshotSaver(read: () => Parameters<typeof saveLocalSnapshot>[0] | undefined, interval = LOCAL_SNAPSHOT_SAVE_INTERVAL_MS) {
  let lastKey = ''
  let lastAt = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let idle: number | undefined
  let pending = false
  const cancel = () => {
    if (timer !== undefined) clearTimeout(timer)
    if (idle !== undefined && typeof cancelIdleCallback === 'function') cancelIdleCallback(idle)
    timer = idle = undefined
  }
  const run = () => {
    cancel()
    pending = false
    const input = read()
    if (!input) return
    lastKey = localSnapshotKey(input.data.viewer.id, input.data.workspace.urlKey)
    lastAt = Date.now()
    const started = typeof performance !== 'undefined' ? performance.now() : 0
    void saveLocalSnapshot(input).then(result => {
      if (typeof performance === 'undefined' || typeof performance.measure !== 'function') return
      try { performance.measure(`flow:local-snapshot:${result}`, { start: started, end: performance.now() }) } catch { /* diagnostics only */ }
    })
  }
  const whenIdle = () => {
    if (typeof requestIdleCallback === 'function') idle = requestIdleCallback(run, { timeout: 2000 })
    else timer = setTimeout(run, 0)
  }
  return {
    /** `viewerId` and `workspaceKey` name what changed: another workspace is saved without waiting. */
    schedule(viewerId: string, workspaceKey: string) {
      pending = true
      if (timer !== undefined || idle !== undefined) return
      const key = localSnapshotKey(viewerId, workspaceKey)
      const wait = key === lastKey ? Math.max(0, lastAt + interval - Date.now()) : 0
      if (wait) timer = setTimeout(() => { timer = undefined; whenIdle() }, wait)
      else whenIdle()
    },
    flush() { if (pending) run() },
    cancel() { cancel(); pending = false },
  }
}

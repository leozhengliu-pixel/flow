import { compareLocalStoreSeq, createLocalIssueView, decimal, lazyLocalKinds, localKinds, localRowFingerprint, localStoreEnabled, localStoreEraseReason, LocalStoreReset, markLocalStoreDisabled, markLocalStoreErase, LOCAL_STORE_DISABLED, LOCAL_STORE_HEADER, type LocalChange, type LocalKind, type LocalRow, type LocalStoreAuthority, type LocalStoreEraseReason } from './local-store-model'
import { registerSessionCacheClearer } from './session-cache-registry'
import { activeBrowserTelemetry } from './browser-telemetry'
import { selectSlicesToEvict, type LocalSlice, type LocalSliceEntry, type LocalSliceKind } from './local-store-slice-model'
import type { OutboxEdit, OutboxOutcome } from './local-store-outbox-model'
import type { BootstrapData, Issue } from '@/types/flow'

const schema = 1
const prefix = 'flow-ls:'
const indexPrefix = 'flow:local-store:'
const headPrefix = `${indexPrefix}head:`
const sessionEndKey = `${indexPrefix}session-ended`
const disabledKey = `${indexPrefix}disabled`
const sessionChannelName = 'flow:local-store:session-ended'
const openPrefix = `${indexPrefix}open:`
const openLeaseMs = 120_000
const deletionWaitMs = 2000
const metaStore = '_meta'
const metaKey = 'workspace'
const directoryKey = 'directory'
const outbox = '_outbox'
const outboxOrder = 'outbox-order'
const issueReadBatchSize = 2048
export const LOCAL_STORE_BUDGET = 200 * 1024 * 1024
export const LOCAL_STARTUP_ISSUE_LIMIT = 512

export interface LocalMeta extends LocalStoreAuthority {
  schema: number
  build: string
  workspaceKey: string
  baseline: string
  bootstrapAt: number
  lastAccessAt: number
  fullCollections: LocalKind[]
  bytes: number
  sliceBytes?: number
  outboxBytes?: number
  resume?: LocalResume
}

export interface LocalResume {
  schema: 2
  revision: string
  eventCursor: string
  issueCount: number
  issueFingerprint: number
}

type Index = { workspaceKey: string; authzEpoch: string; token: string; deleting?: true }
const connections = new Set<LocalStoreDB>()
const deletes = new Map<string, Promise<boolean>>()
const networkOnly = new Set<string>()
let generation = 0
export const localStoreGeneration = () => generation

function openSessionChannel() {
  try {
    if (typeof BroadcastChannel === 'function') return new BroadcastChannel(sessionChannelName)
  } catch { /* Storage events also carry session invalidations. */ }
}

function databaseName(viewerId: string, workspaceId: string) {
  return `${prefix}${encodeURIComponent(viewerId)}:${encodeURIComponent(workspaceId)}`
}

function readIndex(name: string): Index | undefined {
  try { return JSON.parse(localStorage.getItem(indexPrefix + name) ?? 'null') ?? undefined }
  catch { return undefined }
}

function indexedNames() {
  const names = new Set([...connections].map(store => store.name))
  try {
    for (let i = 0; i < localStorage.length; ++i) {
      const key = localStorage.key(i)
      if (key?.startsWith(indexPrefix + prefix)) names.add(key.slice(indexPrefix.length))
    }
  } catch { /* Private storage can be unavailable. */ }
  return names
}

function indexMatches(name: string, viewerId?: string, workspaceKey?: string) {
  return (!viewerId || name.startsWith(`${prefix}${encodeURIComponent(viewerId)}:`)) && (!workspaceKey || readIndex(name)?.workspaceKey === workspaceKey)
}

function deleteStore(name: string, reason: LocalStoreEraseReason): Promise<boolean> {
  const pending = deletes.get(name)
  if (pending) return pending
  markLocalStoreErase(reason)
  const index = readIndex(name)
  if (index) {
    try { localStorage.setItem(indexPrefix + name, JSON.stringify({ ...index, deleting: true })) }
    catch { /* Still close open connections and attempt deletion. */ }
  }
  for (const store of connections) if (store.name === name) store.close()
  const deleting = new Promise<boolean>(resolve => {
    // A blocked request already waits for versionchange handlers to close
    // their connections. Retrying here would queue more deletes behind it.
    const timer = setTimeout(() => {
      markLocalStoreErase('idb-blocked')
      resolve(false)
    }, deletionWaitMs)
    const finish = (deleted: boolean) => {
      clearTimeout(timer)
      deletes.delete(name)
      resolve(deleted)
    }
    try {
      const request = indexedDB.deleteDatabase(name)
      request.onsuccess = () => {
        try {
          if (readIndex(name)?.deleting) {
            localStorage.removeItem(indexPrefix + name)
            localStorage.removeItem(headPrefix + name)
          }
        } catch { /* Ignore storage refusal. */ }
        finish(true)
      }
      request.onerror = () => {
        markLocalStoreErase(localStoreEraseReason(request.error))
        finish(false)
      }
    } catch (error) {
      markLocalStoreErase(localStoreEraseReason(error))
      clearTimeout(timer)
      queueMicrotask(() => finish(false))
    }
  })
  // The caller can fall back to the network, but a late completion must
  // still remove the tombstone and release this database for a fresh seed.
  deletes.set(name, deleting)
  return deleting
}

export function clearLocalStores(reason: LocalStoreEraseReason, viewerId?: string, workspaceKey?: string) {
  generation++
  const matches = [...indexedNames()].filter(name => indexMatches(name, viewerId, workspaceKey))
  return Promise.all(matches.map(name => deleteStore(name, reason)))
}

export function clearUnlistedLocalStores(viewerId: string, workspaces: readonly { id: string; urlKey: string }[]) {
  if (!localStoreEnabled()) return Promise.resolve()
  const allowed = new Map(workspaces.map(workspace => [databaseName(viewerId, workspace.id), workspace.urlKey]))
  const names = [...indexedNames()].filter(name => !allowed.has(name) || allowed.get(name) !== readIndex(name)?.workspaceKey)
  if (names.length) {
    generation++
    return Promise.all(names.map(name => deleteStore(name, 'authority-changed'))).then(() => undefined)
  }
  return Promise.resolve()
}

export function hasLocalStore(viewerId: string, workspaceId: string) {
  return !!readIndex(databaseName(viewerId, workspaceId))
}

export async function localStorageBudget() {
  try {
    const { usage, quota } = await navigator.storage?.estimate?.() ?? {}
    if (typeof usage === 'number' && typeof quota === 'number' && quota - usage < 1024 ** 3) {
      return Math.min(LOCAL_STORE_BUDGET, Math.max(0, Math.floor((quota - usage) / 5)))
    }
  } catch { /* Storage estimates are optional. */ }
  return LOCAL_STORE_BUDGET
}

export function recordLocalStoreVisit() {
  if (!navigator.storage?.persist) return
  const key = `${indexPrefix}visit-days`
  try {
    const days = JSON.parse(localStorage.getItem(key) ?? '[]') as string[]
    const day = new Date().toISOString().slice(0, 10)
    if (days.length >= 3 || days.includes(day)) return
    localStorage.setItem(key, JSON.stringify([...days, day]))
    if (days.length === 2) void navigator.storage.persist().catch(() => undefined)
  } catch { /* Neither localStorage nor persistence is required. */ }
}

export function keepLocalWorkspaceOpen(viewerId: string, workspaceId: string) {
  if (!localStoreEnabled()) return
  const name = databaseName(viewerId, workspaceId)
  const key = `${openPrefix}${name}:${crypto.randomUUID()}`
  const renew = () => {
    try { localStorage.setItem(key, String(Date.now() + openLeaseMs)) }
    catch { /* The browser may refuse persistent storage. */ }
  }
  renew()
  const timer = setInterval(renew, openLeaseMs / 4)
  const controller = new AbortController()
  const closed = new Promise<void>(resolve => controller.signal.addEventListener('abort', () => resolve(), { once: true }))
  void navigator.locks?.request(openPrefix + name, { mode: 'shared', signal: controller.signal }, () => closed).catch(() => undefined)
  return () => {
    clearInterval(timer)
    controller.abort()
    try { localStorage.removeItem(key) }
    catch { /* The fallback lease expires. */ }
  }
}

function workspaceIsOpen(name: string) {
  if ([...connections].some(connection => connection.name === name)) return true
  try {
    for (let i = 0; i < localStorage.length; ++i) {
      const key = localStorage.key(i)
      if (key?.startsWith(`${openPrefix}${name}:`) && Number(localStorage.getItem(key)) > Date.now()) return true
    }
  } catch { return true }
  return false
}

async function evictClosedWorkspace(name: string) {
  const evict = async () => {
    if (workspaceIsOpen(name)) return false
    const entry = readIndex(name)
    if (!entry) return false
    const [viewer, workspace] = name.slice(prefix.length).split(':').map(decodeURIComponent)
    const db = await LocalStoreDB.openRetained(viewer, workspace, entry.workspaceKey, generation)
    if (!db) return false
    const pending = await db.pendingEdits().finally(() => db.close())
    if (!pending || pending.length || workspaceIsOpen(name)) return false
    return deleteStore(name, 'workspace-evicted')
  }
  try {
    if (navigator.locks) return await navigator.locks.request(openPrefix + name, { ifAvailable: true }, lock => lock ? evict() : false)
  } catch { /* Fall back to the open leases. */ }
  return evict()
}

export function isLocalStoreQuotaError(error: unknown) {
  return error instanceof DOMException && error.name === 'QuotaExceededError'
}

export function retainedLocalWorkspaceId(viewerId: string, workspaceKey: string) {
  const viewerPrefix = `${prefix}${encodeURIComponent(viewerId)}:`
  for (const name of indexedNames()) {
    if (!name.startsWith(viewerPrefix)) continue
    const index = readIndex(name)
    if (index?.workspaceKey !== workspaceKey || index.deleting) continue
    try { return decodeURIComponent(name.slice(viewerPrefix.length)) }
    catch { return undefined }
  }
}

export function retainedLocalAuthority(viewerId: string, workspaceId: string, workspaceKey: string) {
  if (!localStoreEnabled()) return undefined
  const index = readIndex(databaseName(viewerId, workspaceId))
  if (index && !index.deleting && index.workspaceKey === workspaceKey) return index
}

function readHeadHint(name: string, token: string) {
  try {
    const hint = JSON.parse(localStorage.getItem(headPrefix + name) ?? 'null')
    if (hint?.token === token && decimal(hint.head)) return hint.head as string
  } catch { /* An unavailable hint only costs a database read before catch-up. */ }
}

export function retainedLocalHead(viewerId: string, workspaceId: string, workspaceKey: string): LocalStoreAuthority | undefined {
  const index = retainedLocalAuthority(viewerId, workspaceId, workspaceKey)
  if (!index) return undefined
  const head = readHeadHint(databaseName(viewerId, workspaceId), index.token)
  if (head !== undefined) return { viewerId, workspaceId, authzEpoch: index.authzEpoch, head }
}

export async function retainLocalStoreAuthority(scope: LocalStoreAuthority, workspaceKey: string, observedGeneration: number) {
  if (!localStoreEnabled() || observedGeneration !== generation) return undefined
  const current = databaseName(scope.viewerId, scope.workspaceId)
  const obsolete = [...indexedNames()].filter(name => {
    const index = readIndex(name)
    return index?.workspaceKey === workspaceKey && (name !== current || index.authzEpoch !== scope.authzEpoch || index.deleting)
  })
  if (!obsolete.length) return generation
  const checkpoint = ++generation
  const removed = await Promise.all(obsolete.map(name => deleteStore(name, 'authority-changed')))
  if (checkpoint === generation && removed.every(Boolean)) return checkpoint
  return undefined
}

export function initializeLocalStore() {
  // Remove the retired startup snapshot index before asking IndexedDB to
  // delete the data. An older tab can delay deletion by holding it open.
  try { localStorage.removeItem('flow:local-snapshot') } catch { /* Storage can be unavailable. */ }
  try {
    indexedDB.deleteDatabase('flow-local-snapshot').onsuccess = event => {
      if ((event as IDBVersionChangeEvent).oldVersion > 0) markLocalStoreErase('retired-snapshot')
    }
  } catch { /* Storage can be unavailable. */ }
  for (const name of indexedNames()) if (readIndex(name)?.deleting) void deleteStore(name, 'deletion-retry')
}

function wipeLocalStores() {
  const indexed = clearLocalStores('kill-switch')
  // Enumeration also removes databases whose index was lost independently of IndexedDB.
  const unlisted = typeof indexedDB !== 'undefined' && typeof indexedDB.databases === 'function'
    ? indexedDB.databases().then(rows => Promise.all(rows.flatMap(row => row.name?.startsWith(prefix) ? [deleteStore(row.name, 'kill-switch')] : []))).catch(() => undefined)
    : undefined
  return Promise.all([indexed, unlisted])
}

function applyLocalStoreDisable() {
  if (!localStoreEnabled()) return
  markLocalStoreDisabled()
  markLocalStoreErase('kill-switch')
  const clearing = wipeLocalStores()
  if (typeof window !== 'undefined') window.dispatchEvent(new Event(disabledKey))
  return clearing
}

function disableLocalStore() {
  const clearing = applyLocalStoreDisable()
  if (clearing) {
    // This is a notification, not a setting. A fresh page checks the server again.
    try { localStorage.setItem(disabledKey, crypto.randomUUID()) } catch { /* The index is unavailable too. */ }
  }
  return clearing
}

export function onLocalStoreDisabled(callback: () => void) {
  window.addEventListener(disabledKey, callback)
  return () => window.removeEventListener(disabledKey, callback)
}

export function invalidateLocalStoreResponse(url: string, response: Response, workspaceKey: string) {
  const { status } = response
  const clearing = response.headers?.get(LOCAL_STORE_HEADER) === LOCAL_STORE_DISABLED ? disableLocalStore() : undefined
  if (status === 401) return (clearing ?? clearLocalStores('session-ended')).then(() => {
    if (typeof window === 'undefined') return
    try { localStorage.setItem(sessionEndKey, crypto.randomUUID()) } catch { /* Other tabs also recheck authority. */ }
    const channel = openSessionChannel()
    channel?.postMessage({ type: 'session-ended' })
    channel?.close()
    window.dispatchEvent(new Event(sessionEndKey))
  })
  if (!localStoreEnabled()) return clearing
  // An unavailable issue capability on a cold start permits the shadow to
  // continue. Only an existing issue baseline or a definite refusal purges it.
  if (status === 404 && /^\/api\/sync\/manifest\?/.test(url)) {
    const query = new URLSearchParams(url.split('?')[1])
    if (query.get('surface') === 'local-store' && query.get('mode') === 'issues' && !query.has('counts')) return
  }
  const workspaceRead = /^\/api\/(?:bootstrap|issue-records\/bootstrap|sync\/(?:manifest|changes))(?:\?|$)/.test(url)
  const localRecordRead = /^\/api\/sync\/(?:records|batch)\?/.test(url) && new URLSearchParams(url.split('?')[1]).get('surface') === 'local-store'
  if (workspaceKey && (status === 403 || status === 404) && (workspaceRead || localRecordRead)) {
    // Let the workspace render its refusal even when a browser is still
    // closing other IndexedDB connections. The index is invalid immediately.
    void clearLocalStores('access-revoked', undefined, workspaceKey)
  }
}

export function onLocalStoreSessionEnd(callback: () => void) {
  const storage = (event: StorageEvent) => { if (event.key === sessionEndKey && event.newValue) callback() }
  const channel = openSessionChannel()
  const message = (event: MessageEvent) => { if (event.data?.type === 'session-ended') callback() }
  window.addEventListener(sessionEndKey, callback)
  window.addEventListener('storage', storage)
  channel?.addEventListener('message', message)
  return () => {
    window.removeEventListener(sessionEndKey, callback)
    window.removeEventListener('storage', storage)
    channel?.removeEventListener('message', message)
    channel?.close()
  }
}

registerSessionCacheClearer((viewerId, workspaceKey) => {
  if (localStoreEnabled()) void clearLocalStores(workspaceKey ? 'authority-changed' : 'session-ended', viewerId, workspaceKey)
})

if (typeof window !== 'undefined') {
  window.addEventListener('storage', event => {
    if (event.key === disabledKey && event.newValue) { void applyLocalStoreDisable(); return }
    if (event.key !== null && !event.key.startsWith(indexPrefix + prefix)) return
    if (event.oldValue || event.key === null) generation++
    for (const store of connections) if (!store.valid) {
      markLocalStoreErase('authority-changed')
      store.close()
    }
  })
  window.addEventListener('pagehide', () => {
    // A departing document may still be in the back/forward cache. Release
    // its connections now so another tab can delete a revoked database.
    generation++
    for (const store of connections) store.close()
  })
}

function upgrade(db: IDBDatabase) {
  db.createObjectStore(metaStore)
  db.createObjectStore(outbox, { keyPath: 'id' })
  for (const kind of [...localKinds, ...lazyLocalKinds]) {
    const store = db.createObjectStore(kind, { keyPath: 'id' })
    const indexes = kind === 'issue' ? ['teamId', 'stateId', 'assigneeId', 'projectId', 'parentId']
      : kind === 'comment' || kind === 'activity' ? ['issueId'] : kind === 'notification' ? ['createdAt'] : []
    for (const key of indexes) store.createIndex(key, key)
    if ((lazyLocalKinds as readonly string[]).includes(kind) || kind === 'issue') db.createObjectStore(`_partial_${kind}`, { keyPath: 'key' })
  }
}

export type LocalCollections = Map<LocalKind, Map<string, LocalRow>>

export class LocalStoreDB {
  readonly name: string
  private readonly token: string
  private readonly db: IDBDatabase
  private readonly transactions = new Set<IDBTransaction>()
  private closed = false
  private sharedSliceReads = false
  private constructor(name: string, token: string, db: IDBDatabase) {
    this.name = name
    this.token = token
    this.db = db
    connections.add(this)
    db.onversionchange = event => {
      markLocalStoreErase(event.newVersion === null ? 'authority-changed' : 'schema-mismatch')
      this.close()
    }
    db.onclose = () => {
      markLocalStoreErase('idb-closed')
      this.close()
    }
  }

  static async open(scope: LocalStoreAuthority, workspaceKey: string, started: number, forOutbox = false): Promise<LocalStoreDB | undefined> {
    if (!localStoreEnabled()) return undefined
    const name = databaseName(scope.viewerId, scope.workspaceId)
    if (networkOnly.has(name) && !forOutbox) return undefined
    let index = readIndex(name)
    if (index?.deleting) {
      if (!await deleteStore(name, 'deletion-retry')) return undefined
      index = undefined
    }
    if (started !== generation) {
      markLocalStoreErase('authority-changed')
      return undefined
    }
    try {
      if (index && (index.workspaceKey !== workspaceKey || index.authzEpoch !== scope.authzEpoch)) {
        markLocalStoreErase('authority-changed')
        return undefined
      }
      if (!index) {
        index = { workspaceKey, authzEpoch: scope.authzEpoch, token: crypto.randomUUID() }
        localStorage.setItem(indexPrefix + name, JSON.stringify(index))
      }
      const token = index.token
      return await new Promise((resolve, reject) => {
        const request = indexedDB.open(name, schema)
        let settled = false
        const finish = (value: LocalStoreDB | undefined) => { settled = true; clearTimeout(timer); resolve(value) }
        const blocked = () => {
          activeBrowserTelemetry()?.record({ kind: 'storage', status: 'open' })
          markLocalStoreErase('idb-blocked')
          finish(undefined)
        }
        const timer = setTimeout(blocked, 2000)
        const stillValid = () => {
          const latest = readIndex(name)
          return !settled && latest?.token === token && !latest.deleting && started === generation
        }
        request.onupgradeneeded = event => {
          if (stillValid()) {
            // The browser can evict IndexedDB without removing its head hint.
            if (event.oldVersion === 0 && readHeadHint(name, token) !== undefined) markLocalStoreErase('idb-missing')
            upgrade(request.result)
          } else request.transaction?.abort()
        }
        request.onerror = () => { clearTimeout(timer); if (!settled) reject(request.error) }
        request.onblocked = blocked
        request.onsuccess = () => {
          if (!stillValid()) {
            if (!settled) markLocalStoreErase('authority-changed')
            request.result.close()
            finish(undefined)
            return
          }
          finish(new LocalStoreDB(name, token, request.result))
        }
      })
    } catch (error) {
      const reason = localStoreEraseReason(error)
      activeBrowserTelemetry()?.record({ kind: 'storage', status: 'open' })
      if (reason === 'schema-mismatch') void deleteStore(name, reason)
      else markLocalStoreErase(reason)
      return undefined
    }
  }

  static openRetained(viewerId: string, workspaceId: string, workspaceKey: string, started: number, forOutbox = false) {
    const index = readIndex(databaseName(viewerId, workspaceId))
    if (!index || index.deleting || index.workspaceKey !== workspaceKey) return undefined
    return this.open({ viewerId, workspaceId, authzEpoch: index.authzEpoch, head: '0' }, workspaceKey, started, forOutbox)
  }

  static async withRetained<T>(scope: { viewerId: string; workspaceId: string; workspaceKey: string }, work: (db: LocalStoreDB) => Promise<T>) {
    const { viewerId, workspaceId, workspaceKey } = scope
    const name = databaseName(viewerId, workspaceId)
    const index = readIndex(name)
    if (!index || index.workspaceKey !== workspaceKey || index.deleting || networkOnly.has(name)) return undefined
    // Only the attached read model has a lifetime independent of its requests.
    for (const connection of connections) {
      if (connection.name === name && connection.sharedSliceReads && connection.valid) return work(connection)
    }
    const db = await this.openRetained(viewerId, workspaceId, workspaceKey, generation)
    if (!db) return undefined
    try { return await work(db) }
    finally { db.close() }
  }

  get valid() {
    const index = readIndex(this.name)
    return !this.closed && index?.token === this.token && !index.deleting
  }

  shareSliceReads() { this.sharedSliceReads = true }

  close() {
    if (this.closed) return
    this.closed = true
    connections.delete(this)
    for (const tx of this.transactions) {
      try { tx.abort() } catch { /* Already finished. */ }
    }
    this.db.close()
  }

  discard(reason: LocalStoreEraseReason) {
    const deleted = this.valid ? deleteStore(this.name, reason) : deletes.get(this.name)
    this.close()
    return deleted ?? Promise.resolve(false)
  }

  private transaction<T>(stores: string[], mode: IDBTransactionMode, work: (tx: IDBTransaction, result: (value: T) => void, fail: (error: unknown) => void) => void) {
    return new Promise<T | undefined>((resolve, reject) => {
      if (!this.valid) { resolve(undefined); return }
      // Only the outbox needs stronger durability than a recoverable mirror.
      let tx: IDBTransaction
      try {
        tx = this.db.transaction(stores, mode, mode === 'readwrite' ? { durability: stores.includes(outbox) ? 'strict' : 'relaxed' } : undefined)
      } catch (error) {
        markLocalStoreErase(localStoreEraseReason(error))
        activeBrowserTelemetry()?.record({ kind: 'storage', status: mode === 'readonly' ? 'read' : 'write' })
        reject(error)
        return
      }
      this.transactions.add(tx)
      let value: T | undefined
      let failed = false
      tx.oncomplete = () => {
        this.transactions.delete(tx)
        resolve(this.valid ? value : undefined)
      }
      tx.onabort = () => {
        this.transactions.delete(tx)
        if (failed) return
        const error = tx.error ?? new DOMException('Local store transaction aborted', 'AbortError')
        if (!this.closed || tx.error) {
          markLocalStoreErase(localStoreEraseReason(error))
          activeBrowserTelemetry()?.record({ kind: 'storage', status: mode === 'readonly' ? 'read' : 'write' })
        }
        reject(error)
      }
      const fail = (error: unknown) => {
        failed = true
        markLocalStoreErase(localStoreEraseReason(error))
        activeBrowserTelemetry()?.record({ kind: 'storage', status: mode === 'readonly' ? 'read' : 'write' })
        try { tx.abort() } catch { /* The transaction may already be inactive. */ }
        reject(error)
      }
      // All requests are enqueued synchronously or in a request callback.
      // Awaiting between statements makes WebKit transactions inactive.
      try { work(tx, next => { value = next }, fail) }
      catch (error) { fail(error) }
    })
  }

  readMeta() { return this.read<LocalMeta>(metaStore, metaKey) }

  invalidateReadModel(expected: LocalMeta, reason: LocalStoreEraseReason) {
    const readStores = [...localKinds, ...lazyLocalKinds, ...['issue', ...lazyLocalKinds].map(kind => `_partial_${kind}`)]
    return this.update(readStores, (tx, current) => {
      if (current?.baseline !== expected.baseline || current.head !== expected.head) return
      markLocalStoreErase(reason)
      for (const store of readStores) tx.objectStore(store).clear()
      tx.objectStore(metaStore).delete(directoryKey)
      return { ...current, baseline: crypto.randomUUID(), resume: undefined, sliceBytes: 0, bytes: 0, fullCollections: [] }
    })
  }

  appendEdit(edit: Omit<OutboxEdit, 'order'>, budget: number) {
    return this.transaction<boolean>([metaStore, outbox], 'readwrite', (tx, result, fail) => {
      const meta = tx.objectStore(metaStore)
      const rows = tx.objectStore(outbox)
      const read = meta.get(metaKey)
      const seq = meta.get(outboxOrder)
      const edits = rows.getAll()
      edits.onsuccess = () => {
        const checkpoint = read.result as LocalMeta | undefined
        const existing = edits.result as OutboxEdit[]
        if (!this.valid || !checkpoint?.resume || existing.filter(row => !row.outcome).length >= 1000) return
        const bytes = (checkpoint.outboxBytes ?? 0) + edit.bytes
        if (checkpoint.bytes + (checkpoint.sliceBytes ?? 0) + bytes > budget) {
          fail(new DOMException('Local store budget exceeded', 'QuotaExceededError'))
          return
        }
        const order = (seq.result as number | undefined ?? 0) + 1
        const previous = existing.filter(row => row.issueId === edit.issueId && row.editor === edit.editor)
        let expectedVersion = edit.input.expectedVersion
        for (const row of previous.sort((a, b) => a.order - b.order)) {
          if (row.outcome?.ok && row.input.expectedVersion === expectedVersion && row.outcome.rebaseVersion !== undefined) {
            expectedVersion = row.outcome.rebaseVersion
          }
        }
        try {
          meta.put(order, outboxOrder)
          meta.put({ ...checkpoint, outboxBytes: bytes }, metaKey)
          rows.add({ ...edit, input: { ...edit.input, expectedVersion }, order })
          const expired = existing.filter(row => row.outcome && row.createdAt < edit.createdAt - 60 * 60_000)
          if (expired.length) markLocalStoreErase('receipt-expired')
          for (const row of expired) rows.delete(row.id)
          result(true)
        } catch (error) { fail(error) }
      }
    })
  }

  pendingEdits() {
    return this.transaction<OutboxEdit[]>([outbox], 'readonly', (tx, result) => {
      const read = tx.objectStore(outbox).getAll()
      read.onsuccess = () => result((read.result as OutboxEdit[]).filter(edit => !edit.outcome).sort((a, b) => a.order - b.order))
    })
  }

  editOutcome(id: string) { return this.read<OutboxEdit>(outbox, id).then(edit => edit?.outcome) }

  markEditSent(id: string) {
    return this.transaction<OutboxEdit>([outbox], 'readwrite', (tx, result, fail) => {
      const rows = tx.objectStore(outbox)
      const read = rows.get(id)
      read.onsuccess = () => {
        const edit = read.result as OutboxEdit | undefined
        if (!edit || edit.outcome || !this.valid) return
        try {
          if (!edit.sent) rows.put({ ...edit, sent: true })
          result(edit)
        } catch (error) { fail(error) }
      }
    })
  }

  settleEdit(edit: OutboxEdit, outcome: OutboxOutcome) {
    return this.transaction<boolean>([metaStore, outbox], 'readwrite', (tx, result, fail) => {
      const meta = tx.objectStore(metaStore)
      const read = meta.get(metaKey)
      const rows = tx.objectStore(outbox)
      const edits = rows.getAll()
      edits.onsuccess = () => {
        const checkpoint = read.result as LocalMeta | undefined
        const pending = edits.result as OutboxEdit[]
        const current = pending.find(row => row.id === edit.id)
        if (!this.valid || !checkpoint || !current || current.outcome) return
        try {
          const { expectedVersion } = current.input
          if (outcome.ok && outcome.rebaseVersion !== undefined) {
            for (const row of pending) {
              if (row.order > edit.order && !row.sent && row.editor === edit.editor && row.issueId === edit.issueId && row.input.expectedVersion === expectedVersion) {
                rows.put({ ...row, input: { ...row.input, expectedVersion: outcome.rebaseVersion } })
              }
            }
          }
          rows.put({ ...current, input: { expectedVersion }, bytes: 0, outcome })
          meta.put({ ...checkpoint, outboxBytes: Math.max(0, (checkpoint.outboxBytes ?? 0) - current.bytes) }, metaKey)
          if (!outcome.ok) markLocalStoreErase(outcome.status === 409 ? 'write-receipt-conflict' : 'write-rejected')
          result(true)
        } catch (error) { fail(error) }
      }
    })
  }

  clearSettledEdits(before: number) {
    return this.transaction([outbox], 'readwrite', tx => {
      const read = tx.objectStore(outbox).openCursor()
      let pruned = false
      read.onsuccess = () => {
        const row = read.result
        if (!row) return
        if (row.value.outcome && row.value.createdAt < before) {
          if (!pruned) markLocalStoreErase('receipt-expired')
          pruned = true
          row.delete()
        }
        row.continue()
      }
    })
  }

  readCommittedIssues(ids: readonly string[], expected: { baseline: string; head: string }) {
    return this.transaction<{ meta: LocalMeta; changes: LocalChange[] }>([metaStore, 'issue'], 'readonly', (tx, result) => {
      const read = tx.objectStore(metaStore).get(metaKey)
      read.onsuccess = () => {
        const meta = read.result as LocalMeta | undefined
        if (!meta?.resume || meta.baseline !== expected.baseline || meta.head !== expected.head) return
        const changes: LocalChange[] = []
        if (!ids.length) { result({ meta, changes }); return }
        const issues = tx.objectStore('issue')
        for (const id of ids) {
          const row = issues.get(id)
          row.onsuccess = () => {
            changes.push({ kind: 'issue', id, row: row.result })
            if (changes.length === ids.length) result({ meta, changes })
          }
        }
      }
    })
  }

  readSlice<T>(kind: LocalSliceKind, key: string) {
    return this.transaction<{ meta: LocalMeta; slice?: LocalSlice<T> }>([metaStore, `_partial_${kind}`], 'readonly', (tx, result) => {
      const meta = tx.objectStore(metaStore).get(metaKey)
      const row = tx.objectStore(`_partial_${kind}`).get(key)
      row.onsuccess = () => {
        if (meta.result?.resume) result({ meta: meta.result, slice: row.result })
      }
    })
  }

  writeSlice<T>(slice: LocalSlice<T>, budget: number) {
    return this.updateSlices((tx, meta, entries) => {
      const eagerBytes = meta.bytes + (meta.outboxBytes ?? 0)
      if (slice.authzEpoch !== meta.authzEpoch || compareLocalStoreSeq(slice.seq, meta.head) < 0 ||
          compareLocalStoreSeq(slice.eventCursor, meta.resume!.eventCursor) < 0 || eagerBytes + slice.bytes > budget) return false
      const others = entries.filter(entry => entry.kind !== slice.kind || entry.key !== slice.key)
      const partialBytes = others.reduce((sum, entry) => sum + entry.bytes, 0)
      const evicted = selectSlicesToEvict(others, eagerBytes + partialBytes + slice.bytes - budget)
      if (evicted.length) markLocalStoreErase('slice-evicted')
      for (const entry of evicted) tx.objectStore(`_partial_${entry.kind}`).delete(entry.key)
      tx.objectStore(`_partial_${slice.kind}`).put(slice)
      const sliceBytes = partialBytes + slice.bytes - evicted.reduce((sum, entry) => sum + entry.bytes, 0)
      tx.objectStore(metaStore).put({ ...meta, sliceBytes, lastAccessAt: Date.now() }, metaKey)
      return true
    })
  }

  removeSlice(kind: LocalSliceKind, key: string) {
    const store = `_partial_${kind}`
    return this.transaction([metaStore, store], 'readwrite', (tx, _, fail) => {
      const metadata = tx.objectStore(metaStore)
      const rows = tx.objectStore(store)
      const meta = metadata.get(metaKey)
      const row = rows.get(key)
      row.onsuccess = () => {
        const checkpoint = meta.result as LocalMeta | undefined
        const slice = row.result as LocalSliceEntry | undefined
        if (!checkpoint?.resume || !slice || !this.valid) return
        try {
          markLocalStoreErase('slice-revoked')
          rows.delete(key)
          metadata.put({ ...checkpoint, sliceBytes: Math.max(0, (checkpoint.sliceBytes ?? 0) - slice.bytes) }, metaKey)
        } catch (error) { fail(error) }
      }
    })
  }

  evictSlices(bytes: number, kind?: LocalSliceKind) {
    return this.updateSlices((tx, meta, entries) => {
      const evicted = selectSlicesToEvict(kind ? entries.filter(entry => entry.kind === kind) : entries, bytes)
      if (evicted.length) markLocalStoreErase('slice-evicted')
      for (const entry of evicted) tx.objectStore(`_partial_${entry.kind}`).delete(entry.key)
      const freed = evicted.reduce((sum, entry) => sum + entry.bytes, 0)
      const sliceBytes = entries.reduce((sum, entry) => sum + entry.bytes, 0) - freed
      if (freed) tx.objectStore(metaStore).put({ ...meta, sliceBytes }, metaKey)
      return freed
    })
  }

  private updateSlices<T>(write: (tx: IDBTransaction, meta: LocalMeta, entries: LocalSliceEntry[]) => T) {
    const stores = ['_partial_activity', '_partial_issue']
    return this.transaction<T>([metaStore, ...stores], 'readwrite', (tx, result, fail) => {
      const readMeta = tx.objectStore(metaStore).get(metaKey)
      readMeta.onsuccess = () => {
        const meta = readMeta.result as LocalMeta | undefined
        if (!meta?.resume || !this.valid) return
        const entries: LocalSliceEntry[] = []
        let remaining = stores.length
        for (const store of stores) {
          const read = tx.objectStore(store).openCursor()
          read.onsuccess = () => {
            const cursor = read.result
            if (cursor) {
              const { value: _value, ...entry } = cursor.value as LocalSlice<unknown>
              entries.push(entry)
              cursor.continue()
            } else if (--remaining === 0 && this.valid) {
              try { result(write(tx, meta, entries)) }
              catch (error) { fail(error) }
            }
          }
        }
      }
    })
  }

  private async makeRoom(limit: number, growth: number) {
    const others = await Promise.all([...indexedNames()].filter(name => name !== this.name).map(async name => {
      try {
        const index = readIndex(name)
        if (!index || index.deleting) return undefined
        const [viewerId, workspaceId] = name.slice(prefix.length).split(':').map(decodeURIComponent)
        const db = await LocalStoreDB.openRetained(viewerId, workspaceId, index.workspaceKey, generation, true)
        if (!db) return undefined
        try {
          const meta = await db.readMeta()
          return meta && { name, meta }
        } finally { db.close() }
      } catch { return undefined }
    }))
    const entries = others.filter(entry => entry !== undefined).sort((a, b) => a.meta.lastAccessAt - b.meta.lastAccessAt)
    const current = await this.readMeta()
    let elsewhere = entries.reduce((sum, { meta }) => sum + meta.bytes + (meta.sliceBytes ?? 0) + (meta.outboxBytes ?? 0), 0)
    let needed = (current?.bytes ?? 0) + (current?.sliceBytes ?? 0) + (current?.outboxBytes ?? 0) + growth + elsewhere - limit
    for (const kind of ['activity', 'issue'] as const) {
      if (needed <= 0) break
      const reclaimed = await this.evictSlices(needed, kind) ?? 0
      needed -= reclaimed
      for (const { meta } of entries) {
        if (needed <= 0) break
        const db = await LocalStoreDB.openRetained(meta.viewerId, meta.workspaceId, meta.workspaceKey, generation, true)
        if (!db) continue
        try {
          const freed = await db.evictSlices(needed, kind) ?? 0
          needed -= freed
          elsewhere -= freed
        } finally { db.close() }
      }
    }
    for (const { name, meta } of entries) {
      if (needed <= 0) break
      // Oldest first also puts workspaces unopened for 30 days ahead of newer ones.
      if (await evictClosedWorkspace(name)) {
        elsewhere -= meta.bytes
        needed -= meta.bytes
      }
    }
    return Math.max(0, limit - elsewhere)
  }

  async disableForSession() {
    markLocalStoreErase('quota')
    networkOnly.add(this.name)
    // Keep durable edits available to the sync owner during network-only use.
    const pending = await this.pendingEdits().catch(() => undefined)
    if (pending?.length === 0) await this.discard('quota')
  }

  private async retryAfterQuota<T>(write: () => Promise<T>) {
    try { return await write() }
    catch (error) {
      if (!isLocalStoreQuotaError(error) || !this.valid) throw error
      try {
        await this.evictSlices(Infinity)
        return await write()
      } catch (retryError) {
        if (isLocalStoreQuotaError(retryError)) await this.disableForSession()
        throw retryError
      }
    }
  }

  async writeWithinBudget<T>(growth: number, write: (budget: number) => Promise<T>, limit?: number) {
    const run = () => this.retryAfterQuota(async () => write(await this.makeRoom(limit ?? await localStorageBudget(), growth)))
    let entered = false
    try {
      if (navigator.locks) return await navigator.locks.request(`${indexPrefix}budget`, () => {
        entered = true
        return run()
      })
    } catch (error) {
      if (entered) throw error
    }
    return run()
  }

  rememberHead(meta: LocalMeta) {
    if (!this.valid) return
    const head = readHeadHint(this.name, this.token)
    if (head !== undefined && compareLocalStoreSeq(meta.head, head) <= 0) return
    // Hint writes never touch the index entry used to revoke access in other tabs.
    try { localStorage.setItem(headPrefix + this.name, JSON.stringify({ token: this.token, head: meta.head })) }
    catch { /* An unavailable hint only costs a database read before catch-up. */ }
  }

  private readIssueView(expected: LocalMeta | undefined, maxIssues: number) {
    return this.transaction<{ meta: LocalMeta; view?: { directory: BootstrapData; issues: Issue[] } }>([metaStore, 'issue'], 'readonly', (tx, result, fail) => {
      const metadata = tx.objectStore(metaStore)
      const read = metadata.get(metaKey)
      const readRows = (limit: number) => {
        const directory = metadata.get(directoryKey)
        const store = tx.objectStore('issue')
        const issues: Issue[] = []
        const project = createLocalIssueView()
        // Project each batch as it arrives so structured clones can be
        // collected. One transaction keeps the directory and rows at one head.
        const next = (after?: string) => {
          const rows = store.getAll(after === undefined ? null : IDBKeyRange.lowerBound(after, true), limit)
          rows.onsuccess = () => {
            const meta = read.result as LocalMeta | undefined
            if (!meta?.resume) return
            if (meta.resume.issueCount > maxIssues) { result({ meta }); return }
            const data = directory.result as BootstrapData | undefined
            const batch = rows.result as LocalRow[]
            const count = issues.length + batch.length
            if (!data || data.viewer.id !== meta.viewerId || data.workspace.id !== meta.workspaceId ||
                data.workspace.urlKey !== meta.workspaceKey || count > meta.resume.issueCount ||
                batch.length < limit && count !== meta.resume.issueCount) {
              fail(new LocalStoreReset('baseline-incomplete', 'Issue baseline is incomplete'))
              return
            }
            try {
              for (const row of batch) issues.push(project(row))
              if (batch.length === limit) next(batch[batch.length - 1].id)
              else result({ meta, view: { directory: data, issues } })
            } catch (error) { fail(error) }
          }
        }
        next()
      }
      if (!expected) {
        // Read a bounded first page alongside the metadata on repeat visits.
        readRows(maxIssues + 1)
      } else {
        read.onsuccess = () => {
          const meta = read.result as LocalMeta | undefined
          if (meta?.resume && meta.baseline === expected.baseline && meta.head === expected.head) readRows(issueReadBatchSize)
        }
      }
    })
  }

  readIssueStartup() { return this.readIssueView(undefined, LOCAL_STARTUP_ISSUE_LIMIT) }

  async readIssueWorkspace(expected: LocalMeta) {
    return (await this.readIssueView(expected, Infinity))?.view
  }

  readIssueChunk(after: string | undefined, limit: number) {
    return this.transaction<LocalRow[]>(['issue'], 'readonly', (tx, result) => {
      const read = tx.objectStore('issue').getAll(after === undefined ? null : IDBKeyRange.lowerBound(after, true), limit)
      read.onsuccess = () => result(read.result)
    })
  }

  readRows(kind: LocalKind) {
    return this.transaction<LocalRow[]>([kind], 'readonly', (tx, result) => {
      const read = tx.objectStore(kind).getAll()
      read.onsuccess = () => result(read.result as LocalRow[])
    })
  }

  private read<T>(store: string, key: string) {
    return this.transaction<T>([store], 'readonly', (tx, result) => {
      const read = tx.objectStore(store).get(key)
      read.onsuccess = () => result(read.result as T)
    })
  }

  seed(authority: LocalStoreAuthority, metadata: Omit<LocalMeta, keyof LocalStoreAuthority | 'schema'>, rows: LocalCollections, expected?: LocalMeta, directory?: BootstrapData, budget = LOCAL_STORE_BUDGET) {
    const meta: LocalMeta = { ...authority, ...metadata, schema }
    return this.update(localKinds, (tx, previous) => {
      if (previous?.baseline !== expected?.baseline || previous?.head !== expected?.head) return
      if (previous && previous.authzEpoch === meta.authzEpoch && compareLocalStoreSeq(previous.head, meta.head) > 0) return
      if (previous?.resume && !meta.resume) return
      if (meta.bytes > budget) return
      if (meta.bytes + (previous?.sliceBytes ?? 0) + (previous?.outboxBytes ?? 0) > budget) throw new DOMException('Local store budget exceeded', 'QuotaExceededError')
      if (previous?.bytes) markLocalStoreErase('baseline-replaced')
      for (const kind of localKinds) {
        const store = tx.objectStore(kind)
        store.clear()
        for (const item of rows.get(kind)?.values() ?? []) store.put(item)
      }
      const metadata = tx.objectStore(metaStore)
      if (directory) metadata.put(directory, directoryKey)
      else metadata.delete(directoryKey)
      return { ...meta, sliceBytes: previous?.sliceBytes, outboxBytes: previous?.outboxBytes }
    })
  }

  apply(meta: LocalMeta, next: string, bytes: number, writes: LocalChange[], resume = meta.resume) {
    const touched = [...new Set(writes.map(change => change.kind))]
    return this.update(touched, (tx, current) => {
      if (current?.baseline !== meta.baseline || current.head !== meta.head || current.authzEpoch !== meta.authzEpoch) return
      if (writes.some(change => !change.row)) markLocalStoreErase('journal-removal')
      for (const { kind, id, row } of writes) {
        const store = tx.objectStore(kind)
        if (row) store.put(row)
        else store.delete(id)
      }
      return { ...current, head: next, bytes, resume, lastAccessAt: Date.now() }
    })
  }

  applyIssues(meta: LocalMeta, head: string, checkpoint: Pick<LocalResume, 'revision' | 'eventCursor'>, writes: LocalChange[], budget: number) {
    return this.transaction<LocalMeta>([metaStore, 'issue'], 'readwrite', (tx, result, fail) => {
      const metadata = tx.objectStore(metaStore)
      const read = metadata.get(metaKey)
      read.onsuccess = () => {
        const current = read.result as LocalMeta | undefined
        if (!current?.resume || current.baseline !== meta.baseline || current.head !== meta.head || current.authzEpoch !== meta.authzEpoch) return
        const issues = tx.objectStore('issue')
        let { bytes } = current
        let { issueCount, issueFingerprint } = current.resume
        const commit = () => {
          if (bytes > budget) { fail(new LocalStoreReset('quota', 'Issue store exceeds budget')); return }
          if (bytes + (current.sliceBytes ?? 0) + (current.outboxBytes ?? 0) > budget) {
            fail(new DOMException('Local store budget exceeded', 'QuotaExceededError'))
            return
          }
          try {
            if (writes.some(change => !change.row)) markLocalStoreErase('journal-removal')
            for (const { id, row } of writes) {
              if (row) issues.put(row)
              else issues.delete(id)
            }
            const next = { ...current, head, bytes, lastAccessAt: Date.now(), resume: { ...current.resume!, ...checkpoint, issueCount, issueFingerprint } }
            metadata.put(next, metaKey)
            result(next)
          } catch (error) { fail(error) }
        }
        if (!writes.length) { commit(); return }
        let pending = writes.length
        for (const { id, row } of writes) {
          const previous = issues.get(id)
          previous.onsuccess = () => {
            for (const [value, direction] of [[previous.result as LocalRow | undefined, -1], [row, 1]] as const) {
              if (!value) continue
              const fingerprint = localRowFingerprint(value)
              bytes += direction * fingerprint.bytes
              issueCount += direction
              issueFingerprint ^= fingerprint.hash
            }
            if (--pending === 0) commit()
          }
        }
      }
    })
  }

  private update(stores: readonly string[], write: (tx: IDBTransaction, current?: LocalMeta) => LocalMeta | undefined) {
    return this.transaction<LocalMeta>([metaStore, ...stores], 'readwrite', (tx, result, fail) => {
      const object = tx.objectStore(metaStore)
      const read = object.get(metaKey)
      read.onsuccess = () => {
        if (!this.valid) return
        try {
          const next = write(tx, read.result as LocalMeta | undefined)
          if (next) {
            object.put(next, metaKey)
            result(next)
            if (typeof tx.commit === 'function') tx.commit()
          }
        } catch (error) {
          fail(error)
        }
      }
    })
  }
}

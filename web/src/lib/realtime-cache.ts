import type { BootstrapData } from '@/types/flow'
import { APPLIED_CURSOR_SCHEMA, appliedRealtimeCursorEnabled } from '@/lib/scale-p08'
import { registerSessionCacheClearer } from '@/lib/session-cache-registry'

/**
 * Small durable cache for the workspace stream cursor and latest snapshot.
 * IndexedDB is preferred for large workspaces; localStorage is kept as a
 * synchronous fallback for private browsing and test environments.
 */
export interface RealtimeCacheRecord {
  workspaceKey: string
  viewerId?: string
  schema?: number
  cursor?: string
  snapshot?: BootstrapData
  updatedAt: string
}

const databaseName = 'flow-realtime-cache'
const storeName = 'workspaces'
const databaseVersion = 1
const storagePrefix = 'flow:realtime:'
const memorySnapshots = new Map<string, BootstrapData | undefined>()

function withCursor(record: RealtimeCacheRecord | undefined, workspaceKey: string): RealtimeCacheRecord | undefined {
  const checkpoint = readLocal(workspaceKey)
  if (!record && !checkpoint) return undefined
  return { ...(record ?? checkpoint!), cursor: checkpoint?.cursor ?? record?.cursor, snapshot: record?.snapshot ?? memorySnapshots.get(workspaceKey) }
}

function storageKey(workspaceKey: string) {
  return `${storagePrefix}${workspaceKey}`
}

function readLocal(workspaceKey: string): RealtimeCacheRecord | undefined {
  if (typeof localStorage === 'undefined') return undefined
  try {
    const value = localStorage.getItem(storageKey(workspaceKey))
    return value ? (JSON.parse(value) as RealtimeCacheRecord) : undefined
  } catch {
    return undefined
  }
}

export function loadRealtimeCursor(workspaceKey: string, viewerId?: string, schema = APPLIED_CURSOR_SCHEMA): string | undefined {
  if (appliedRealtimeCursorEnabled()) return readAppliedCheckpoint(workspaceKey, viewerId, schema)?.cursor
  return readLocal(workspaceKey)?.cursor
}

const appliedPrefix = 'flow:realtime:applied:'

function appliedKey(workspaceKey: string, viewerId: string, schema: number) {
  return `${appliedPrefix}${schema}:${encodeURIComponent(viewerId)}:${encodeURIComponent(workspaceKey)}`
}

function readAppliedCheckpoint(workspaceKey: string, viewerId?: string, schema = APPLIED_CURSOR_SCHEMA): RealtimeCacheRecord | undefined {
  if (!workspaceKey || !viewerId || typeof localStorage === 'undefined') return undefined
  try {
    const raw = localStorage.getItem(appliedKey(workspaceKey, viewerId, schema))
    if (!raw || raw.length > 4096) return undefined
    const record = JSON.parse(raw) as RealtimeCacheRecord
    return record.workspaceKey === workspaceKey && record.viewerId === viewerId && record.schema === schema && typeof record.cursor === 'string' ? record : undefined
  } catch { return undefined }
}

function clearAppliedCheckpoints(viewerId?: string, workspaceKey?: string) {
  if (typeof localStorage === 'undefined') return
  try {
    for (let i = localStorage.length - 1; i >= 0; i--) {
      const key = localStorage.key(i)
      if (!key?.startsWith(appliedPrefix)) continue
      const [, viewer, workspace] = key.slice(appliedPrefix.length).split(':')
      if ((!viewerId || viewer === encodeURIComponent(viewerId)) && (!workspaceKey || workspace === encodeURIComponent(workspaceKey))) localStorage.removeItem(key)
    }
  } catch { /* Storage is optional; checkpoints never replace initialization. */ }
}

registerSessionCacheClearer(clearAppliedCheckpoints)

function writeLocal(record: RealtimeCacheRecord) {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(storageKey(record.workspaceKey), JSON.stringify({ workspaceKey: record.workspaceKey, cursor: record.cursor, updatedAt: record.updatedAt }))
  } catch {
    // Quota/private-mode failures should never interrupt the live stream.
  }
}

function openDatabase(): Promise<IDBDatabase | undefined> {
  if (typeof indexedDB === 'undefined') return Promise.resolve(undefined)
  return new Promise((resolve) => {
    let request: IDBOpenDBRequest
    try {
      request = indexedDB.open(databaseName, databaseVersion)
    } catch {
      resolve(undefined)
      return
    }
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(storeName)) request.result.createObjectStore(storeName, { keyPath: 'workspaceKey' })
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => resolve(undefined)
    request.onblocked = () => resolve(undefined)
  })
}

export async function loadRealtimeCache(workspaceKey: string, viewerId?: string, schema = APPLIED_CURSOR_SCHEMA): Promise<RealtimeCacheRecord | undefined> {
  if (!workspaceKey) return undefined
  if (appliedRealtimeCursorEnabled()) return readAppliedCheckpoint(workspaceKey, viewerId, schema)
  const database = await openDatabase()
  if (!database) return withCursor(undefined, workspaceKey)
  return new Promise((resolve) => {
    const request = database.transaction(storeName, 'readonly').objectStore(storeName).get(workspaceKey)
    request.onsuccess = () => resolve(withCursor(request.result as RealtimeCacheRecord | undefined, workspaceKey))
    request.onerror = () => resolve(withCursor(undefined, workspaceKey))
    database.close()
  })
}

export async function saveRealtimeCache(record: RealtimeCacheRecord): Promise<void> {
  if (!record.workspaceKey) return
  if (appliedRealtimeCursorEnabled()) {
    // There is no atomic entity+cursor store. Keep only a diagnostic checkpoint;
    // a new tab still subscribes and loads a fresh server baseline.
    if (record.cursor) await saveRealtimeCursor(record.workspaceKey, record.cursor, record.viewerId, record.schema)
    return
  }
  writeLocal({ ...record, cursor: readLocal(record.workspaceKey)?.cursor ?? record.cursor })
  const database = await openDatabase()
  if (!database) {
    memorySnapshots.delete(record.workspaceKey)
    memorySnapshots.set(record.workspaceKey, record.snapshot)
    while (memorySnapshots.size > 2) memorySnapshots.delete(memorySnapshots.keys().next().value!)
    return
  }
  await new Promise<void>((resolve) => {
    const request = database.transaction(storeName, 'readwrite').objectStore(storeName).put(record)
    request.onsuccess = request.onerror = () => resolve()
  })
  database.close()
}

export async function saveRealtimeCursor(workspaceKey: string, cursor: string, viewerId?: string, schema = APPLIED_CURSOR_SCHEMA): Promise<void> {
  if (!workspaceKey || !cursor) return
  if (appliedRealtimeCursorEnabled()) {
    if (!viewerId || typeof localStorage === 'undefined') return
    try {
      localStorage.setItem(appliedKey(workspaceKey, viewerId, schema), JSON.stringify({ workspaceKey, viewerId, schema, cursor, updatedAt: new Date().toISOString() }))
    } catch { /* Quota/private-mode failures must not interrupt delivery. */ }
    return
  }
  // Cursor checkpoints must not read, serialize, or rewrite a workspace snapshot.
  writeLocal({ workspaceKey, cursor, updatedAt: new Date().toISOString() })
}

export async function clearRealtimeCache(workspaceKey: string): Promise<void> {
  if (!workspaceKey) return
  clearAppliedCheckpoints(undefined, workspaceKey)
  memorySnapshots.delete(workspaceKey)
  if (typeof localStorage !== 'undefined') {
    try {
      localStorage.removeItem(storageKey(workspaceKey))
    } catch {
      // Ignore storage failures.
    }
  }
  const database = await openDatabase()
  if (!database) return
  await new Promise<void>((resolve) => {
    const request = database.transaction(storeName, 'readwrite').objectStore(storeName).delete(workspaceKey)
    request.onsuccess = request.onerror = () => resolve()
  })
  database.close()
}

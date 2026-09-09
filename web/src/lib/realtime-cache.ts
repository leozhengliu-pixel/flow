import type { BootstrapData } from '@/types/flow'

/**
 * Small durable cache for the workspace stream cursor and latest snapshot.
 * IndexedDB is preferred for large workspaces; localStorage is kept as a
 * synchronous fallback for private browsing and test environments.
 */
export interface RealtimeCacheRecord {
  workspaceKey: string
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

export function loadRealtimeCursor(workspaceKey: string): string | undefined {
  return readLocal(workspaceKey)?.cursor
}

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

export async function loadRealtimeCache(workspaceKey: string): Promise<RealtimeCacheRecord | undefined> {
  if (!workspaceKey) return undefined
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

export async function saveRealtimeCursor(workspaceKey: string, cursor: string): Promise<void> {
  if (!workspaceKey || !cursor) return
  // Cursor checkpoints must not read, serialize, or rewrite a workspace snapshot.
  writeLocal({ workspaceKey, cursor, updatedAt: new Date().toISOString() })
}

export async function clearRealtimeCache(workspaceKey: string): Promise<void> {
  if (!workspaceKey) return
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

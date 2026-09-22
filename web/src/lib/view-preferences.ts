/**
 * LS-0781 ViewPreferences store — org-scoped hydrated preferences keyed by view type.
 * REST-only: persists via ClientStorage (no GraphQL). Surfaces hydrate + setPreference.
 */
import { ClientStorage } from '@/lib/client-storage'

export type ViewPreferenceType =
  | 'issues'
  | 'projects'
  | 'triage'
  | 'myIssues'
  | 'views'
  | 'cycle'
  | 'team'
  | 'custom'

export type ViewPreferencesRecord = {
  layout?: string
  viewOrdering?: string
  viewOrderingDirection?: string
  issueGrouping?: string
  issueSubGrouping?: string
  projectLayout?: string
  projectViewOrdering?: string
  projectGrouping?: string
  projectSubGrouping?: string
  projectZoomLevel?: string
  timelineZoomScale?: number
  triageViewOrdering?: string
  showCompletedIssues?: string
  showCompletedProjects?: string
  showSubIssues?: boolean
  showTriageIssues?: boolean
  showEmptyGroups?: boolean
  [key: string]: string | number | boolean | undefined
}

type StoreBucket = Record<string, ViewPreferencesRecord>

const STORAGE_PREFIX = 'flow:view-preferences:'
const listeners = new Map<string, Set<() => void>>()
const memory = new Map<string, StoreBucket>()
/** Stable snapshots for useSyncExternalStore getServerSnapshot/getSnapshot. */
const snapshots = new Map<string, ViewPreferencesRecord>()

function storageKey(orgKey: string) {
  return `${STORAGE_PREFIX}${orgKey || 'default'}`
}

function snapshotKey(orgKey: string, type: ViewPreferenceType) {
  return `${orgKey}::${type}`
}

function readBucket(orgKey: string): StoreBucket {
  if (memory.has(orgKey)) return memory.get(orgKey)!
  const stored = ClientStorage.get<StoreBucket>(storageKey(orgKey), {
    storageMechanism: 'local',
    logError: false,
  })
  const bucket = stored && typeof stored === 'object' ? stored : {}
  memory.set(orgKey, bucket)
  return bucket
}

function refreshSnapshot(orgKey: string, type: ViewPreferenceType) {
  const record = { ...(readBucket(orgKey)[type] ?? {}) }
  snapshots.set(snapshotKey(orgKey, type), record)
  return record
}

function writeBucket(orgKey: string, bucket: StoreBucket) {
  memory.set(orgKey, bucket)
  ClientStorage.set(storageKey(orgKey), bucket, 'local')
  for (const type of Object.keys(bucket) as ViewPreferenceType[]) {
    refreshSnapshot(orgKey, type)
  }
  for (const listener of listeners.get(orgKey) ?? []) listener()
}

function subscribe(orgKey: string, listener: () => void) {
  const set = listeners.get(orgKey) ?? new Set()
  set.add(listener)
  listeners.set(orgKey, set)
  return () => {
    set.delete(listener)
  }
}

export class ViewPreferencesHandle {
  orgKey: string
  type: ViewPreferenceType

  constructor(orgKey: string, type: ViewPreferenceType) {
    this.orgKey = orgKey
    this.type = type
  }

  hydrate(): ViewPreferencesRecord {
    const bucket = readBucket(this.orgKey)
    if (!bucket[this.type]) {
      bucket[this.type] = {}
      writeBucket(this.orgKey, bucket)
    } else {
      refreshSnapshot(this.orgKey, this.type)
    }
    return this.get()
  }

  get(): ViewPreferencesRecord {
    const key = snapshotKey(this.orgKey, this.type)
    const existing = snapshots.get(key)
    if (existing) return existing
    return refreshSnapshot(this.orgKey, this.type)
  }

  setPreference<K extends keyof ViewPreferencesRecord>(key: K, value: ViewPreferencesRecord[K]) {
    const bucket = { ...readBucket(this.orgKey) }
    const current = { ...(bucket[this.type] ?? {}) }
    if (value === undefined) {
      delete current[key]
    } else {
      current[key] = value
    }
    bucket[this.type] = current
    writeBucket(this.orgKey, bucket)
  }

  save(_force = false) {
    writeBucket(this.orgKey, readBucket(this.orgKey))
  }

  subscribe(listener: () => void) {
    return subscribe(this.orgKey, listener)
  }
}

export class ViewPreferencesOrganization {
  orgKey: string

  constructor(orgKey: string) {
    this.orgKey = orgKey
  }

  hydrate(): StoreBucket {
    return { ...readBucket(this.orgKey) }
  }

  getViewPreferences(type: ViewPreferenceType): ViewPreferencesHandle {
    return new ViewPreferencesHandle(this.orgKey, type)
  }
}

/** Test / ResetApplication helper. */
export function clearViewPreferences(orgKey?: string) {
  if (orgKey) {
    memory.delete(orgKey)
    ClientStorage.remove(storageKey(orgKey), 'local')
    for (const key of [...snapshots.keys()]) {
      if (key.startsWith(`${orgKey}::`)) snapshots.delete(key)
    }
    for (const listener of listeners.get(orgKey) ?? []) listener()
    return
  }
  for (const key of [...memory.keys()]) {
    memory.delete(key)
    ClientStorage.remove(storageKey(key), 'local')
  }
  snapshots.clear()
  for (const key of ClientStorage.getKeys('local')) {
    if (key.startsWith(STORAGE_PREFIX)) ClientStorage.remove(key, 'local')
  }
  for (const set of listeners.values()) {
    for (const listener of set) listener()
  }
}

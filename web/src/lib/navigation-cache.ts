import type { BootstrapData, Issue } from '@/types/flow'
import { issueCacheSummary, mergeIssueRecords } from '@/lib/issue-detail-cache'
import { agentSessionSummary } from '@/lib/agent-session-merge'
import { clearSessionCaches } from '@/lib/session-cache-registry'
import { boundedBrowserCacheEnabled, cacheStringBytes, NAVIGATION_CACHE_MAX_BYTES } from '@/lib/scale-p08'

export interface NavigationCacheRecord {
  viewerId: string
  workspaceKey: string
  updatedAt: string
  data: BootstrapData
  schema?: number
}

export interface CachedIssuePreview {
  viewerId: string
  key: string
  issue: Issue
}

const storagePrefix = 'flow:navigation:'
const memory = new Map<string, NavigationCacheRecord>()
const boundedMemory = new Map<string, string>()
const navigationSchema = 2
let invalidationGeneration = 0

export function navigationCacheGeneration() {
  return invalidationGeneration
}

/** A later revoke must not be undone by an older recovery request finishing. */
export function writeRecoveredNavigationCache(viewerId: string, workspaceKey: string, data: BootstrapData, generation: number) {
  if (boundedBrowserCacheEnabled() && generation === invalidationGeneration) writeNavigationCacheWhenIdle(viewerId, workspaceKey, data)
}

function trimNavigationStorage() {
  if (typeof localStorage === 'undefined') return
  try {
    const records: Array<{ key: string; bytes: number; at: string }> = []
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i)
      if (!key?.startsWith(storagePrefix)) continue
      const raw = localStorage.getItem(key) ?? ''
      if (raw.length * 2 > NAVIGATION_CACHE_MAX_BYTES) { localStorage.removeItem(key); i--; continue }
      try { records.push({ key, bytes: cacheStringBytes(raw), at: JSON.parse(raw).updatedAt ?? '' }) }
      catch { localStorage.removeItem(key); i-- }
    }
    let bytes = records.reduce((sum, record) => sum + record.bytes, 0)
    records.sort((a, b) => a.at.localeCompare(b.at))
    for (const record of records) {
      if (bytes <= NAVIGATION_CACHE_MAX_BYTES) break
      localStorage.removeItem(record.key)
      boundedMemory.delete(record.key)
      memory.delete(record.key)
      bytes -= record.bytes
    }
  } catch { /* Storage is optional. */ }
}

function rememberBounded(key: string, raw: string) {
  boundedMemory.delete(key)
  boundedMemory.set(key, raw)
  let bytes = [...boundedMemory.values()].reduce((sum, value) => sum + cacheStringBytes(value), 0)
  while (bytes > NAVIGATION_CACHE_MAX_BYTES && boundedMemory.size) {
    const oldest = boundedMemory.keys().next().value!
    bytes -= cacheStringBytes(boundedMemory.get(oldest)!)
    boundedMemory.delete(oldest)
  }
}
// A hydrated snapshot has not been confirmed by a bootstrap yet. It may be used
// for the first paint, but it must never replace a server snapshot on disk.
const hydratedSnapshots = new WeakSet<BootstrapData>()

export function markNavigationHydrated<T extends BootstrapData>(data: T): T {
  hydratedSnapshots.add(data)
  return data
}

export function isNavigationHydrated(data: BootstrapData | null | undefined): boolean {
  return Boolean(data && hydratedSnapshots.has(data))
}

/** Keep a merged snapshot classified as cached until a real bootstrap replaces it. */
export function carryNavigationHydration(from: BootstrapData | null | undefined, to: BootstrapData): BootstrapData {
  return isNavigationHydrated(from) ? markNavigationHydrated(to) : to
}

function storageKey(viewerId: string, workspaceKey: string) {
  return `${storagePrefix}${viewerId}:${workspaceKey}`
}

function isAuthorizedRecord(record: NavigationCacheRecord | undefined, viewerId: string, workspaceKey: string) {
  return Boolean(
    record
    && record.viewerId === viewerId
    && record.workspaceKey === workspaceKey
    && record.data.viewer?.id === viewerId
    && record.data.workspace?.urlKey === workspaceKey,
  )
}

function stripIssue(issue: Issue): Issue {
  const preview: Partial<Issue> = {
    ...issue,
    isSummary: true,
    needsDetailRefresh: true,
  }
  delete preview.description
  delete preview.descriptionState
  delete preview.documentContent
  return preview as Issue
}

/** Directory snapshot safe to reuse as left-nav chrome. Issue bodies stay out of storage. */
export function toNavigationSnapshot(data: BootstrapData): BootstrapData {
  if (boundedBrowserCacheEnabled()) {
    // Only navigation fields survive. Preserve empty collection shapes for the
    // existing adapter, and keep every detail surface behind its bootstrap.
    const omitted = Object.fromEntries(Object.entries(data).map(([key, value]) => [key, Array.isArray(value) ? [] : value && typeof value === 'object' ? {} : undefined])) as unknown as BootstrapData
    return {
      ...omitted,
      workspace: data.workspace, viewer: data.viewer, viewerRole: data.viewerRole,
      resourceDetailsOmitted: true, issueCollectionPaged: true,
      users: data.users, teams: data.teams, states: data.states, labels: data.labels, labelGroups: data.labelGroups,
      favorites: data.favorites ?? [], favoriteFolders: data.favoriteFolders ?? [],
      teamResourceSections: data.teamResourceSections ?? [], teamPinnedResources: data.teamPinnedResources ?? [],
      issues: (data.issues ?? []).map(issueCacheSummary),
      projects: [], initiatives: [], documents: [],
      comments: {}, activities: {}, projectUpdates: {}, initiativeUpdates: {}, issueHistoryCursors: {},
      notifications: [], agentSessions: [], workspaceSettings: data.workspaceSettings,
    }
  }
  return {
    ...data,
    issues: (data.issues ?? []).map(stripIssue),
    comments: {},
    activities: {},
    issueHistoryCursors: {},
    documents: (data.documents ?? []).map(document => ({ ...document, content: '', contentState: undefined, contentData: undefined })),
    documentContentDrafts: [],
    projectUpdates: {},
    initiativeUpdates: {},
    aiConversations: [],
    aiPromptProgress: [],
    agentActivities: [],
    // Conversations stay out of storage: a marked summary, so the restored list never passes for the chats.
    agentSessions: data.agentSessions?.map(agentSessionSummary) ?? [],
    // The inbox is server-owned: a notification deleted elsewhere must not
    // survive in this device's snapshot and reappear on the next reload.
    notifications: [],
    notificationDeliveries: [],
    integrationDeliveries: [],
    auditLog: [],
    trash: [],
    importJobs: [],
    exportJobs: [],
    migrationJobs: [],
  }
}

export function readNavigationCache(viewerId: string, workspaceKey: string): BootstrapData | undefined {
  if (!viewerId || !workspaceKey) return undefined
  const key = storageKey(viewerId, workspaceKey)
  if (boundedBrowserCacheEnabled()) {
    trimNavigationStorage()
    try {
      const raw = typeof localStorage === 'undefined' ? boundedMemory.get(key) : localStorage.getItem(key) ?? boundedMemory.get(key)
      if (!raw || cacheStringBytes(raw) > NAVIGATION_CACHE_MAX_BYTES) return undefined
      const record = JSON.parse(raw) as NavigationCacheRecord
      if (record.schema !== navigationSchema || !isAuthorizedRecord(record, viewerId, workspaceKey)) return undefined
      rememberBounded(key, raw)
      return markNavigationHydrated(record.data)
    } catch { boundedMemory.delete(key); return undefined }
  }
  if (typeof localStorage !== 'undefined') {
    try {
      const raw = localStorage.getItem(key)
      if (raw) {
        const record = JSON.parse(raw) as NavigationCacheRecord
        if (!isAuthorizedRecord(record, viewerId, workspaceKey)) {
          memory.delete(key)
          return undefined
        }
        memory.set(key, record)
        return record.data
      }
    } catch {
      memory.delete(key)
      return undefined
    }
  }
  const remembered = memory.get(key)
  return isAuthorizedRecord(remembered, viewerId, workspaceKey) ? remembered?.data : undefined
}

export function writeNavigationCache(viewerId: string, workspaceKey: string, data: BootstrapData): void {
  if (!viewerId || !workspaceKey || data.viewer.id !== viewerId || data.workspace.urlKey !== workspaceKey) return
  // A snapshot read from this cache is older than whatever the server holds.
  // Only a completed bootstrap (which produces a new, unmarked snapshot) may
  // replace the stored one.
  if (isNavigationHydrated(data)) return
  if (boundedBrowserCacheEnabled()) {
    const key = storageKey(viewerId, workspaceKey)
    try {
      const raw = JSON.stringify({ schema: navigationSchema, viewerId, workspaceKey, updatedAt: new Date().toISOString(), data: toNavigationSnapshot(data) } satisfies NavigationCacheRecord)
      boundedMemory.delete(key)
      memory.delete(key)
      if (typeof localStorage !== 'undefined') localStorage.removeItem(key)
      if (cacheStringBytes(raw) > NAVIGATION_CACHE_MAX_BYTES) return
      rememberBounded(key, raw)
      if (typeof localStorage !== 'undefined') {
        trimNavigationStorage()
        localStorage.setItem(key, raw)
        trimNavigationStorage()
      }
    } catch { /* Quota/private-mode failures must not block bootstrap. */ }
    return
  }
  const record: NavigationCacheRecord = {
    viewerId,
    workspaceKey,
    updatedAt: new Date().toISOString(),
    data: toNavigationSnapshot(data),
  }
  memory.set(storageKey(viewerId, workspaceKey), record)
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(storageKey(viewerId, workspaceKey), JSON.stringify(record))
  } catch {
    // Quota or private-mode failures must not block bootstrap.
  }
}

const pendingWrites = new Map<string, { data: BootstrapData; cancel: () => void }>()

/** Same as writeNavigationCache, but the snapshot is serialised when the browser is idle. */
export function writeNavigationCacheWhenIdle(viewerId: string, workspaceKey: string, data: BootstrapData): void {
  if (isNavigationHydrated(data)) return
  if (boundedBrowserCacheEnabled()) {
    // Pending idle work must not retain an entire bootstrap per workspace.
    cancelPendingWrites(storagePrefix)
    try {
      data = toNavigationSnapshot(data)
      if (cacheStringBytes(JSON.stringify(data)) > NAVIGATION_CACHE_MAX_BYTES) return
    } catch { return }
  }
  const key = storageKey(viewerId, workspaceKey)
  pendingWrites.get(key)?.cancel()
  const run = () => {
    pendingWrites.delete(key)
    writeNavigationCache(viewerId, workspaceKey, data)
  }
  let cancel: () => void
  if (typeof window !== 'undefined' && 'requestIdleCallback' in window) {
    const handle = window.requestIdleCallback(run, { timeout: 2000 })
    cancel = () => window.cancelIdleCallback(handle)
  } else {
    const handle = setTimeout(run, 0)
    cancel = () => clearTimeout(handle)
  }
  pendingWrites.set(key, { data, cancel })
}

function cancelPendingWrites(prefix: string, exact = false) {
  for (const [key, pending] of [...pendingWrites]) {
    if (exact ? key !== prefix : !key.startsWith(prefix)) continue
    pending.cancel()
    pendingWrites.delete(key)
  }
}

const lastWorkspaceKey = 'flow:last-workspace'

export interface RememberedWorkspace { viewerId: string; workspaceKey: string }

/** The workspace this viewer last opened on this device; the only key worth fetching before account confirms it. */
export function readRememberedWorkspace(): RememberedWorkspace | undefined {
  try {
    const value = JSON.parse(localStorage.getItem(lastWorkspaceKey) ?? 'null')
    if (typeof value?.viewerId === 'string' && typeof value?.workspaceKey === 'string' && value.viewerId && value.workspaceKey) return value
  } catch { /* unavailable or corrupt */ }
  return undefined
}

export function rememberWorkspace(viewerId: string, workspaceKey: string): void {
  try {
    const current = readRememberedWorkspace()
    if (current?.viewerId === viewerId && current.workspaceKey === workspaceKey) return
    localStorage.setItem(lastWorkspaceKey, JSON.stringify({ viewerId, workspaceKey }))
  } catch { /* private mode */ }
}

export function clearNavigationCache(viewerId?: string, workspaceKey?: string): void {
  if (boundedBrowserCacheEnabled()) invalidationGeneration++
  clearSessionCaches(viewerId, workspaceKey)
  cancelPendingWrites(viewerId && workspaceKey ? storageKey(viewerId, workspaceKey) : viewerId ? `${storagePrefix}${viewerId}:` : storagePrefix, boundedBrowserCacheEnabled() && Boolean(viewerId && workspaceKey))
  const boundedPrefix = viewerId ? workspaceKey ? storageKey(viewerId, workspaceKey) : `${storagePrefix}${viewerId}:` : storagePrefix
  for (const key of boundedMemory.keys()) if (viewerId && workspaceKey ? key === boundedPrefix : key.startsWith(boundedPrefix)) boundedMemory.delete(key)
  if (!viewerId) { try { localStorage.removeItem(lastWorkspaceKey) } catch { /* ignore */ } }
  if (viewerId && workspaceKey) {
    memory.delete(storageKey(viewerId, workspaceKey))
    if (typeof localStorage === 'undefined') return
    try { localStorage.removeItem(storageKey(viewerId, workspaceKey)) } catch { /* ignore */ }
    return
  }
  const prefix = viewerId ? `${storagePrefix}${viewerId}:` : storagePrefix
  for (const key of [...memory.keys()]) {
    if (key.startsWith(prefix)) memory.delete(key)
  }
  if (typeof localStorage === 'undefined') return
  try {
    for (let index = localStorage.length - 1; index >= 0; index -= 1) {
      const key = localStorage.key(index)
      if (key?.startsWith(prefix)) localStorage.removeItem(key)
    }
  } catch { /* ignore */ }
}

export function hydrateWorkspaceNavigation({
  current,
  cached,
  requestedWorkspaceKey,
  viewerId,
  preview,
}: {
  current: BootstrapData | null
  cached: BootstrapData | undefined
  requestedWorkspaceKey: string
  viewerId: string
  preview?: CachedIssuePreview | null
}): BootstrapData | null {
  if (current?.workspace.urlKey === requestedWorkspaceKey && (!boundedBrowserCacheEnabled() || current.viewer.id === viewerId)) return current
  if (!cached || cached.viewer.id !== viewerId || cached.workspace.urlKey !== requestedWorkspaceKey) return null
  // Older builds stored notifications here. They may already be deleted on the
  // server, so the inbox reconciles them from /api/notifications instead.
  const hydrated = { ...cached, notifications: [] }
  if (preview?.viewerId === viewerId && preview.key.startsWith(`${requestedWorkspaceKey}:`) && !preview.issue.isSummary) {
    return markNavigationHydrated({ ...hydrated, issues: mergeIssueRecords(hydrated.issues ?? [], [preview.issue]) })
  }
  return markNavigationHydrated(hydrated)
}

/** Cache hydration can make the workspace look loaded; keep sharing the in-flight bootstrap. */
export function workspaceBootstrapPhase(
  loadedWorkspaceKey: string | undefined,
  requestedWorkspaceKey: string,
  inFlightKey: string | null | undefined,
  requestKey: string,
): 'skip' | 'continue' {
  if (loadedWorkspaceKey !== requestedWorkspaceKey) return 'continue'
  return inFlightKey === requestKey ? 'continue' : 'skip'
}

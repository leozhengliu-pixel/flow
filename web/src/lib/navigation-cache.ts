import type { BootstrapData, Issue } from '@/types/flow'
import { mergeIssueRecords } from '@/lib/issue-detail-cache'

export interface NavigationCacheRecord {
  viewerId: string
  workspaceKey: string
  updatedAt: string
  data: BootstrapData
}

export interface CachedIssuePreview {
  viewerId: string
  key: string
  issue: Issue
}

const storagePrefix = 'flow:navigation:'
const memory = new Map<string, NavigationCacheRecord>()

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
    agentSessions: data.agentSessions?.map(session => ({ ...session, messages: [] })) ?? [],
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

function cancelPendingWrites(prefix: string) {
  for (const [key, pending] of [...pendingWrites]) {
    if (!key.startsWith(prefix)) continue
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
  cancelPendingWrites(viewerId && workspaceKey ? storageKey(viewerId, workspaceKey) : viewerId ? `${storagePrefix}${viewerId}:` : storagePrefix)
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
  if (current?.workspace.urlKey === requestedWorkspaceKey) return current
  if (!cached || cached.viewer.id !== viewerId || cached.workspace.urlKey !== requestedWorkspaceKey) return null
  if (preview?.viewerId === viewerId && preview.key.startsWith(`${requestedWorkspaceKey}:`) && !preview.issue.isSummary) {
    return { ...cached, issues: mergeIssueRecords(cached.issues ?? [], [preview.issue]) }
  }
  return cached
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

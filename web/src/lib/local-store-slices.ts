import type { BootstrapData, Issue } from '@/types/flow'
import { fetchIssueHistory, fetchIssueRecord, listIssueRecords, type IssueHistoryPage } from './api'
import { ApiError } from './api-client'
import { LocalStoreDB, retainedLocalAuthority, retainedLocalHead } from './local-store-db'
import { localStoreEnabled } from './local-store-model'
import { readLocalIssueManifest } from './local-store-resume'
import { localArchiveSliceKey, localHistorySliceKey, localSliceStamp, matchesSliceStamp, type LocalSlice, type LocalSliceKind } from './local-store-slice-model'

export type LocalSliceScope = { viewerId: string; workspaceId: string; workspaceKey: string }
type HistoryCursor = Pick<IssueHistoryPage, 'commentsCursor' | 'activitiesCursor'>

export function localSliceScope(data?: { viewer: Pick<BootstrapData['viewer'], 'id'>; workspace: Pick<BootstrapData['workspace'], 'id' | 'urlKey'> } | null): LocalSliceScope | undefined {
  if (!data || !localStoreEnabled()) return undefined
  const { id: workspaceId, urlKey: workspaceKey } = data.workspace
  const viewerId = data.viewer.id
  return retainedLocalHead(viewerId, workspaceId, workspaceKey) ? { viewerId, workspaceId, workspaceKey } : undefined
}

async function storeSlice<T>(db: LocalStoreDB, slice: LocalSlice<T>, growth: number, signal?: AbortSignal) {
  try {
    await db.writeWithinBudget(growth, budget => {
      signal?.throwIfAborted()
      return db.writeSlice(slice, budget)
    })
  } catch { /* The authorized network page still renders if storage fails. */ }
}

async function loadSlice<T>(scope: LocalSliceScope, kind: LocalSliceKind, key: string, load: () => Promise<T>, pagination: (value: T) => Pick<LocalSlice<T>, 'cursor' | 'complete'>, signal?: AbortSignal, refresh = false) {
  const { viewerId, workspaceId, workspaceKey } = scope
  const hint = retainedLocalHead(viewerId, workspaceId, workspaceKey)
  if (!hint) return load()
  const authority = readLocalIssueManifest(scope, false, signal).then(value => ({ value }), error => ({ error }))
  const value = await LocalStoreDB.withRetained(scope, async db => {
    const [stored, authorized] = await Promise.all([db.readSlice<T>(kind, key).catch(() => undefined), authority])
    signal?.throwIfAborted()
    if ('error' in authorized) {
      const error: unknown = authorized.error
      if (error instanceof ApiError && error.status === 401) throw error
      if (error instanceof ApiError && [403, 404].includes(error.status)) await db.discard('access-revoked')
      return load()
    }
    const stamp = localSliceStamp(authorized.value)
    if (hint.authzEpoch !== stamp.authzEpoch || stored && stored.meta.authzEpoch !== stamp.authzEpoch) {
      await db.discard('authority-changed')
      return load()
    }
    if (!stored || !db.valid) return load()
    const slice = stored.slice
    if (!refresh && slice && matchesSliceStamp(slice, stamp)) {
      if (Date.now() - slice.lastAccessAt > 60_000) {
        await storeSlice(db, { ...slice, lastAccessAt: Date.now() }, 0, signal)
      }
      signal?.throwIfAborted()
      if (db.valid) return slice.value
      return load()
    }
    // Stamp the page with the authority observed before its network read. A
    // later journal or realtime head forces the next reader back to network.
    const value = await load().catch(async error => {
      if (error instanceof ApiError && [403, 404].includes(error.status) && db.valid) {
        // A missing issue or archive can leave the workspace and its pending
        // edits authorized. Remove the rejected page and check that scope.
        await db.removeSlice(kind, key).catch(() => undefined)
        try {
          const current = await readLocalIssueManifest(scope, false, signal)
          if (current.authzEpoch !== stamp.authzEpoch) await db.discard('authority-changed')
        } catch (cause) {
          if (cause instanceof ApiError && [401, 403, 404].includes(cause.status)) {
            await db.discard(cause.status === 401 ? 'session-ended' : 'access-revoked')
          }
        }
      }
      throw error
    })
    signal?.throwIfAborted()
    const bytes = JSON.stringify(value).length * 2
    if (db.valid && bytes <= 2_000_000) {
      const now = Date.now()
      await storeSlice(db, { ...stamp, ...pagination(value), key, kind, value, bytes, loadedAt: now, lastAccessAt: now }, bytes - (slice?.bytes ?? 0), signal)
    }
    return value
  })
  return value ?? load()
}

export function loadLocalIssueHistory(id: string, scope?: LocalSliceScope, signal?: AbortSignal, cursors?: HistoryCursor, refresh?: boolean) {
  return readIssueHistory(id, scope, signal, cursors, refresh)
}

function readIssueHistory(id: string, scope?: LocalSliceScope, signal?: AbortSignal, cursors?: HistoryCursor, refresh?: boolean, onNetworkRead?: () => void) {
  if (!scope) return fetchIssueHistory(id, signal, cursors)
  const load = () => {
    onNetworkRead?.()
    return fetchIssueHistory(id, signal, cursors, scope.workspaceKey)
  }
  return loadSlice(scope, 'activity', localHistorySliceKey(id, cursors), load, ({ commentsCursor, activitiesCursor }) => ({
    cursor: { commentsCursor, activitiesCursor }, complete: !commentsCursor && !activitiesCursor,
  }), signal, refresh)
}

export type LocalIssueVisit = ReturnType<typeof preloadLocalIssueVisit>

export function preloadLocalIssueVisit(issue: Issue, scope: LocalSliceScope) {
  const { viewerId, workspaceId, workspaceKey } = scope
  const authority = () => retainedLocalAuthority(viewerId, workspaceId, workspaceKey)
  const token = authority()?.token
  const controller = new AbortController()
  const { signal } = controller
  const startRecord = issue.isSummary || issue.needsDetailRefresh ? fetchIssueRecord(issue.id, signal, workspaceKey) : undefined
  let finishLookup: () => void = () => {}
  const lookup = new Promise<void>(resolve => { finishLookup = resolve })
  let history: Promise<IssueHistoryPage> | undefined = readIssueHistory(issue.id, scope, signal, undefined, false, finishLookup)
  void history.then(finishLookup, finishLookup)
  const recordAfterLookup = async (value: Issue) => {
    // Mount the editor with its history when both reads are ready. A network
    // history miss or a slow local lookup must not block the editor.
    let timer: number | undefined
    try {
      await Promise.race([lookup, new Promise<void>(resolve => { timer = window.setTimeout(resolve, 40) })])
    } finally { clearTimeout(timer) }
    signal.throwIfAborted()
    if (!token || authority()?.token !== token) throw new ApiError('Sync authorization changed', 403)
    return value
  }
  let record = startRecord?.then(recordAfterLookup)
  // Route rendering may wait for a lazy chunk. The loaders can run during that
  // wait, with the same viewer and workspace checks as a direct navigation.
  void Promise.allSettled([record, history])
  const deadline = performance.now() + 1500
  const matches = (id: string, viewer: string, workspace: string) => id === issue.id && viewer === viewerId && workspace === workspaceKey &&
    performance.now() < deadline && token === authority()?.token && !signal.aborted
  return {
    abort: () => controller.abort(),
    takeRecord(id: string, viewer: string, workspace: string) {
      if (!matches(id, viewer, workspace)) return
      const result = record
      record = undefined
      return result
    },
    takeHistory(id: string, viewer: string, workspace: string) {
      if (!matches(id, viewer, workspace)) return
      const result = history
      history = undefined
      return result
    },
  }
}

export function loadLocalArchivePage(teamId: string, limit: number, cursor: string | undefined, scope: LocalSliceScope, signal?: AbortSignal, refresh?: boolean) {
  const load = () => listIssueRecords({ teamId, archived: 'true', limit, cursor, includeTotal: true, sort: 'updatedAt', direction: 'desc' }, signal, scope.workspaceKey)
  return loadSlice(scope, 'issue', localArchiveSliceKey(teamId, limit, cursor), load, ({ hasMore, nextCursor }) => ({
    cursor: nextCursor, complete: !hasMore,
  }), signal, refresh)
}

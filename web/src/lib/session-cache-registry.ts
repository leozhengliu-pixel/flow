/**
 * In-memory caches that hold one viewer's data (paged issue lists, ...) register a clearer here.
 * `clearNavigationCache` runs them on logout, account switch and workspace access loss, so a cache
 * never has to import (or be imported by) the navigation cache itself.
 */
export type SessionCacheClearer = (viewerId?: string, workspaceKey?: string) => void

/**
 * True when the whole session ends (logout, account switch): no workspace is named. A workspace-scoped clear
 * (access loss, sync reset) keeps the same viewer's in-flight work, so state with unsaved work only drops on this.
 */
export function endsSession(workspaceKey?: string): boolean {
  return workspaceKey === undefined
}

const clearers = new Set<SessionCacheClearer>()

export function registerSessionCacheClearer(clear: SessionCacheClearer): void {
  clearers.add(clear)
}

export function clearSessionCaches(viewerId?: string, workspaceKey?: string): void {
  for (const clear of clearers) clear(viewerId, workspaceKey)
}

/** Unsaved work (draft saves, queued writes) registers a flusher that resolves once it has been sent. */
export type SessionFlusher = () => Promise<unknown>

const flushers = new Set<SessionFlusher>()

export function registerSessionFlusher(flush: SessionFlusher): void {
  flushers.add(flush)
}

export const SESSION_FLUSH_BOUND_MS = 1500

/**
 * Sends what is still pending while the session cookie is valid, waiting at most `boundMs`. Whatever is left after the
 * bound is dropped by the session clearers, never sent as the next viewer. Call before the logout request.
 */
export async function flushBeforeSessionEnd(boundMs = SESSION_FLUSH_BOUND_MS): Promise<void> {
  if (!flushers.size) return
  let timer: ReturnType<typeof setTimeout> | undefined
  const work = Promise.allSettled([...flushers].map(flush => { try { return flush() } catch (error) { return Promise.reject(error) } }))
  await Promise.race([work, new Promise<void>(resolve => { timer = setTimeout(resolve, boundMs) })])
  if (timer !== undefined) clearTimeout(timer)
}

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

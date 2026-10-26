/**
 * In-memory caches that hold one viewer's data (paged issue lists, ...) register a clearer here.
 * `clearNavigationCache` runs them on logout, account switch and workspace access loss, so a cache
 * never has to import (or be imported by) the navigation cache itself.
 */
export type SessionCacheClearer = (viewerId?: string, workspaceKey?: string) => void

const clearers = new Set<SessionCacheClearer>()

export function registerSessionCacheClearer(clear: SessionCacheClearer): void {
  clearers.add(clear)
}

export function clearSessionCaches(viewerId?: string, workspaceKey?: string): void {
  for (const clear of clearers) clear(viewerId, workspaceKey)
}

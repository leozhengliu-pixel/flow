// Opt in until the complete permission and rolling-upgrade matrix has passed.
export const APPLIED_CURSOR_SCHEMA = 2
export const NAVIGATION_CACHE_MAX_BYTES = 2 * 1024 * 1024
export const ISSUE_DETAIL_CACHE_MAX_BYTES = 32 * 1024 * 1024

export function appliedRealtimeCursorEnabled() {
  return import.meta.env.VITE_APPLIED_REALTIME_CURSOR === 'true'
}

export function boundedBrowserCacheEnabled() {
  return import.meta.env.VITE_BOUNDED_BROWSER_CACHE === 'true'
}

/** Charge both UTF-16 storage and UTF-8 encoding, plus a small entry allowance. */
export function cacheStringBytes(value: string): number {
  return Math.max(value.length * 2, new TextEncoder().encode(value).byteLength) + 256
}

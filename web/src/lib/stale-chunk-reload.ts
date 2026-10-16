/**
 * A deploy renames every hashed chunk, so a page still running the previous build asks for files the server no
 * longer has. Reloading fetches the current index.html (served no-cache) and with it the current chunk names.
 */

const RELOAD_KEY = 'flow:stale-chunk-reload-at'
// A chunk that is still missing right after a reload is a real outage: show the error instead of looping.
const RELOAD_GUARD_MS = 30_000

const STALE_CHUNK_MESSAGES = [
  'Failed to fetch dynamically imported module', // Chromium
  'error loading dynamically imported module', // Firefox
  'Importing a module script failed', // Safari
  'Unable to preload CSS', // Vite CSS preload
]

export function isStaleChunkError(error: unknown) {
  const message = error instanceof Error ? error.message : String(error ?? '')
  return STALE_CHUNK_MESSAGES.some(text => message.includes(text))
}

/** Reload once for a stale-chunk failure; returns whether a reload was started. */
export function reloadForStaleChunk(error: unknown, now = Date.now()) {
  if (!isStaleChunkError(error)) return false
  try {
    const last = Number(sessionStorage.getItem(RELOAD_KEY))
    if (last && now - last < RELOAD_GUARD_MS) return false
    sessionStorage.setItem(RELOAD_KEY, String(now))
  } catch {
    // Without storage there is no loop guard, so leave the error visible.
    return false
  }
  window.location.reload()
  return true
}

export function installStaleChunkReload() {
  window.addEventListener('vite:preloadError', event => {
    if (reloadForStaleChunk((event as Event & { payload?: unknown }).payload)) event.preventDefault()
  })
}

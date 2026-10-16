import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { isStaleChunkError, reloadForStaleChunk } from './stale-chunk-reload'

describe('stale chunk reload', () => {
  const reload = vi.fn()
  beforeEach(() => {
    sessionStorage.clear()
    vi.stubGlobal('location', { ...window.location, reload })
  })
  afterEach(() => {
    reload.mockReset()
    vi.unstubAllGlobals()
  })

  it('recognizes chunk load failures from each browser, not ordinary errors', () => {
    expect(isStaleChunkError(new TypeError('Failed to fetch dynamically imported module: https://x/assets/a-1.js'))).toBe(true)
    expect(isStaleChunkError(new TypeError('error loading dynamically imported module: https://x/assets/a-1.js'))).toBe(true)
    expect(isStaleChunkError(new TypeError('Importing a module script failed.'))).toBe(true)
    expect(isStaleChunkError(new Error('Unable to preload CSS for /assets/a-1.css'))).toBe(true)
    expect(isStaleChunkError(new TypeError("Cannot read properties of undefined (reading 'id')"))).toBe(false)
    expect(isStaleChunkError(undefined)).toBe(false)
  })

  it('reloads once, then leaves a still-missing chunk visible instead of looping', () => {
    const error = new TypeError('Failed to fetch dynamically imported module')
    expect(reloadForStaleChunk(error, 1_000_000)).toBe(true)
    expect(reloadForStaleChunk(error, 1_005_000)).toBe(false)
    expect(reload).toHaveBeenCalledTimes(1)
    // A later deploy gets its own reload.
    expect(reloadForStaleChunk(error, 1_100_000)).toBe(true)
    expect(reload).toHaveBeenCalledTimes(2)
  })

  it('does not reload for unrelated errors', () => {
    expect(reloadForStaleChunk(new Error('boom'))).toBe(false)
    expect(reload).not.toHaveBeenCalled()
  })
})

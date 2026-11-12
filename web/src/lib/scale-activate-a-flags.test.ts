import { afterEach, describe, expect, it, vi } from 'vitest'

import { appliedRealtimeCursorEnabled, boundedBrowserCacheEnabled } from '@/lib/scale-p08'
import { syncBootstrapRequestKey, syncV2Enabled } from '@/lib/sync-client'

afterEach(() => vi.unstubAllEnvs())

describe('scale-activate-a flag defaults', () => {
  it('keeps all web defaults off pending the load gate and honors explicit opt-in', () => {
    vi.stubEnv('VITE_APPLIED_REALTIME_CURSOR', undefined)
    vi.stubEnv('VITE_BOUNDED_BROWSER_CACHE', undefined)
    vi.stubEnv('VITE_SYNC_V2', undefined)
    expect(appliedRealtimeCursorEnabled()).toBe(false)
    expect(boundedBrowserCacheEnabled()).toBe(false)
    expect(syncV2Enabled()).toBe(false)
    expect(syncBootstrapRequestKey('workspace', 'document-detail')).toBe('workspace')

    vi.stubEnv('VITE_APPLIED_REALTIME_CURSOR', 'true')
    vi.stubEnv('VITE_BOUNDED_BROWSER_CACHE', 'true')
    vi.stubEnv('VITE_SYNC_V2', 'true')
    expect(appliedRealtimeCursorEnabled()).toBe(true)
    expect(boundedBrowserCacheEnabled()).toBe(true)
    expect(syncV2Enabled()).toBe(true)
    expect(syncBootstrapRequestKey('workspace', 'document-detail')).toBe('workspace:sync:document-detail')

    for (const value of ['', 'false', 'off', '0', 'invalid']) {
      vi.stubEnv('VITE_APPLIED_REALTIME_CURSOR', value)
      vi.stubEnv('VITE_BOUNDED_BROWSER_CACHE', value)
      vi.stubEnv('VITE_SYNC_V2', value)
      expect(appliedRealtimeCursorEnabled()).toBe(false)
      expect(boundedBrowserCacheEnabled()).toBe(false)
      expect(syncV2Enabled()).toBe(false)
      expect(syncBootstrapRequestKey('workspace', 'document-detail')).toBe('workspace')
    }
  })
})

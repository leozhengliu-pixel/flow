import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchSurfaceBootstrap, syncClientFor } from '@/lib/sync-client'
import { realtimeChannelName } from '@/lib/shared-realtime'
import { APPLIED_CURSOR_SCHEMA } from '@/lib/scale-p08'
import { makeBootstrap } from '@/test/fixtures'

afterEach(() => { vi.unstubAllEnvs(); vi.unstubAllGlobals() })

describe('scale-activate-a rolling compatibility', () => {
  it('isolates the new applied stream from an old client of the same user', () => {
    expect(realtimeChannelName('workspace', 'viewer', APPLIED_CURSOR_SCHEMA)).not.toBe(realtimeChannelName('workspace', 'viewer'))
    expect(realtimeChannelName('workspace', 'viewer', APPLIED_CURSOR_SCHEMA)).not.toBe(realtimeChannelName('workspace', 'other-viewer', APPLIED_CURSOR_SCHEMA))
  })

  it.each([404, 405, 501])('keeps the entire old bootstrap against an old server returning %i', async status => {
    vi.stubEnv('VITE_APPLIED_REALTIME_CURSOR', 'true')
    vi.stubEnv('VITE_BOUNDED_BROWSER_CACHE', 'true')
    vi.stubEnv('VITE_SYNC_V2', 'true')
    const data = makeBootstrap()
    const legacy = vi.fn(async () => data)
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status })))
    expect(await fetchSurfaceBootstrap('workspace', 'document-detail', legacy)).toBe(data)
    expect(legacy).toHaveBeenCalledOnce()
    expect(syncClientFor(data)).toBeUndefined()
  })

  it('falls back without replacing unloaded collections when the new server lacks the complete capability', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true')
    const data = makeBootstrap()
    const fetch = vi.fn(async () => new Response(JSON.stringify({ schema: 2, capabilities: [], core: { workspace: data.workspace, viewer: data.viewer, omitted: ['teams', 'favorites', 'people', 'settings'] } })))
    vi.stubGlobal('fetch', fetch)
    expect(await fetchSurfaceBootstrap('workspace', 'issue-detail', async () => data)).toBe(data)
    expect(fetch).toHaveBeenCalledOnce()
  })

  it('preserves the old request path exactly after the web flag is rolled back', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'false')
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    const data = makeBootstrap()
    expect(await fetchSurfaceBootstrap('workspace', 'issue-detail', async () => data)).toBe(data)
    expect(fetch).not.toHaveBeenCalled()
  })
})

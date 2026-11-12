import { afterEach, describe, expect, it, vi } from 'vitest'
import { scaleP19CoreSupported, scaleP19ManifestMatches, scaleP19ProjectsEnabled, scaleP19Surface } from './scale-p19-surfaces'
import { makeBootstrap } from '@/test/fixtures'
import { fetchSurfaceBootstrap, migratedSyncSignal } from '@/lib/sync-client'
import type { SyncManifest } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchSyncManifest: vi.fn(), fetchSyncChanges: vi.fn() }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), ...api }))
afterEach(() => { vi.unstubAllEnvs(); vi.clearAllMocks() })

describe('scale-p19 surface gates and compatibility', () => {
  it('keeps scheduled Pulse side effects on the bounded catch-up path only for migrated pages', () => {
    expect(migratedSyncSignal('pulse.summary_scheduled', 'projects-page')).toBe(true)
    expect(migratedSyncSignal('pulse.summary_scheduled', 'initiatives-page')).toBe(true)
    expect(migratedSyncSignal('pulse.summary_scheduled', 'issue-detail')).toBe(false)
    expect(migratedSyncSignal('workspace.updated', 'projects-page')).toBe(false)
  })
  it('leaves every legacy route and bootstrap untouched with either client flag off', async () => {
    const legacy = vi.fn().mockResolvedValue(makeBootstrap())
    for (const [sync, projects] of [['false', 'false'], ['true', 'false'], ['false', 'true']]) {
      vi.stubEnv('VITE_SYNC_V2', sync); vi.stubEnv('VITE_PROJECT_QUERY_WINDOWS', projects)
      expect(scaleP19Surface('projects')).toBeUndefined()
      const result = await fetchSurfaceBootstrap('workspace', scaleP19Surface('projects'), legacy)
      expect(result).toBe(await legacy.mock.results[0].value)
      if (projects === 'false') expect(await fetchSurfaceBootstrap('workspace', 'projects-page', legacy)).toBe(result)
    }
    expect(api.fetchSyncManifest).not.toHaveBeenCalled()
  })

  it('requires the sync capability, irrespective of arrays or client flags', () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_PROJECT_QUERY_WINDOWS', 'true')
    expect(scaleP19ProjectsEnabled(makeBootstrap())).toBe(false)
    const data = makeBootstrap({ sync: { schema: 2, authzEpoch: 'epoch', head: '1', collections: {}, capabilities: [], revision: 0 } })
    expect(scaleP19ProjectsEnabled(data)).toBe(false)
    data.sync!.capabilities.push('sync-v2')
    expect(scaleP19ProjectsEnabled(data)).toBe(true)
  })

  it('rejects cross-workspace, principal and epoch substitutions even when other fields match', () => {
    const manifest = { schema: 2, workspaceId: 'workspace', viewerId: 'member', authzEpoch: 'epoch' } as SyncManifest
    expect(scaleP19ManifestMatches(manifest, { ...manifest })).toBe(true)
    for (const replacement of [{ workspaceId: 'other' }, { viewerId: 'admin' }, { authzEpoch: 'revoked' }]) expect(scaleP19ManifestMatches(manifest, { ...manifest, ...replacement })).toBe(false)
  })

  it('falls back for minimal P10 cores and for initiative queries without complete business contracts', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_PROJECT_QUERY_WINDOWS', 'true'); vi.stubEnv('VITE_INITIATIVE_QUERY_WINDOWS', 'true')
    const core = makeBootstrap()
    const manifest = { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, authzEpoch: 'epoch', head: '0', capabilities: ['sync-v2'], collections: {}, core: { workspace: core.workspace, viewer: core.viewer, viewerRole: core.viewerRole } } as SyncManifest
    api.fetchSyncManifest.mockResolvedValue(manifest)
    const legacy = vi.fn().mockResolvedValue(core)
    expect(scaleP19CoreSupported(manifest.core)).toBe(false)
    expect(await fetchSurfaceBootstrap('workspace', scaleP19Surface('projects'), legacy)).toBe(core)
    expect(await fetchSurfaceBootstrap('workspace', scaleP19Surface('initiatives'), legacy)).toBe(core)
    expect(legacy).toHaveBeenCalledTimes(2)
    expect(api.fetchSyncChanges).not.toHaveBeenCalled()
  })
})

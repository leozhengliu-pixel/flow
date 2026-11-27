import { afterEach, describe, expect, it, vi } from 'vitest'
import { queryCoreSupported, queryWindowManifestMatches, projectWindowsEnabled, queryWindowSyncSurface } from './query-window-surfaces'
import { makeBootstrap } from '@/test/fixtures'
import { fetchSurfaceBootstrap, migratedSyncSignal } from '@/lib/sync-client'
import type { SyncManifest } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchSyncManifest: vi.fn(), fetchSyncChanges: vi.fn() }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), ...api }))
afterEach(() => { vi.unstubAllEnvs(); vi.clearAllMocks() })

describe('surface gates and compatibility', () => {
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
      expect(queryWindowSyncSurface('projects')).toBeUndefined()
      const result = await fetchSurfaceBootstrap('workspace', queryWindowSyncSurface('projects'), legacy)
      expect(result).toBe(await legacy.mock.results[0].value)
      if (projects === 'false') expect(await fetchSurfaceBootstrap('workspace', 'projects-page', legacy)).toBe(result)
    }
    expect(api.fetchSyncManifest).not.toHaveBeenCalled()
  })

  it('requires the sync capability, irrespective of arrays or client flags', () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_PROJECT_QUERY_WINDOWS', 'true')
    expect(projectWindowsEnabled(makeBootstrap())).toBe(false)
    const data = makeBootstrap({ sync: { schema: 2, authzEpoch: 'epoch', head: '1', collections: {}, capabilities: [], revision: 0 } })
    expect(projectWindowsEnabled(data)).toBe(false)
    data.sync!.capabilities.push('sync-v2')
    expect(projectWindowsEnabled(data)).toBe(true)
  })

  it('requires explicit supporting catalogs so omitted templates, relations and customer choices stay on legacy', () => {
    const core = makeBootstrap({ projectTemplates: [], projectRelations: [], favoriteFolders: [], subscriptions: [], savedViews: [], favorites: [], agentSkills: [], invitations: [], customers: [], customerStatuses: [], customerTiers: [], customerRequests: [], userSettings: {}, teamSettings: {}, settings: {} })
    const collections = Object.fromEntries(['user', 'team', 'label', 'initiative', 'savedView', 'favorite'].map(kind => [kind, { revision: '1', complete: true }]))
    expect(queryCoreSupported(core, collections)).toBe(true)
    for (const field of ['projectTemplates', 'projectRelations', 'customers', 'favoriteFolders'] as const) {
      const omitted = { ...core, [field]: undefined }
      expect(queryCoreSupported(omitted, collections)).toBe(false)
    }
  })

  it('rejects cross-workspace, principal and epoch substitutions even when other fields match', () => {
    const manifest = { schema: 2, workspaceId: 'workspace', viewerId: 'member', authzEpoch: 'epoch' } as SyncManifest
    expect(queryWindowManifestMatches(manifest, { ...manifest })).toBe(true)
    for (const replacement of [{ workspaceId: 'other' }, { viewerId: 'admin' }, { authzEpoch: 'revoked' }]) expect(queryWindowManifestMatches(manifest, { ...manifest, ...replacement })).toBe(false)
  })

  it('falls back when the sync core omits required project and initiative catalogs', async () => {
    vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_PROJECT_QUERY_WINDOWS', 'true'); vi.stubEnv('VITE_INITIATIVE_QUERY_WINDOWS', 'true')
    const core = makeBootstrap()
    const manifest = { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, authzEpoch: 'epoch', head: '0', capabilities: ['sync-v2'], collections: {}, core: { workspace: core.workspace, viewer: core.viewer, viewerRole: core.viewerRole } } as SyncManifest
    api.fetchSyncManifest.mockResolvedValue(manifest)
    const legacy = vi.fn().mockResolvedValue(core)
    expect(queryCoreSupported(manifest.core)).toBe(false)
    expect(await fetchSurfaceBootstrap('workspace', queryWindowSyncSurface('projects'), legacy)).toBe(core)
    expect(await fetchSurfaceBootstrap('workspace', queryWindowSyncSurface('initiatives'), legacy)).toBe(core)
    expect(legacy).toHaveBeenCalledTimes(2)
    expect(api.fetchSyncChanges).not.toHaveBeenCalled()
  })
})

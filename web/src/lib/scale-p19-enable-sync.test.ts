import { describe, expect, it, vi } from 'vitest'
import { makeBootstrap, project } from '@/test/fixtures'
import { migratedSyncSignal, SyncClient } from './sync-client'
import { scaleP19WindowChanges } from './scale-p19-sync'
import type { SyncChanges, SyncManifest } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchSyncManifest: vi.fn() }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), ...api }))

describe('scale-p19 bounded core and history invalidations', () => {
  it('refreshes settings before advancing the applied cursor and preserves the entity window', async () => {
    const core = makeBootstrap({ projects: [], projectTemplates: [], projectRelations: [], savedViews: [], favorites: [], customers: [] })
    const manifest: SyncManifest = { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, authzEpoch: 'epoch', head: '0', capabilities: ['sync-v2', 'query-windows-v1'], collections: Object.fromEntries(['user', 'team', 'label', 'savedView', 'favorite'].map(kind => [kind, { complete: true, revision: '0' }])), core }
    const page: SyncChanges = { next: '10', head: '10', authzEpoch: 'epoch', hasMore: false, resyncRequired: false, transactions: [{ txId: 'settings', seq: '10', upserts: [], removals: [], invalidations: ['userSettings', 'projectUpdates'] }] }
    const client = new SyncClient(manifest, core.workspace.urlKey, { changes: vi.fn().mockResolvedValue(page), batch: vi.fn(), page: vi.fn() })
    client.surface = 'projects-page'
    client.install([{ key: { kind: 'project', id: project.id }, version: '1', completeness: 'summary', value: { ...project } }])
    const previous = client.adapt()
    let finish!: (manifest: SyncManifest) => void
    api.fetchSyncManifest.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const pending = client.catchUp()
    await vi.waitFor(() => expect(api.fetchSyncManifest).toHaveBeenCalled())
    expect(client.head).toBe('0')
    finish({ ...manifest, head: '12', core: { ...core, userSettings: { ...core.userSettings, [core.viewer.id]: { ...core.userSettings?.[core.viewer.id], language: 'zh-CN' } } } })
    await pending
    expect(client.head).toBe('10')
    expect(client.manifest.head).toBe('0')
    expect(client.disposed).toBe(false)
    expect(client.legacyRequired).toBe(false)
    const next = client.adapt(previous)
    expect(next.userSettings[core.viewer.id].language).toBe('zh-CN')
    expect(next.projects.map(item => item.id)).toContain(project.id)
    expect(client.get({ kind: 'project', id: project.id })?.version).toBe('1')
    client.dispose()
  })
  it('routes update bodies and relations into the owning query window invalidation', () => {
    const page: SyncChanges = { next: '9', head: '9', authzEpoch: 'epoch', hasMore: false, resyncRequired: false, transactions: [{ seq: '9', txId: 'updates', upserts: [{ key: { kind: 'projectUpdates', id: project.id }, version: '2', completeness: 'summary', value: { id: project.id, body: 'not retained' } }], removals: [], invalidations: ['initiativeUpdates', 'projectRelations'] }] }
    const result = scaleP19WindowChanges(page)
    expect(result.transactions[0].upserts).toEqual([])
    expect(result.transactions[0].invalidations.sort()).toEqual(['initiative', 'project'])
    expect(page.transactions[0].upserts).toHaveLength(1)
    expect(migratedSyncSignal('project_update.created', 'projects-page')).toBe(true)
    expect(migratedSyncSignal('user_settings.updated', 'initiatives-page')).toBe(true)
    expect(migratedSyncSignal('user_settings.updated', 'issue-detail')).toBe(false)
  })
})

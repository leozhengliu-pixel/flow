import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeBootstrap, project } from '@/test/fixtures'
import { SyncClient } from '@/lib/sync-client'
import { scaleP19InitiativeCatalogSupported } from '@/lib/scale-p19-surfaces'
import type { Initiative, SyncManifest } from '@/types/flow'
import { scaleP19InitiativeEntries, useScaleP19InitiativeWindow } from './scale-p19-initiative-window'
import { projectHasUpdates } from '@/lib/project-has-updates'

afterEach(() => vi.unstubAllEnvs())
const initiative = (id: string) => ({ id, name: id, parentInitiativeIds: [], projectIds: [], contributingTeamIds: [], labelIds: [] }) as unknown as Initiative
function scope(catalog: Initiative[], complete = true) {
  vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_INITIATIVE_QUERY_WINDOWS', 'true')
  const core = makeBootstrap({ initiatives: catalog, projects: [], favorites: [], savedViews: [], subscriptions: [], userSettings: {}, projectTemplates: [], projectRelations: [], favoriteFolders: [], agentSkills: [], invitations: [], customers: [], customerStatuses: [], customerTiers: [], customerRequests: [], teamSettings: {}, settings: {} })
  const manifest: SyncManifest = { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, core, authzEpoch: 'epoch', head: '0', capabilities: ['sync-v2'], collections: Object.fromEntries(['user', 'team', 'label', 'savedView', 'favorite', 'initiative', 'project'].map(kind => [kind, { revision: '1', complete: kind === 'project' ? complete : true }])) }
  const client = new SyncClient(manifest, core.workspace.urlKey)
  client.surface = 'initiatives-page'
  const data = client.adapt()
  const wrapper = ({ children }: { children: ReactNode }) => <WorkspaceStoreProvider data={data} session={null} account={null}>{children}</WorkspaceStoreProvider>
  return { wrapper, client, data }
}

describe('scale-p19 initiative windows', () => {
  it('keeps group headings and hierarchy order across an explicit 100-row boundary', () => {
    const entries = [{ kind: 'group' as const, key: 'group' }, ...Array.from({ length: 205 }, (_, index) => ({ kind: 'initiative' as const, key: `i-${index}`, initiative: initiative(`i-${index}`) }))]
    const second = scaleP19InitiativeEntries(entries, 1)
    expect(second[0]).toBe(entries[0])
    expect(second.filter(entry => entry.kind === 'initiative')).toHaveLength(100)
    expect(second[1].key).toBe('i-100')
    expect(scaleP19InitiativeEntries(entries, 2).map(entry => entry.key)).toEqual(['group', 'i-200', 'i-201', 'i-202', 'i-203', 'i-204'])
  })
  it('requires explicit completeness and resets pagination when filters change', async () => {
    const catalog = Array.from({ length: 205 }, (_, index) => initiative(`i-${index}`))
    const { wrapper, data } = scope(catalog)
    const entries = catalog.map(initiative => ({ kind: 'initiative' as const, initiative }))
    const hook = renderHook(({ query }) => useScaleP19InitiativeWindow(entries, query), { wrapper, initialProps: { query: 'active' } })
    expect(hook.result.current.enabled).toBe(true)
    expect(hook.result.current.page.total).toBe(205)
    act(() => hook.result.current.onPage(2))
    expect(hook.result.current.page.items).toHaveLength(5)
    hook.rerender({ query: 'planned' })
    expect(hook.result.current.page.index).toBe(0)
    expect(scaleP19InitiativeCatalogSupported(data, data.sync!.collections)).toBe(true)
    const missing = scope(catalog, false)
    renderHook(() => useScaleP19InitiativeWindow(entries, 'all'), { wrapper: missing.wrapper })
    await waitFor(() => expect(missing.client.legacyRequired).toBe(true))
  })
  it('never substitutes health for exact project update membership', () => {
    expect(projectHasUpdates({ ...project, health: 'onTrack' }, { [project.id]: [] })).toBe(false)
    expect(projectHasUpdates({ ...project, health: 'noUpdate' }, { [project.id]: [{ id: 'update' } as never] })).toBe(true)
    const { data } = scope([initiative('a')], false)
    expect(scaleP19InitiativeCatalogSupported(data, data.sync!.collections)).toBe(false)
  })
  it('leaves the legacy collection intact with the initiative page flag off', () => {
    const { wrapper } = scope([initiative('a')])
    vi.stubEnv('VITE_INITIATIVE_QUERY_WINDOWS', 'false')
    const entries = Array.from({ length: 205 }, (_, index) => ({ kind: 'initiative' as const, initiative: initiative(`i-${index}`) }))
    const hook = renderHook(() => useScaleP19InitiativeWindow(entries, 'all'), { wrapper })
    expect(hook.result.current.enabled).toBe(false)
    expect(hook.result.current.entries).toBe(entries)
  })
})

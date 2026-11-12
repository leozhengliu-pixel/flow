import { renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeBootstrap, project } from '@/test/fixtures'
import { SyncClient } from '@/lib/sync-client'
import type { SyncManifest } from '@/types/flow'
import { scaleP19DependencyOption, useScaleP19DependencyOptions } from './scale-p19-dependency-options'
import type { ProjectDependencyValue } from './project-dependency-picker'

const api = vi.hoisted(() => ({ listProjectRecords: vi.fn(), fetchSyncManifest: vi.fn() }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), ...api }))
afterEach(() => { vi.unstubAllEnvs(); vi.resetAllMocks() })

function scope() {
  vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_PROJECT_QUERY_WINDOWS', 'true')
  const core = makeBootstrap({ projects: [] })
  const manifest: SyncManifest = { schema: 2, core, workspaceId: core.workspace.id, viewerId: core.viewer.id, authzEpoch: 'epoch', head: '0', capabilities: ['sync-v2'], collections: {} }
  const client = new SyncClient(manifest, core.workspace.urlKey)
  const data = client.adapt()
  api.fetchSyncManifest.mockResolvedValue(manifest)
  const wrapper = ({ children }: { children: ReactNode }) => <WorkspaceStoreProvider data={data} session={null} account={null}>{children}</WorkspaceStoreProvider>
  return { wrapper, client, manifest }
}

describe('scale-p19 dependency selection windows', () => {
  it('resolves a selected template dependency outside the current window without retaining its body', async () => {
    const { wrapper } = scope()
    api.listProjectRecords.mockResolvedValue({ items: [{ ...project, id: 'outside', description: 'body'.repeat(10000) }], total: 1, hasMore: false })
    const values: ProjectDependencyValue[] = [{ projectId: 'outside', type: 'blocked_by' }]
    const hook = renderHook(() => useScaleP19DependencyOptions([], values), { wrapper })
    await waitFor(() => expect(hook.result.current.options[0]?.id).toBe('outside'))
    expect(hook.result.current.options[0].label).toBe(project.name)
    expect(hook.result.current.options[0]).not.toHaveProperty('description')
    expect(hook.result.current.options[0]).not.toHaveProperty('previewData')
    expect(api.listProjectRecords.mock.calls[0][0]).toMatchObject({ limit: 100, filter: [{ field: 'project', operator: 'is', values: ['outside'] }] })
    expect(values).toEqual([{ projectId: 'outside', type: 'blocked_by' }])
  })

  it('keeps the legacy options and makes no request with the page flag off', () => {
    const { wrapper } = scope()
    vi.stubEnv('VITE_PROJECT_QUERY_WINDOWS', 'false')
    const options = [{ id: 'known', label: 'Known', disabled: true }]
    const values: ProjectDependencyValue[] = [{ projectId: 'outside', type: 'blocks' }]
    const hook = renderHook(() => useScaleP19DependencyOptions(options, values), { wrapper })
    expect(hook.result.current.options).toEqual(options)
    expect(api.listProjectRecords).not.toHaveBeenCalled()
  })

  it('never publishes selected names returned under a revoked epoch', async () => {
    const { wrapper, client, manifest } = scope()
    api.listProjectRecords.mockResolvedValue({ items: [{ ...project, id: 'outside', name: 'Hidden name' }], total: 1, hasMore: false })
    api.fetchSyncManifest.mockResolvedValue({ ...manifest, authzEpoch: 'revoked' })
    const values: ProjectDependencyValue[] = [{ projectId: 'outside', type: 'blocked_by' }]
    const hook = renderHook(() => useScaleP19DependencyOptions([], values), { wrapper })
    await waitFor(() => expect(client.disposed).toBe(true))
    expect(hook.result.current.options).toEqual([])
  })

  it('preserves your/other project grouping using the authoritative lead and member IDs', () => {
    expect(scaleP19DependencyOption(project, project.lead!.id).group).toBe('your')
    expect(scaleP19DependencyOption({ ...project, lead: undefined, memberIds: ['viewer'] }, 'viewer').group).toBe('your')
    expect(scaleP19DependencyOption(project, 'outsider').group).toBe('other')
  })
})

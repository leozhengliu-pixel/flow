import { act, render, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ComponentProps } from 'react'
import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeBootstrap, project } from '@/test/fixtures'
import { SyncClient } from '@/lib/sync-client'
import type { BootstrapData, SyncManifest } from '@/types/flow'
import type { ProjectsDataViewProps } from './projects-data-view'
import type { ProjectsPageSurface } from './projects-page-surface'
import { DEFAULT_PROJECTS_DISPLAY } from './projects-display-model'

const captured = vi.hoisted(() => ({ data: undefined as ProjectsDataViewProps | undefined, surface: undefined as ComponentProps<typeof ProjectsPageSurface> | undefined }))
const api = vi.hoisted(() => ({ listProjectRecords: vi.fn(), fetchSyncManifest: vi.fn(), fetchSyncBatch: vi.fn() }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), ...api }))
vi.mock('./projects-data-view', () => ({ ProjectsDataView: (props: ProjectsDataViewProps) => { captured.data = props; return null } }))
vi.mock('./projects-page-surface', () => ({ ProjectsPageSurface: (props: ComponentProps<typeof ProjectsPageSurface>) => { captured.surface = props; return <>{props.children}</> } }))
vi.mock('./new-project-dialog', () => ({ NewProjectDialog: () => null }))
vi.mock('./project-updates-preview', () => ({ ProjectUpdatesPreview: () => null }))

import { ProjectsPage } from './projects-page'

function scope() {
  const core = makeBootstrap({ projects: [], favorites: [], subscriptions: [], savedViews: [] })
  const manifest: SyncManifest = { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, authzEpoch: 'epoch-1', head: '0', capabilities: ['sync-v2'], core, collections: { project: { revision: '1', complete: false } } }
  const client = new SyncClient(manifest, core.workspace.urlKey)
  client.surface = 'projects-page'
  api.fetchSyncManifest.mockResolvedValue(manifest)
  return { data: client.adapt(), client }
}

function page(data: BootstrapData, projects = data.projects) {
  return <I18nProvider><WorkspaceStoreProvider data={data} session={null} account={null}><ProjectsPage projects={projects} projectStatuses={data.projectStatuses} users={data.users} teams={data.teams} workspaceKey={data.workspace.urlKey}/></WorkspaceStoreProvider></I18nProvider>
}

const ids = () => captured.data!.groups.flatMap(group => group.projects.map(project => project.id))
beforeEach(() => {
  localStorage.clear()
  vi.stubEnv('VITE_SYNC_V2', 'true')
  vi.stubEnv('VITE_PROJECT_QUERY_WINDOWS', 'true')
  api.listProjectRecords.mockImplementation(async (query: { cursor?: string; limit?: number }) => {
    const start = Number(query.cursor ?? 0)
    return { items: Array.from({ length: query.limit ?? 100 }, (_, index) => ({ ...project, id: `p-${start + index}`, name: `Project ${start + index}`, position: start + index })), total: 23210, hasMore: true, nextCursor: String(start + 100) }
  })
  api.fetchSyncBatch.mockImplementation(async (_workspace: string, keys: Array<{ kind: string; id: string }>) => ({ authzEpoch: 'epoch-1', items: keys.map(key => ({ key, version: '1', completeness: 'summary', value: { ...project, id: key.id, name: `Project ${key.id.slice(2)}`, position: Number(key.id.slice(2)) } })) }))
})
afterEach(() => { vi.unstubAllEnvs(); vi.clearAllMocks() })

describe('scale-p19 ProjectsPage integration', () => {
  it('keeps legacy seed and append behavior exactly when the page flag is off', async () => {
    vi.stubEnv('VITE_PROJECT_QUERY_WINDOWS', 'false')
    const data = makeBootstrap({ projects: [] })
    const mounted = render(page(data))
    await waitFor(() => expect(ids()).toHaveLength(100))
    await act(async () => { await captured.data!.onLoadMore!() })
    expect(ids()).toHaveLength(200)
    expect(api.listProjectRecords.mock.calls[0][0].includeTotal).toBeUndefined()
    expect(api.fetchSyncManifest).not.toHaveBeenCalled()
    mounted.unmount()
    api.listProjectRecords.mockClear()
    render(page(data, Array.from({ length: 101 }, (_, index) => ({ ...project, id: `seed-${index}` }))))
    expect(ids()).toHaveLength(101)
    expect(api.listProjectRecords).not.toHaveBeenCalled()
  })

  it('ignores seed-array length and replaces rather than appending the next window', async () => {
    const { data } = scope()
    render(page(data, Array(150).fill(project)))
    await waitFor(() => expect(ids()[0]).toBe('p-0'))
    expect(ids()).toHaveLength(100)
    expect(api.listProjectRecords.mock.calls.every(([query]) => query.limit === 100)).toBe(true)
    expect(api.listProjectRecords.mock.calls.some(([query]) => query.includeTotal === true && query.limit === 100)).toBe(true)
    // The window pager owns advancement. Infinite-scroll append is disabled.
    expect(captured.data!.hasMore).toBe(false)
    const next = document.querySelector<HTMLButtonElement>('[aria-label="Next page"]')!
    await act(async () => { next.click() })
    await waitFor(() => expect(ids()[0]).toBe('p-100'))
    expect(ids()).toHaveLength(100)
    expect(document.querySelector('.scale-p19-project-pager')?.textContent).toContain('23210')
    act(() => captured.surface!.onFilterOpenChange!(true))
    await waitFor(() => expect(api.listProjectRecords.mock.calls.some(([query]) => query.limit === 1 && query.includeTotal)).toBe(true))
  })

  it('retains selected IDs for a bulk operation after changing pages', async () => {
    const { data } = scope()
    render(page(data))
    await waitFor(() => expect(ids()[0]).toBe('p-0'))
    act(() => captured.data!.onSelectionChange!(['p-0']))
    expect(document.querySelector('[aria-label="1 selected projects"]')).not.toBeNull()
    await act(async () => { document.querySelector<HTMLButtonElement>('[aria-label="Next page"]')!.click() })
    await waitFor(() => expect(ids()[0]).toBe('p-100'))
    expect(captured.data!.selectedIds).toEqual(['p-0'])
    expect(document.querySelector('[aria-label="1 selected projects"]')).not.toBeNull()
  })

  it('falls back before presenting unsupported timeline and global ordering semantics', async () => {
    const { data, client } = scope()
    render(page(data))
    await waitFor(() => expect(ids()[0]).toBe('p-0'))
    act(() => captured.surface!.onChangeDisplay!({ ...DEFAULT_PROJECTS_DISPLAY, layout: 'timeline' }))
    await waitFor(() => expect(client.legacyRequired).toBe(true))
    expect(client.disposed).toBe(true)
  })

  it('preserves a just-opened insights panel across the legacy bootstrap handoff', async () => {
    const { data, client } = scope()
    const mounted = render(page(data))
    await waitFor(() => expect(ids()[0]).toBe('p-0'))
    act(() => captured.surface!.onToggleSidebar!())
    await waitFor(() => expect(client.legacyRequired).toBe(true))
    mounted.unmount()
    render(page(makeBootstrap({ projects: [] })))
    await waitFor(() => expect(captured.surface!.sidebarOpen).toBe(true))
  })
})

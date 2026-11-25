import { act, render, screen, waitFor } from '@testing-library/react'
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
const api = vi.hoisted(() => ({ listProjectRecords: vi.fn(), fetchSyncManifest: vi.fn(), fetchSyncBatch: vi.fn(), readScaleP19Window: vi.fn() }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), ...api }))
vi.mock('@/lib/scale-p19-window-api', async original => ({ ...await original<typeof import('@/lib/scale-p19-window-api')>(), readScaleP19Window: api.readScaleP19Window }))
vi.mock('./projects-data-view', () => ({ ProjectsDataView: (props: ProjectsDataViewProps) => { captured.data = props; return null } }))
vi.mock('./projects-page-surface', () => ({ ProjectsPageSurface: (props: ComponentProps<typeof ProjectsPageSurface>) => { captured.surface = props; return <>{props.children}</> } }))
vi.mock('./new-project-dialog', () => ({ NewProjectDialog: () => null }))
vi.mock('./project-updates-preview', () => ({ ProjectUpdatesPreview: () => null }))

import { ProjectsPage } from './projects-page'

function scope() {
  const core = makeBootstrap({ projects: [], favorites: [], subscriptions: [], savedViews: [] })
  const manifest: SyncManifest = { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, authzEpoch: 'epoch-1', head: '0', capabilities: ['sync-v2', 'query-windows-v1'], core, collections: { project: { revision: '1', complete: false } } }
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
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubEnv('VITE_SYNC_V2', 'true')
  vi.stubEnv('VITE_PROJECT_QUERY_WINDOWS', 'true')
  api.listProjectRecords.mockImplementation(async (query: { cursor?: string; limit?: number }) => {
    const start = Number(query.cursor ?? 0)
    return { items: Array.from({ length: query.limit ?? 100 }, (_, index) => ({ ...project, id: `p-${start + index}`, name: `Project ${start + index}`, position: start + index })), total: 23210, hasMore: true, nextCursor: String(start + 100) }
  })
  api.fetchSyncBatch.mockImplementation(async (_workspace: string, keys: Array<{ kind: string; id: string }>) => ({ authzEpoch: 'epoch-1', items: keys.map(key => ({ key, version: '1', completeness: 'summary', value: { ...project, id: key.id, name: `Project ${key.id.slice(2)}`, position: Number(key.id.slice(2)) } })) }))
  api.readScaleP19Window.mockImplementation(async (_workspace, _kind, query, cursor) => {
    const page = await api.listProjectRecords({ cursor, limit: 100, includeTotal: true, filter: query.filters })
    return { ...page, schema: 1, revision: '99', versions: Object.fromEntries(page.items.map((item: { id: string }) => [item.id, '1'])), authzEpoch: 'epoch-1', offset: Number(cursor ?? 0), counts: { health: { 'on-track': 23210 } }, insights: { health: [{ value: 'on-track', label: 'On track', count: 23210 }] } }
  })
})
afterEach(() => { vi.unstubAllEnvs(); vi.unstubAllGlobals(); vi.clearAllMocks() })

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
    expect(api.listProjectRecords.mock.calls.some(([query]) => query.limit === 1)).toBe(false)
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

  it('shows optimistic edits and current summaries without another directory read', async () => {
    const { data, client } = scope()
    const mounted = render(page(data))
    await waitFor(() => expect(ids()[0]).toBe('p-0'))
    const reads = api.listProjectRecords.mock.calls.length
    const current = client.adapt(data)
    mounted.rerender(page({ ...current, projects: current.projects.map(item => item.id === 'p-0' ? { ...item, name: 'Optimistic name' } : item) }))
    expect(captured.data!.groups.flatMap(group => group.projects)[0].name).toBe('Optimistic name')
    act(() => client.install([{ key: { kind: 'project', id: 'p-0' }, version: '2', completeness: 'summary', value: { ...project, id: 'p-0', name: 'Server name' } }]))
    mounted.rerender(page(client.adapt(current)))
    expect(captured.data!.groups.flatMap(group => group.projects)[0].name).toBe('Server name')
    act(() => client.install([{ key: { kind: 'project', id: 'p-0' }, version: '3', completeness: 'deleted' }]))
    mounted.rerender(page(client.adapt(current)))
    expect(ids()).not.toContain('p-0')
    expect(api.listProjectRecords).toHaveBeenCalledTimes(reads)
  })

  it('updates off-page bulk names and hides deleted selections without refetching the page', async () => {
    const { data, client } = scope()
    const mounted = render(page(data))
    await waitFor(() => expect(ids()[0]).toBe('p-0'))
    act(() => captured.data!.onSelectionChange!(['p-0']))
    await act(async () => { document.querySelector<HTMLButtonElement>('[aria-label="Next page"]')!.click() })
    await waitFor(() => expect(ids()[0]).toBe('p-100'))
    const reads = api.listProjectRecords.mock.calls.length
    act(() => client.install([{ key: { kind: 'project', id: 'p-0' }, version: '2', completeness: 'summary', value: { ...project, id: 'p-0', name: 'Renamed off page' } }]))
    mounted.rerender(page(client.adapt(data)))
    act(() => document.querySelector<HTMLButtonElement>('[aria-label="Open project command menu"]')!.click())
    expect(screen.getByRole('dialog').textContent).toContain('Renamed off page')
    act(() => client.install([{ key: { kind: 'project', id: 'p-0' }, version: '3', completeness: 'deleted' }]))
    mounted.rerender(page(client.adapt(data)))
    await waitFor(() => expect(document.querySelector('[aria-label="1 selected projects"]')).toBeNull())
    expect(captured.data!.selectedIds).toEqual(['p-0'])
    expect(api.listProjectRecords).toHaveBeenCalledTimes(reads)
  })

  it('hides off-result selections after changing filters, as the legacy page does', async () => {
    const { data } = scope()
    render(page(data))
    await waitFor(() => expect(ids()[0]).toBe('p-0'))
    act(() => captured.data!.onSelectionChange!(['p-0']))
    expect(document.querySelector('[aria-label="1 selected projects"]')).not.toBeNull()
    api.listProjectRecords.mockResolvedValue({ items: [], total: 0, hasMore: false })
    act(() => captured.surface!.onAddFilter!('Status', { id: 'Planned', label: 'Planned' }))
    await waitFor(() => expect(captured.data!.loading).toBe(false))
    await waitFor(() => expect(document.querySelector('[aria-label="1 selected projects"]')).toBeNull())
    expect(captured.data!.selectedIds).toEqual(['p-0'])
  })

  it('queries timeline and global ordering without a legacy handoff', async () => {
    const { data, client } = scope()
    render(page(data))
    await waitFor(() => expect(ids()[0]).toBe('p-0'))
    act(() => captured.surface!.onChangeDisplay!({ ...DEFAULT_PROJECTS_DISPLAY, layout: 'timeline' }))
    await waitFor(() => expect(api.readScaleP19Window.mock.calls.some(([, , query]) => query.display.layout === 'timeline')).toBe(true))
    expect(client.legacyRequired).toBe(false)
    expect(client.disposed).toBe(false)
  })

  it('uses complete server insight counts in the open panel', async () => {
    const { data, client } = scope()
    const mounted = render(page(data))
    await waitFor(() => expect(ids()[0]).toBe('p-0'))
    act(() => captured.surface!.onToggleSidebar!())
    await waitFor(() => expect(captured.surface!.sidebarOpen).toBe(true))
    expect(screen.getByRole('complementary', { name: 'Project insights' })).toHaveTextContent('23210')
    expect(client.legacyRequired).toBe(false)
    mounted.unmount()
  })

  it('sends saved insight predicates to the server query', async () => {
    const { data, client } = scope()
    const savedView = { id: 'insight-view', name: 'At risk', filters: [{ kind: 'health', value: 'at-risk' }] } as unknown as NonNullable<ComponentProps<typeof ProjectsPage>['savedView']>
    render(<I18nProvider><WorkspaceStoreProvider data={data} session={null} account={null}><ProjectsPage projects={[]} projectStatuses={data.projectStatuses} users={data.users} teams={data.teams} workspaceKey={data.workspace.urlKey} savedView={savedView}/></WorkspaceStoreProvider></I18nProvider>)
    await waitFor(() => expect(api.readScaleP19Window.mock.calls.some(([, , query]) => query.insight?.value === 'at-risk')).toBe(true))
    expect(client.legacyRequired).toBe(false)
  })
})

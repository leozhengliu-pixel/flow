import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.hoisted(() => {
  // cmdk (the bulk Actions menu) measures its list.
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
  Element.prototype.scrollIntoView ??= () => {}
})
const api = vi.hoisted(() => ({ deleteReleasePipeline: vi.fn(), restoreTrashEntry: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { FeatureSettingsPage } from '@/components/settings/feature-settings'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, Release, ReleasePipeline, TrashEntry } from '@/types/flow'

import { pipelineRowSummaries } from './pipeline-settings-model'

function pipeline(overrides: Partial<ReleasePipeline> = {}): ReleasePipeline {
  return { id: 'p1', slugId: 'web', name: 'Web', teamIds: [], type: 'scheduled', production: true, stages: ['Planned'], stageStatuses: { Planned: 'planned' }, position: 0, pathFilters: [], autoGenerateReleaseNotes: false, createdAt: '', updatedAt: '', ...overrides } as ReleasePipeline
}

function release(overrides: Partial<Release>): Release {
  return { id: 'r', slugId: 'r', name: 'R', version: '', description: '', status: 'planned', position: 0, projectIds: [], issueIds: [], subscriberIds: [], resources: [], creator: makeBootstrap().viewer, createdAt: '', updatedAt: '', ...overrides } as Release
}

function Location() {
  const location = useLocation()
  return <output aria-label="location">{location.pathname + location.search}</output>
}

function renderList(data: BootstrapData, path = '/workspace/settings/releases') {
  const onOpen = vi.fn(), onCreate = vi.fn(), onReload = vi.fn().mockResolvedValue(undefined)
  render(<MemoryRouter initialEntries={[path]}><I18nProvider><Routes><Route path="*" element={<><FeatureSettingsPage page="releases" data={data} onCreateReleasePipeline={onCreate} onOpenReleasePipeline={onOpen} onOpenIntegration={vi.fn()} onReload={onReload}/><Location/></>}/></Routes></I18nProvider></MemoryRouter>)
  return { onOpen, onCreate, onReload }
}

beforeEach(() => {
  localStorage.setItem('flow:locale', 'en')
  api.deleteReleasePipeline.mockReset().mockResolvedValue(undefined)
  api.restoreTrashEntry.mockReset().mockResolvedValue(undefined)
})

describe('Settings › Releases', () => {
  it('shows Linear’s empty state with docs links', () => {
    renderList(makeBootstrap({ viewerRole: 'member', trash: [] }))
    expect(screen.getByRole('heading', { name: 'Releases' })).toBeVisible()
    expect(screen.getAllByRole('link', { name: /Docs/ })[0]).toHaveAttribute('target', '_blank')
    expect(screen.getByRole('heading', { name: 'Create release pipelines' })).toBeVisible()
    expect(screen.getByText(/Continuous pipelines capture changes as they deploy/)).toBeVisible()
    expect(screen.getByRole('link', { name: 'Documentation' })).toBeVisible()
    expect(screen.getByRole('combobox', { name: 'Pipeline status' })).toHaveTextContent('Active')
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('lists pipelines with production badge, team keys, counts and latest release', async () => {
    const base = makeBootstrap({ viewerRole: 'admin', trash: [] })
    const team = base.teams[0]
    const data = {
      ...base,
      releasePipelines: [pipeline({ teamIds: [team.id] }), pipeline({ id: 'p2', slugId: 'deploys', name: 'Deploys', type: 'continuous', production: false, position: 1 })],
      releases: [
        release({ id: 'a', pipelineId: 'p1', status: 'released', releasedAt: new Date(Date.now() - 2 * 86400000).toISOString() }),
        release({ id: 'b', pipelineId: 'p1' }),
        release({ id: 'c', pipelineId: 'p1', archivedAt: '2026-01-01T00:00:00Z' }),
      ],
    } as BootstrapData
    const { onOpen } = renderList(data)
    // Linear orders the list by name: Deploys, then Web.
    const [deploys, web] = screen.getAllByRole('row').slice(1)
    const rows = [web, deploys]
    expect(within(rows[0]).getByText('Production')).toBeVisible()
    expect(within(rows[0]).getByText(team.key)).toBeVisible()
    expect(within(rows[0]).getByRole('button', { name: 'View 2 releases' })).toHaveTextContent('2')
    expect(within(rows[0]).getByText('2d ago')).toBeVisible()
    expect(within(rows[1]).queryByText('Production')).toBeNull()
    expect(within(rows[1]).getByText('Continuous')).toBeVisible()
    await userEvent.click(within(rows[0]).getByText('Web'))
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' }))
    await userEvent.click(within(rows[0]).getByRole('button', { name: 'View 2 releases' }))
    expect(screen.getByLabelText('location')).toHaveTextContent('/workspace/pipeline/web/releases')
  })

  it('sorts by column and filters by name with a clear action', async () => {
    const data = { ...makeBootstrap({ viewerRole: 'admin', trash: [] }), releasePipelines: [pipeline({ name: 'Beta', position: 0 }), pipeline({ id: 'p2', slugId: 'alpha', name: 'Alpha', position: 1 })] } as BootstrapData
    renderList(data)
    const names = () => screen.getAllByRole('row').slice(1).map(row => row.querySelector('strong')?.textContent)
    expect(names()).toEqual(['Alpha', 'Beta'])
    await userEvent.click(screen.getByRole('button', { name: 'Order by Pipeline name' }))
    expect(names()).toEqual(['Beta', 'Alpha'])
    await userEvent.type(screen.getByRole('searchbox', { name: 'Filter by pipeline name' }), 'zzz')
    expect(screen.getByText('No matching pipelines')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Clear filters' }))
    expect(names()).toHaveLength(2)
  })

  it('duplicates into the new pipeline form and deletes after typing the name', async () => {
    const data = { ...makeBootstrap({ viewerRole: 'admin', trash: [] }), releasePipelines: [pipeline()] } as BootstrapData
    const { onReload } = renderList(data)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Open menu Web' }))
    expect(await screen.findByRole('menuitem', { name: 'View releases' })).toBeVisible()
    await user.click(screen.getByRole('menuitem', { name: 'Duplicate…' }))
    expect(screen.getByLabelText('location')).toHaveTextContent('/workspace/settings/releases/pipelines/new?copyFrom=web')
    await user.click(screen.getByRole('button', { name: 'Open menu Web' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const dialog = await screen.findByRole('dialog', { name: 'Delete the pipeline "Web"?' })
    expect(dialog).toHaveTextContent('Recently deleted pipelines')
    await user.type(within(dialog).getByRole('textbox', { name: 'Pipeline name' }), 'Web')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.deleteReleasePipeline).toHaveBeenCalledWith('p1'))
    expect(onReload).toHaveBeenCalled()
  })

  it('shows recently deleted pipelines from the URL filter and restores them', async () => {
    const entry: TrashEntry = { id: 'trash-1', resourceType: 'release_pipeline', resourceId: 'p9', title: 'Old', payload: { type: 'continuous', production: true, teamIds: [] }, deletedBy: makeBootstrap().viewer, deletedAt: new Date(Date.now() - 3600000).toISOString(), expiresAt: '' }
    const data = { ...makeBootstrap({ viewerRole: 'admin' }), trash: [entry] } as BootstrapData
    const { onReload } = renderList(data, '/workspace/settings/releases?display=deleted')
    expect(screen.getByText('Deleted pipelines are retained here for 30 days before being permanently deleted along with all their releases.')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'New pipeline' })).toBeNull()
    const row = screen.getAllByRole('row')[1]
    expect(within(row).getByText('Old')).toBeVisible()
    expect(within(row).getByText('1h ago')).toBeVisible()
    const user = userEvent.setup()
    await user.click(within(row).getByRole('button', { name: 'Open menu Old' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Restore pipeline' }))
    await waitFor(() => expect(api.restoreTrashEntry).toHaveBeenCalledWith('trash-1'))
    expect(onReload).toHaveBeenCalled()
  })

  it('summarizes counts and latest completion per pipeline', () => {
    const summaries = pipelineRowSummaries({ releases: [release({ pipelineId: 'x', status: 'released', releasedAt: '2026-01-02' }), release({ pipelineId: 'x', status: 'released', releasedAt: '2026-03-01' }), release({ pipelineId: 'x', archivedAt: '2026-01-01' })] })
    expect(summaries.get('x')).toEqual({ releaseCount: 2, latestCompletedAt: '2026-03-01' })
  })

  it('multi-selects pipelines and deletes them from the bulk bar', async () => {
    const data = { ...makeBootstrap({ viewerRole: 'admin', trash: [] }), releasePipelines: [pipeline(), pipeline({ id: 'p2', slugId: 'mobile', name: 'Mobile', position: 1 })] } as BootstrapData
    const { onReload, onOpen } = renderList(data)
    const user = userEvent.setup()
    const [mobile, web] = screen.getAllByRole('row').slice(1)
    await user.click(within(mobile).getByRole('checkbox', { name: 'Select pipeline' }))
    expect(onOpen).not.toHaveBeenCalled()
    // With a selection, clicking a row toggles it instead of opening it.
    await user.click(within(web).getByText('Web'))
    expect(onOpen).not.toHaveBeenCalled()
    const bar = screen.getByRole('toolbar', { name: '2 selected' })
    await user.click(within(bar).getByRole('button', { name: /Actions/ }))
    await user.click(await screen.findByRole('option', { name: 'Delete 2 pipelines' }))
    const dialog = await screen.findByRole('dialog', { name: 'Delete 2 pipelines?' })
    await user.type(within(dialog).getByRole('textbox', { name: 'Confirmation' }), 'delete')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.deleteReleasePipeline).toHaveBeenCalledTimes(2))
    expect(onReload).toHaveBeenCalled()
  })

  it('clears the selection from the bulk bar', async () => {
    const data = { ...makeBootstrap({ viewerRole: 'admin', trash: [] }), releasePipelines: [pipeline()] } as BootstrapData
    renderList(data)
    const user = userEvent.setup()
    await user.click(screen.getByRole('checkbox', { name: 'Select pipeline' }))
    await user.click(screen.getByRole('button', { name: 'Clear selection' }))
    expect(screen.queryByRole('toolbar')).toBeNull()
  })
})

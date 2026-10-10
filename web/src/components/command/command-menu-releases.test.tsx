import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'
import { resetCommandContext, useRegisterCommandContext, type CommandContext } from './command-context'
import { CommandMenu } from './command-menu'

const api = vi.hoisted(() => ({ searchWorkspace: vi.fn(), setIssueReleases: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api }))

const issue = makeIssue()
const pipeline = { id: 'pipe-1', slugId: 'web-app', name: 'Web app', type: 'scheduled', teamIds: [], stages: ['Planned'], stageStatuses: { Planned: 'planned' } } as unknown as ReleasePipeline
const ci = { id: 'pipe-2', slugId: 'api', name: 'API', type: 'continuous', teamIds: [], stages: [], stageStatuses: {} } as unknown as ReleasePipeline
const planned = { id: 'rel-1', slugId: 'v1', name: 'v1', version: '1.0.0', status: 'planned', pipelineId: pipeline.id, issueIds: [], stage: 'Planned' } as unknown as Release
const started = { id: 'rel-2', slugId: 'v2', name: 'v2', version: '', status: 'inProgress', pipelineId: pipeline.id, issueIds: [issue.id], stage: 'Started' } as unknown as Release
const done = { id: 'rel-3', slugId: 'v0', name: 'Legacy', version: '', status: 'released', pipelineId: pipeline.id, issueIds: [], stage: 'Released' } as unknown as Release
const base = makeBootstrap({ issues: [issue], releasePipelines: [pipeline, ci], releases: [planned, started, done] } as Partial<BootstrapData>)

function Register({ context }: { context?: CommandContext }) { useRegisterCommandContext(context); return null }

function setup(options: { data?: BootstrapData; path?: string; context?: CommandContext; host?: boolean; initialReleasePicker?: boolean } = {}) {
  const host = { navigate: vi.fn(), createRelease: vi.fn() }
  const onReleasesChanged = vi.fn(async () => undefined)
  const noop = vi.fn()
  render(<MemoryRouter initialEntries={[options.path ?? '/workspace/my-issues']}><I18nProvider><Register context={options.context}/>
    <CommandMenu open data={options.data ?? base} initialReleasePicker={options.initialReleasePicker} releaseHost={options.host === false ? undefined : host} onReleasesChanged={onReleasesChanged} onOpenChange={noop}
      onCreateIssue={noop} onCreateIssueTemplate={noop} onCreateProject={noop} onCreateView={noop} onCreateInitiative={noop} onSearchWorkspace={noop}
      onNavigateInbox={noop} onNavigateMyIssues={noop} onNavigateProjects={noop} onNavigateInitiatives={noop} onNavigateViews={noop} onNavigateMembers={noop}
      onNavigateCustomers={noop} onNavigateAgent={noop} onOpenResult={noop}/></I18nProvider></MemoryRouter>)
  return { host, onReleasesChanged, user: userEvent.setup() }
}

describe('release commands (Linear: Go to release pipelines, Open release pipeline…, Create new release…)', () => {
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    api.searchWorkspace.mockResolvedValue({ results: [] })
    api.setIssueReleases.mockResolvedValue([])
  })
  afterEach(() => { resetCommandContext(); vi.unstubAllGlobals(); vi.clearAllMocks() })

  it('goes to the release pipelines and opens a pipeline from a sub-list', async () => {
    const { user, host } = setup()
    await user.click(screen.getByRole('option', { name: /Go to release pipelines/ }))
    expect(host.navigate).toHaveBeenCalledWith('/workspace/release-pipelines')
    resetCommandContext()
  })

  it('opens a pipeline from "Open release pipeline…"', async () => {
    const { user, host } = setup()
    await user.click(screen.getByRole('option', { name: /Open release pipeline…/ }))
    expect(screen.getByPlaceholderText('Open release pipeline…')).toBeVisible()
    await user.click(screen.getByRole('option', { name: 'API' }))
    expect(host.navigate).toHaveBeenCalledWith('/workspace/pipeline/api/releases')
  })

  it('"Create new release…" (N then R) lists only scheduled pipelines', async () => {
    const { user, host } = setup()
    const create = screen.getByRole('option', { name: /Create new release…/ })
    expect(create).toHaveTextContent('NthenR')
    await user.click(create)
    expect(screen.queryByRole('option', { name: 'API' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('option', { name: 'Web app' }))
    expect(host.createRelease).toHaveBeenCalledWith(pipeline)
  })

  it('opens straight on the pipeline picker for the N then R shortcut', () => {
    setup({ initialReleasePicker: true })
    expect(screen.getByPlaceholderText('Create new release…')).toBeVisible()
    expect(screen.getByRole('option', { name: 'Web app' })).toBeVisible()
  })

  it('on a pipeline page "Create new release" skips the picker and the pipeline commands appear', async () => {
    const { user, host } = setup({ path: '/workspace/pipeline/web-app/releases' })
    expect(screen.queryByRole('option', { name: /Create new release…/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'Web app' })).not.toBeInTheDocument()
    for (const name of ['View releases', 'Open archive', 'Pipeline settings', 'View recently deleted releases']) expect(screen.getByRole('option', { name })).toBeVisible()
    await user.click(screen.getByRole('option', { name: 'Open archive' }))
    expect(host.navigate).toHaveBeenCalledWith('/workspace/pipeline/web-app/releases/archived')
  })

  it('creates a release for the pipeline of the open release page', async () => {
    const { user, host } = setup({ path: '/workspace/pipeline/web-app/release/v1/issues' })
    await user.click(screen.getByRole('option', { name: /Create new release(?!…)/ }))
    expect(host.createRelease).toHaveBeenCalledWith(pipeline)
  })

  it('opens the pipeline settings and recently deleted releases', async () => {
    const { user, host } = setup({ path: '/workspace/pipeline/web-app/changelog' })
    await user.click(screen.getByRole('option', { name: 'Pipeline settings' }))
    expect(host.navigate).toHaveBeenLastCalledWith('/workspace/settings/releases/pipelines/web-app')
  })

  it('has no release commands for guests, without the releases feature or without a host', () => {
    setup({ data: { ...base, viewerRole: 'guest' } })
    expect(screen.queryByRole('option', { name: /release/i })).not.toBeInTheDocument()
  })

  it('hides them when the releases feature is off', () => {
    setup({ data: { ...base, workspaceSettings: { ...base.workspaceSettings, featureFlags: { releases: false } } } })
    expect(screen.queryByRole('option', { name: /release/i })).not.toBeInTheDocument()
  })

  it('hides them without a host', () => {
    setup({ host: false })
    expect(screen.queryByRole('option', { name: /release/i })).not.toBeInTheDocument()
  })
})

describe('issue release commands (Linear: Add to release…, Remove from release)', () => {
  const context: CommandContext = { kind: 'issues', source: 'detail', issues: [issue] }
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    api.searchWorkspace.mockResolvedValue({ results: [] })
    api.setIssueReleases.mockResolvedValue([])
  })
  afterEach(() => { resetCommandContext(); vi.unstubAllGlobals(); vi.clearAllMocks() })

  it('lists releases grouped Selected / In progress / Planned and adds the issue to another one', async () => {
    const { user, onReleasesChanged } = setup({ context })
    await user.click(screen.getByRole('option', { name: /Add to release…/ }))
    expect(screen.getByText('Selected releases')).toBeVisible()
    expect(screen.getByText('Planned')).toBeVisible()
    expect(screen.queryByRole('option', { name: /Legacy/ })).not.toBeInTheDocument()
    expect(screen.getByRole('option', { name: /v1 · 1.0.0/ })).toHaveTextContent('Web app')
    await user.click(screen.getByRole('option', { name: /v1 · 1.0.0/ }))
    await waitFor(() => expect(api.setIssueReleases).toHaveBeenCalledWith(issue.id, ['rel-2', 'rel-1']))
    expect(onReleasesChanged).toHaveBeenCalled()
  })

  it('removes the issue from a selected release by toggling it', async () => {
    const { user } = setup({ context })
    await user.click(screen.getByRole('option', { name: /Add to release…/ }))
    await user.click(screen.getByRole('option', { name: /v2/ }))
    await waitFor(() => expect(api.setIssueReleases).toHaveBeenCalledWith(issue.id, []))
  })

  it('finds completed releases once the query has three letters', async () => {
    const { user } = setup({ context })
    await user.click(screen.getByRole('option', { name: /Add to release…/ }))
    expect(screen.queryByRole('option', { name: /Legacy/ })).not.toBeInTheDocument()
    await user.type(document.querySelector<HTMLInputElement>('[cmdk-input]')!, 'leg')
    expect(screen.getByRole('option', { name: /Legacy/ })).toBeVisible()
  })

  it('offers "Remove from release" only through search', async () => {
    const { user } = setup({ context })
    expect(screen.queryByRole('option', { name: /Remove from release/ })).not.toBeInTheDocument()
    await user.type(document.querySelector<HTMLInputElement>('[cmdk-input]')!, 'remove')
    await user.click(screen.getByRole('option', { name: 'Remove from release' }))
    await waitFor(() => expect(api.setIssueReleases).toHaveBeenCalledWith(issue.id, []))
  })

  it('has no release actions when the workspace has no pipelines', () => {
    setup({ context, data: { ...base, releasePipelines: [], releases: [] } })
    expect(screen.queryByRole('option', { name: /Add to release|Remove from release/ })).not.toBeInTheDocument()
  })
})

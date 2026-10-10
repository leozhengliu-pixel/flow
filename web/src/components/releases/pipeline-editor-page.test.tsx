import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  createReleasePipeline: vi.fn(),
  updateReleasePipeline: vi.fn(),
  rotateReleasePipelineAccessKey: vi.fn(),
  revokeReleasePipelineAccessKey: vi.fn(),
  deleteReleasePipeline: vi.fn(),
}))
vi.mock('@/lib/api', async (importOriginal) => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, ReleasePipeline } from '@/types/flow'

import { PipelineEditorPage } from './pipeline-editor-page'
import { accessKeyState } from './pipeline-settings-model'

function pipelineFor(teamIds: string[], overrides: Partial<ReleasePipeline> = {}): ReleasePipeline {
  return {
    id: 'pipeline-1', slugId: 'app', name: 'App', teamIds, type: 'continuous', production: true,
    stages: ['Released'], stageStatuses: { Released: 'released' }, position: 0, pathFilters: [], autoGenerateReleaseNotes: false, createdAt: '', updatedAt: '',
    ...overrides,
  } as ReleasePipeline
}

function dataFor(role: BootstrapData['viewerRole'], teamRole?: 'owner' | 'member', pipelines: ReleasePipeline[] = []): BootstrapData {
  const base = makeBootstrap({ viewerRole: role })
  const teamId = base.teams[0].id
  return { ...base, releasePipelines: pipelines, teamSettings: {}, teamMembers: teamRole ? [{ teamId, userId: base.viewer.id, role: teamRole, joinedAt: '' }] : [] } as BootstrapData
}

function renderEditor(data: BootstrapData, pipeline?: ReleasePipeline, callbacks: Partial<{ onCreated: (pipeline: ReleasePipeline) => Promise<void>; onChanged: () => Promise<void>; onSaved: (pipeline: ReleasePipeline) => Promise<void> }> = {}, path = '/workspace/settings/releases/pipelines/new') {
  return render(<MemoryRouter initialEntries={[path]}><I18nProvider><PipelineEditorPage data={data} pipeline={pipeline} onCancel={vi.fn()} onSaved={callbacks.onSaved ?? vi.fn().mockResolvedValue(undefined)} onCreated={callbacks.onCreated} onChanged={callbacks.onChanged ?? vi.fn().mockResolvedValue(undefined)}/></I18nProvider></MemoryRouter>)
}

beforeEach(() => {
  localStorage.setItem('flow:locale', 'en')
  for (const fn of Object.values(api)) fn.mockReset()
})

describe('PipelineEditorPage permissions', () => {
  it('lets members create a pipeline', () => {
    renderEditor(dataFor('member'))
    expect(screen.getByRole('textbox', { name: 'Name' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Create pipeline' })).toBeInTheDocument()
  })

  it('lets owners of every pipeline team edit it', () => {
    const data = dataFor('member', 'owner')
    renderEditor(data, pipelineFor([data.teams[0].id]))
    expect(screen.queryByRole('note')).toBeNull()
    expect(screen.getByRole('textbox', { name: 'Name' })).toBeEnabled()
    // Linear autosaves pipeline settings: no Save button.
    expect(screen.queryByRole('button', { name: 'Save changes' })).toBeNull()
  })

  it('shows pipeline settings read-only to other members', () => {
    const data = dataFor('member', 'member')
    renderEditor(data, pipelineFor([data.teams[0].id]))
    expect(screen.getByRole('note')).toHaveTextContent('Only admins and team owners can modify this pipeline')
    expect(screen.getByRole('textbox', { name: 'Name' })).toBeDisabled()
    expect(screen.getByRole('checkbox', { name: 'Production' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Releases' })).toBeEnabled()
  })

  it('keeps pipelines without teams admin-managed', () => {
    const { unmount } = renderEditor(dataFor('member', 'owner'), pipelineFor([]))
    expect(screen.getByLabelText('Name')).toBeDisabled()
    unmount()
    renderEditor(dataFor('admin'), pipelineFor([]))
    expect(screen.getByLabelText('Name')).toBeEnabled()
  })
})

describe('new pipeline form', () => {
  it('shows only General and Stages like Linear, and hides stages for continuous pipelines', async () => {
    renderEditor(dataFor('admin'))
    expect(screen.getByRole('heading', { name: 'Create a new release pipeline' })).toBeVisible()
    expect(screen.getByRole('link', { name: /Docs/ })).toHaveAttribute('target', '_blank')
    expect(screen.getByText('Collect changes into a release and track its progress over time')).toBeVisible()
    expect(screen.getByText('Common for continuous delivery or frequent deploys')).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Stages' })).toBeVisible()
    for (const hidden of ['Completion', 'Release notes', 'CI setup']) expect(screen.queryByRole('heading', { name: hidden })).toBeNull()
    await userEvent.click(screen.getByRole('radio', { name: /Continuous/ }))
    expect(screen.queryByRole('heading', { name: 'Stages' })).toBeNull()
  })

  it('validates the name and creates with ⌘Enter, then opens the new pipeline', async () => {
    const created = pipelineFor([], { id: 'new', slugId: 'web', name: 'Web', type: 'scheduled' })
    api.createReleasePipeline.mockResolvedValue(created)
    const onCreated = vi.fn().mockResolvedValue(undefined)
    renderEditor(dataFor('admin'), undefined, { onCreated })
    const name = screen.getByRole('textbox', { name: 'Name' })
    fireEvent.change(name, { target: { value: 'x'.repeat(121) } })
    fireEvent.keyDown(name, { key: 'Enter', metaKey: true })
    expect(await screen.findByRole('alert')).toHaveTextContent('Name cannot exceed 120 characters.')
    expect(api.createReleasePipeline).not.toHaveBeenCalled()
    fireEvent.change(name, { target: { value: 'Web' } })
    fireEvent.keyDown(name, { key: 'Enter', ctrlKey: true })
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(created))
    expect(api.createReleasePipeline).toHaveBeenCalledWith(expect.objectContaining({ name: 'Web', type: 'scheduled', production: true, stages: ['Planned', 'In Progress', 'Released', 'Canceled'] }))
  })

  it('creates continuous pipelines without sending stages', async () => {
    api.createReleasePipeline.mockResolvedValue(pipelineFor([]))
    renderEditor(dataFor('admin'), undefined, { onCreated: vi.fn().mockResolvedValue(undefined) })
    fireEvent.change(screen.getByRole('textbox', { name: 'Name' }), { target: { value: 'Deploys' } })
    await userEvent.click(screen.getByRole('radio', { name: /Continuous/ }))
    await userEvent.click(screen.getByRole('button', { name: 'Create pipeline' }))
    await waitFor(() => expect(api.createReleasePipeline).toHaveBeenCalled())
    expect(api.createReleasePipeline.mock.calls[0][0]).not.toHaveProperty('stages')
  })

  it('prefills a duplicate from ?copyFrom', () => {
    const data = dataFor('admin')
    const source = pipelineFor([data.teams[0].id], { slugId: 'mobile', name: 'Mobile', type: 'scheduled', production: false, stages: ['Planned', 'Beta', 'Released', 'Canceled'], stageStatuses: { Planned: 'planned', Beta: 'inProgress', Released: 'released', Canceled: 'canceled' } })
    renderEditor({ ...data, releasePipelines: [source] }, undefined, {}, '/workspace/settings/releases/pipelines/new?copyFrom=mobile')
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('Mobile copy')
    expect(screen.getByRole('checkbox', { name: 'Production' })).not.toBeChecked()
    expect(screen.getByText('Beta')).toBeVisible()
  })
})

describe('pipeline settings', () => {
  it('autosaves the name on blur and toggles immediately', async () => {
    api.updateReleasePipeline.mockResolvedValue(pipelineFor([]))
    const onChanged = vi.fn().mockResolvedValue(undefined)
    renderEditor(dataFor('admin'), pipelineFor([]), { onChanged })
    const name = screen.getByRole('textbox', { name: 'Name' })
    fireEvent.change(name, { target: { value: 'App 2' } })
    fireEvent.blur(name)
    await waitFor(() => expect(api.updateReleasePipeline).toHaveBeenCalledWith('pipeline-1', { name: 'App 2' }))
    await userEvent.click(screen.getByRole('checkbox', { name: 'Production' }))
    await waitFor(() => expect(api.updateReleasePipeline).toHaveBeenCalledWith('pipeline-1', { production: false }))
    expect(onChanged).toHaveBeenCalled()
    // Continuous pipelines have no stages or completion settings.
    expect(screen.queryByRole('heading', { name: 'Stages' })).toBeNull()
    expect(screen.queryByRole('heading', { name: 'Completion' })).toBeNull()
  })

  it('edits the release notes template in a rich text editor and links CI docs instead of inline snippets', () => {
    renderEditor(dataFor('admin'), pipelineFor([], { releaseNotesTemplate: '## New' }))
    const template = screen.getByRole('group', { name: 'Template' })
    expect(within(template).getByRole('textbox', { name: 'Release notes template content' })).toHaveAttribute('contenteditable', 'true')
    expect(screen.getByRole('link', { name: /GitHub Actions/ })).toHaveAttribute('href', expect.stringContaining('release-automation.md#github-actions'))
    expect(screen.getByRole('link', { name: /Release API/ })).toHaveAttribute('target', '_blank')
    expect(screen.queryByText('/api/release-ci/sync')).toBeNull()
  })

  it('generates an access key once, then shows its status and enables path filters', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    api.rotateReleasePipelineAccessKey.mockResolvedValue({ pipelineId: 'pipeline-1', prefix: 'flow_release_abc', secret: 'flow_release_secret', createdAt: '2026-10-01T00:00:00Z' })
    const data = dataFor('admin')
    const { rerender } = renderEditor(data, pipelineFor([]))
    expect(screen.getByText('No access key has been generated yet.')).toBeVisible()
    expect(screen.getByRole('textbox', { name: 'Path filters' })).toBeDisabled()
    expect(screen.getByText(/Generate an access key to configure path filters/)).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Generate access key' }))
    expect(await screen.findByText('flow_release_secret')).toBeVisible()
    expect(writeText).toHaveBeenCalledWith('flow_release_secret')
    expect(screen.getByText('This access key will not be visible again. Please copy it now.')).toBeVisible()
    const withKey = pipelineFor([], { accessKeyPrefix: 'flow_release_abc', accessKeyCreatedAt: new Date().toISOString(), accessKeyLastUsedAt: new Date().toISOString() })
    rerender(<MemoryRouter><I18nProvider><PipelineEditorPage data={data} pipeline={withKey} onCancel={vi.fn()} onSaved={vi.fn()} onChanged={vi.fn().mockResolvedValue(undefined)}/></I18nProvider></MemoryRouter>)
    const keySection = screen.getByRole('group', { name: 'Access key' })
    expect(within(keySection).getByText('Active')).toBeVisible()
    expect(within(keySection).getByText(/Last used/)).toBeVisible()
    expect(screen.getByRole('textbox', { name: 'Path filters' })).toBeEnabled()
  })

  it('rotates with a grace period unless revoked immediately, and revokes', async () => {
    api.rotateReleasePipelineAccessKey.mockResolvedValue({ pipelineId: 'pipeline-1', prefix: 'p', secret: 'flow_release_next', createdAt: '' })
    api.revokeReleasePipelineAccessKey.mockResolvedValue(pipelineFor([]))
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
    const user = userEvent.setup()
    renderEditor(dataFor('admin'), pipelineFor([], { accessKeyPrefix: 'flow_release_abc', accessKeyCreatedAt: new Date().toISOString() }))
    await user.click(screen.getByRole('button', { name: 'Access key actions' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Rotate' }))
    const rotate = await screen.findByRole('dialog', { name: 'Rotate access key?' })
    expect(rotate).toHaveTextContent('continue working for 1 hour')
    await user.click(within(rotate).getByRole('checkbox', { name: 'Revoke old key immediately (no grace period)' }))
    await user.click(within(rotate).getByRole('button', { name: 'Rotate' }))
    await waitFor(() => expect(api.rotateReleasePipelineAccessKey).toHaveBeenCalledWith('pipeline-1', { revokeImmediately: true }))
    await user.click(screen.getByRole('button', { name: 'Access key actions' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Revoke' }))
    const revoke = await screen.findByRole('dialog', { name: 'Revoke access key?' })
    await user.click(within(revoke).getByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(api.revokeReleasePipelineAccessKey).toHaveBeenCalledWith('pipeline-1', false))
  })

  it('asks for the pipeline name before deleting', async () => {
    api.deleteReleasePipeline.mockResolvedValue(undefined)
    const onSaved = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderEditor(dataFor('admin'), pipelineFor([]), { onSaved })
    await user.click(screen.getByRole('button', { name: 'Pipeline options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const dialog = await screen.findByRole('dialog', { name: 'Delete the pipeline "App"?' })
    const confirm = within(dialog).getByRole('button', { name: 'Delete' })
    expect(confirm).toBeDisabled()
    await user.type(within(dialog).getByRole('textbox', { name: 'Pipeline name' }), 'App')
    await user.click(confirm)
    await waitFor(() => expect(api.deleteReleasePipeline).toHaveBeenCalledWith('pipeline-1'))
    expect(onSaved).toHaveBeenCalled()
  })
})

describe('access key state', () => {
  it('derives the access key state', () => {
    const now = Date.parse('2026-10-10T00:00:00Z')
    expect(accessKeyState({}, now)).toBe('none')
    expect(accessKeyState({ accessKeyPrefix: 'p' }, now)).toBe('active')
    expect(accessKeyState({ accessKeyPrefix: 'p', accessKeyRevokedAt: '2026-10-10T00:30:00Z' }, now)).toBe('expiring')
    expect(accessKeyState({ accessKeyPrefix: 'p', accessKeyRevokedAt: '2026-10-09T23:00:00Z' }, now)).toBe('none')
  })
})

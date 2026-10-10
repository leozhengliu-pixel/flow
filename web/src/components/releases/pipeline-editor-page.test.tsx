import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, ReleasePipeline } from '@/types/flow'

import { PipelineEditorPage } from './pipeline-editor-page'

function pipelineFor(teamIds: string[]): ReleasePipeline {
  return {
    id: 'pipeline-1', slugId: 'app', name: 'App', teamIds, type: 'continuous', production: true,
    stages: [], stageStatuses: {}, position: 0, pathFilters: [], createdAt: '', updatedAt: '',
  } as unknown as ReleasePipeline
}

function dataFor(role: BootstrapData['viewerRole'], teamRole?: 'owner' | 'member'): BootstrapData {
  const base = makeBootstrap({ viewerRole: role })
  const teamId = base.teams[0].id
  return { ...base, teamSettings: {}, teamMembers: teamRole ? [{ teamId, userId: base.viewer.id, role: teamRole, joinedAt: '' }] : [] } as BootstrapData
}

function renderEditor(data: BootstrapData, pipeline?: ReleasePipeline) {
  return render(<I18nProvider><PipelineEditorPage data={data} pipeline={pipeline} onCancel={vi.fn()} onSaved={vi.fn()}/></I18nProvider>)
}

describe('PipelineEditorPage permissions', () => {
  beforeEach(() => localStorage.setItem('flow:locale', 'en'))

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
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeInTheDocument()
  })

  it('shows pipeline settings read-only to other members', () => {
    const data = dataFor('member', 'member')
    renderEditor(data, pipelineFor([data.teams[0].id]))
    expect(screen.getByRole('note')).toHaveTextContent('Only admins and team owners can modify this pipeline')
    expect(screen.getByRole('textbox', { name: 'Name' })).toBeDisabled()
    expect(screen.getByRole('switch', { name: 'Production' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Save changes' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Back' })).toBeEnabled()
  })

  it('keeps pipelines without teams admin-managed', () => {
    const { unmount } = renderEditor(dataFor('member', 'owner'), pipelineFor([]))
    expect(screen.getByLabelText('Name')).toBeDisabled()
    unmount()
    renderEditor(dataFor('admin'), pipelineFor([]))
    expect(screen.getByLabelText('Name')).toBeEnabled()
  })
})

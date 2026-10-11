import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BootstrapData, DocumentTemplate } from '@/types/flow'

const api = vi.hoisted(() => ({
  fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn(),
  createDocumentTemplate: vi.fn(), updateDocumentTemplate: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api, realtimeClientId: () => 'document-templates-settings-test' }))

import { mentionFixture } from '@/components/editor/mentions/mention-fixtures'
import { makeIssue } from '@/test/fixtures'
import { FeatureSettingsPage } from './feature-settings'
import { Shell } from './mention-field-shell'
import { stubEditorDom } from './mention-field-test-kit'

const team = makeIssue().team
const noop = vi.fn()
const template = (over: Partial<DocumentTemplate>) => ({ id: 'dt', teamId: '', name: 'Template', description: '', title: '', content: '', createdAt: '', updatedAt: '', ...over }) as unknown as DocumentTemplate
const data = (templates: DocumentTemplate[]) => ({ ...mentionFixture(), teams: [team], documentTemplates: templates, projectTemplates: [], issueTemplates: [], viewerRole: 'admin', teamSettings: {} }) as unknown as BootstrapData
const page = (value: BootstrapData) => <Shell data={value}><FeatureSettingsPage page="documents" data={value} onCreateReleasePipeline={noop} onOpenReleasePipeline={noop} onOpenIntegration={noop} onNavigateSettings={noop} onReload={async () => {}}/></Shell>

describe('Settings > Documents templates', () => {
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    for (const mock of Object.values(api)) mock.mockReset()
    api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
    api.createDocumentTemplate.mockResolvedValue({})
    api.updateDocumentTemplate.mockResolvedValue({})
    stubEditorDom()
  })
  afterEach(() => { vi.unstubAllGlobals(); window.history.replaceState(null, '', '/') })

  it('lists only the workspace templates (team templates live in team settings), in the reference app\'s card', () => {
    render(page(data([template({ id: 'a', name: 'Company kickoff' }), template({ id: 'b', name: 'Team retro', teamId: team.id })])))
    expect(screen.getByRole('heading', { name: 'Documents' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Templates' })).toBeInTheDocument()
    expect(screen.getByText('Company kickoff')).toBeInTheDocument()
    expect(screen.queryByText('Team retro')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create new document template' })).toBeInTheDocument()
  })

  it('shows the empty state with a New template button', async () => {
    const user = userEvent.setup()
    render(page(data([])))
    expect(screen.getByText('No document templates')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Create new document template' }))
    expect(await screen.findByRole('heading', { name: 'New document template' })).toBeInTheDocument()
  })

  it('edits a workspace template in the full editor and saves it', async () => {
    const user = userEvent.setup()
    render(page(data([template({ id: 'a', name: 'Company kickoff', icon: 'Page' })])))
    await user.click(screen.getByRole('button', { name: 'Company kickoff' }))
    expect(await screen.findByRole('textbox', { name: 'Document template content' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.updateDocumentTemplate).toHaveBeenCalledWith('a', expect.objectContaining({ teamId: '', name: 'Company kickoff', icon: 'Page' })))
  })

  it('opens a new template editor from ⌘K (?newTemplate=1)', async () => {
    window.history.replaceState(null, '', '/workspace/settings/documents?newTemplate=1')
    render(page(data([])))
    expect(await screen.findByRole('textbox', { name: 'Template name' })).toHaveValue('')
    expect(screen.getByText('New document template')).toBeInTheDocument()
  })
})

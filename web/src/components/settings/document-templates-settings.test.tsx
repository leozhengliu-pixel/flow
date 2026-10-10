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

  it('lists workspace templates and, under their team, the team templates', () => {
    render(page(data([template({ id: 'a', name: 'Company kickoff' }), template({ id: 'b', name: 'Team retro', teamId: team.id })])))
    expect(screen.getByText('Company kickoff')).toBeInTheDocument()
    expect(screen.getByText('Team retro')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: team.name })).toBeInTheDocument()
  })

  it('edits a team template in the full editor and keeps its team on save', async () => {
    const user = userEvent.setup()
    render(page(data([template({ id: 'b', name: 'Team retro', teamId: team.id, icon: 'Page' })])))
    await user.click(screen.getByRole('button', { name: 'Edit: Team retro' }))
    expect(await screen.findByRole('textbox', { name: 'Document content' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.updateDocumentTemplate).toHaveBeenCalledWith('b', expect.objectContaining({ teamId: team.id, name: 'Team retro', icon: 'Page' })))
  })

  it('opens a new template editor from ⌘K (?newTemplate=1)', async () => {
    window.history.replaceState(null, '', '/workspace/settings/documents?newTemplate=1')
    render(page(data([])))
    expect(await screen.findByRole('textbox', { name: 'Template name' })).toHaveValue('')
    expect(screen.getByText('New document template')).toBeInTheDocument()
  })
})

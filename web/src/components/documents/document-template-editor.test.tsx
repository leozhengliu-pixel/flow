import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { stubEditorDom } from '@/components/settings/mention-field-test-kit'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { BootstrapData, DocumentTemplate } from '@/types/flow'
import { DocumentTemplateEditor } from './document-template-editor'

const api = vi.hoisted(() => ({ createDocumentTemplate: vi.fn(), updateDocumentTemplate: vi.fn(), deleteDocumentTemplate: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api, realtimeClientId: () => 'template-editor-test' }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
const confirm = vi.hoisted(() => ({ confirmTemplateAction: vi.fn() }))
vi.mock('@/components/settings/template-confirm', () => confirm)

const data = makeBootstrap({ documents: [], documentTemplates: [] } as unknown as Partial<BootstrapData>)
const saved = { id: 'tpl-1', teamId: 'team-1', name: 'Kickoff', description: 'For launches', title: 'Kickoff', icon: 'Page', content: 'Agenda', contentData: { type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'Agenda' }] }] }, creator: viewer, createdAt: '', updatedAt: '' } as unknown as DocumentTemplate

function mount(template: DocumentTemplate | null, teamId = '') {
  const handlers = { onClose: vi.fn(), onSaved: vi.fn().mockResolvedValue(undefined) }
  render(<I18nProvider><DocumentTemplateEditor data={data} teamId={teamId} template={template} {...handlers}/></I18nProvider>)
  return handlers
}

describe('DocumentTemplateEditor', () => {
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    stubEditorDom()
    api.createDocumentTemplate.mockResolvedValue({})
    api.updateDocumentTemplate.mockResolvedValue({})
    api.deleteDocumentTemplate.mockResolvedValue(undefined)
  })
  afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks() })

  it('lays out the reference app\'s full-page editor: back link, heading, page icon, name and a rich body', async () => {
    mount(null)
    expect(screen.getByRole('button', { name: 'Documents' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'New document template' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Document icon' })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Template name' })).toHaveAttribute('placeholder', 'Template name')
    expect(await screen.findByRole('textbox', { name: 'Document template content' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create' })).toBeEnabled()
    expect(screen.queryByLabelText('Document title')).not.toBeInTheDocument()
  })

  it('creates a workspace template with the typed body as Markdown, editor JSON and collaborative state', async () => {
    const user = userEvent.setup()
    const { onSaved, onClose } = mount(null)
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Weekly')
    await user.click(await screen.findByRole('textbox', { name: 'Document template content' }))
    await user.keyboard('Agenda items')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createDocumentTemplate).toHaveBeenCalled())
    const input = api.createDocumentTemplate.mock.calls[0][0]
    expect(input).toMatchObject({ teamId: '', name: 'Weekly', title: '', icon: '' })
    expect(input.content).toContain('Agenda items')
    expect(JSON.stringify(input.contentData)).toContain('Agenda items')
    expect(typeof input.contentState).toBe('string')
    await waitFor(() => expect(onSaved).toHaveBeenCalled())
    expect(onClose).toHaveBeenCalled()
  })

  it('does not save without a name and focuses the name field instead', async () => {
    const user = userEvent.setup()
    mount(null)
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(api.createDocumentTemplate).not.toHaveBeenCalled()
    expect(screen.getByRole('textbox', { name: 'Template name' })).toHaveFocus()
  })

  it('edits a team template, keeping its team, and leaves the body alone when it was not touched', async () => {
    const user = userEvent.setup()
    mount(saved, 'team-1')
    expect(screen.getByRole('heading', { name: 'Edit document template' })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Template name' })).toHaveValue('Kickoff')
    await user.clear(screen.getByRole('textbox', { name: 'Template name' }))
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Launch kickoff')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.updateDocumentTemplate).toHaveBeenCalledWith('tpl-1', expect.objectContaining({ teamId: 'team-1', name: 'Launch kickoff', icon: 'Page', content: 'Agenda' })))
    expect(api.updateDocumentTemplate.mock.calls[0][1]).not.toHaveProperty('contentData')
    expect(api.createDocumentTemplate).not.toHaveBeenCalled()
  })

  it('saves the colour picked for the template icon and shows it on the icon', async () => {
    const user = userEvent.setup()
    mount(saved, 'team-1')
    await user.click(screen.getByRole('button', { name: 'Document icon' }))
    if (!screen.queryByRole('radio', { name: 'Purple' })) await user.click(screen.getByRole('button', { name: 'Select default color' }))
    await user.click(screen.getByRole('radio', { name: 'Purple' }))
    expect(screen.getByRole('button', { name: 'Document icon' }).querySelector('svg')).toHaveStyle({ color: '#5e6ad2' })
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.updateDocumentTemplate).toHaveBeenCalledWith('tpl-1', expect.objectContaining({ icon: 'Page', color: '#5e6ad2' })))
  })

  it('deletes an existing template from its menu after confirming, then closes', async () => {
    const user = userEvent.setup()
    confirm.confirmTemplateAction.mockResolvedValue(true)
    const { onSaved, onClose } = mount(saved, 'team-1')
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    expect(confirm.confirmTemplateAction).toHaveBeenCalledWith(expect.objectContaining({ title: 'Delete the template "Kickoff"?', confirmLabel: 'Delete' }))
    await waitFor(() => expect(api.deleteDocumentTemplate).toHaveBeenCalledWith('tpl-1'))
    await waitFor(() => expect(onSaved).toHaveBeenCalled())
    expect(onClose).toHaveBeenCalled()
  })

  it('does not offer delete for a new template and cancels without saving', async () => {
    const user = userEvent.setup()
    const { onClose } = mount(null)
    expect(screen.queryByRole('button', { name: 'Open menu' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onClose).toHaveBeenCalled()
    expect(confirm.confirmTemplateAction).not.toHaveBeenCalled()
    expect(api.createDocumentTemplate).not.toHaveBeenCalled()
  })

  it('asks before discarding typed changes', async () => {
    const user = userEvent.setup()
    confirm.confirmTemplateAction.mockResolvedValue(false)
    const { onClose } = mount(null)
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Draft')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(confirm.confirmTemplateAction).toHaveBeenCalledWith(expect.objectContaining({ title: 'Discard changes?', confirmLabel: 'Discard' }))
    expect(onClose).not.toHaveBeenCalled()
  })
})

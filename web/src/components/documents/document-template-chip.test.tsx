import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { BootstrapData, DocumentTemplate, FlowDocument } from '@/types/flow'
import type { DocumentActionContext } from './document-actions'
import { DocumentTemplateChip } from './document-template-chip'
import { documentTemplateOptions, isNewEmptyDocument } from './document-template-options'

const api = vi.hoisted(() => ({ updateDocument: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api }))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const team = { id: 'team-1', key: 'TST', name: 'Test team', color: '#5e6ad2', icon: 'Team' }
const other = { id: 'team-2', key: 'OTH', name: 'Other team', color: '#5e6ad2', icon: 'Team' }
const template = (over: Partial<DocumentTemplate>): DocumentTemplate => ({ id: 't', teamId: '', name: 'T', title: '', icon: '', content: '', creator: viewer, createdAt: '', updatedAt: '', ...over }) as DocumentTemplate
const templates = [
  template({ id: 'ws', name: 'Workspace kickoff', title: 'Kickoff', icon: 'Page', content: '# Goals', contentState: 'yjs', contentData: { type: 'doc', content: [{ type: 'heading' }] } }),
  template({ id: 'mine', teamId: team.id, name: 'Team retro' }),
  template({ id: 'theirs', teamId: other.id, name: 'Other team plan' }),
]
const empty = (over: Partial<FlowDocument> = {}) => ({
  id: 'doc-1', slugId: 'untitled-abc123', title: '', content: '', contentData: { type: 'doc', content: [{ type: 'paragraph' }] }, creator: viewer, projectIds: [], teamIds: [team.id],
  subscriberIds: [], favorite: false, createdAt: '', updatedAt: '', revisions: [], ...over,
}) as FlowDocument

function ctx(): DocumentActionContext {
  const data = makeBootstrap({ teams: [team, other], teamMembers: [{ teamId: team.id, userId: viewer.id }], documentTemplates: templates, projects: [], documents: [] } as unknown as Partial<BootstrapData>)
  return { data, reload: vi.fn().mockResolvedValue(undefined), navigate: vi.fn(), t: value => value }
}
const mount = (document: FlowDocument, context = ctx()) => {
  render(<I18nProvider><DocumentTemplateChip ctx={context} document={document} className="document-template-chip"/></I18nProvider>)
  return context
}

describe('isNewEmptyDocument', () => {
  it('is true only without a title and without any body', () => {
    expect(isNewEmptyDocument(empty())).toBe(true)
    expect(isNewEmptyDocument(empty({ contentData: undefined }))).toBe(true)
    expect(isNewEmptyDocument(empty({ title: 'Plan' }))).toBe(false)
    expect(isNewEmptyDocument(empty({ content: 'Hello' }))).toBe(false)
    expect(isNewEmptyDocument(empty({ contentData: { type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'x' }] }] } }))).toBe(false)
    expect(isNewEmptyDocument(empty({ contentData: { type: 'doc', content: [{ type: 'heading' }] } }))).toBe(false)
  })
})

describe('DocumentTemplateChip', () => {
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    api.updateDocument.mockResolvedValue({})
  })
  afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks() })

  it('renders nothing for a document that has a title or a body', () => {
    mount(empty({ title: 'Plan' }))
    expect(screen.queryByRole('button', { name: 'Choose a template' })).not.toBeInTheDocument()
  })

  it('renders nothing once the body has content', () => {
    mount(empty({ content: 'Text' }))
    expect(screen.queryByRole('button', { name: 'Choose a template' })).not.toBeInTheDocument()
  })

  it('shows a Template chip on a new empty document', () => {
    mount(empty())
    const chip = screen.getByRole('button', { name: 'Choose a template' })
    expect(chip).toHaveTextContent('Template')
    expect(chip).toHaveClass('document-template-chip')
  })

  it('lists workspace templates and the document team templates, but not other teams', async () => {
    const user = userEvent.setup()
    mount(empty())
    await user.click(screen.getByRole('button', { name: 'Choose a template' }))
    expect(await screen.findByText('Workspace')).toBeInTheDocument()
    expect(screen.getByText('Test team')).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /Workspace kickoff/ })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /Team retro/ })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /Other team plan/ })).not.toBeInTheDocument()
  })

  it('applies the picked template (title, icon and Markdown body so the open editor reloads) and reloads data', async () => {
    const user = userEvent.setup()
    const context = mount(empty())
    await user.click(screen.getByRole('button', { name: 'Choose a template' }))
    await user.click(await screen.findByRole('option', { name: /Workspace kickoff/ }))
    await waitFor(() => expect(api.updateDocument).toHaveBeenCalledWith('doc-1', { title: 'Kickoff', icon: 'Page', content: '# Goals' }))
    await waitFor(() => expect(context.reload).toHaveBeenCalled())
  })

  it('falls back to the viewer teams when the document has no team', () => {
    const { options } = documentTemplateOptions(ctx(), empty({ teamIds: [] }))
    expect(options.map(option => option.label)).toEqual(['Workspace kickoff', 'Team retro'])
    expect(options.map(option => option.groupLabel)).toEqual(['Workspace', 'Test team'])
  })

  it('shows an empty state when there are no templates', async () => {
    const user = userEvent.setup()
    const context = ctx()
    context.data = { ...context.data, documentTemplates: [] }
    mount(empty(), context)
    await user.click(screen.getByRole('button', { name: 'Choose a template' }))
    expect(await screen.findByText('No templates')).toBeInTheDocument()
  })
})

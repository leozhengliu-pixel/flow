import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, DocumentTemplate, FlowDocument } from '@/types/flow'
import { CommandMenu } from './command-menu'
import { resetCommandContext, useRegisterCommandContext, type CommandContext } from './command-context'

const api = vi.hoisted(() => ({
  searchWorkspace: vi.fn(),
  listIssueRecords: vi.fn(),
  createDocument: vi.fn(),
  updateDocument: vi.fn(),
  deleteDocument: vi.fn(),
  replaceDocumentPermissions: vi.fn(),
  createDocumentTemplate: vi.fn(),
  createDocumentReminder: vi.fn(),
  createInitiativeResource: vi.fn(),
  pinTeamResource: vi.fn(),
  deleteTeamResource: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api }))
const favorites = vi.hoisted(() => ({ toggleFavoriteFor: vi.fn() }))
vi.mock('@/lib/favorites', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/favorites')>(), ...favorites }))
const dialogs = vi.hoisted(() => ({ confirmAction: vi.fn(), promptAction: vi.fn(), promptDateAction: vi.fn() }))
vi.mock('@/components/ui/action-dialog-service', () => dialogs)
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const team = { id: 'team-1', key: 'TST', name: 'Test team', color: '#5e6ad2', icon: 'Team' }
const second = { id: 'team-2', key: 'SEC', name: 'Second team', color: '#5e6ad2', icon: 'Team' }
const doc = (over: Partial<FlowDocument> = {}): FlowDocument => ({
  id: 'doc-1', slugId: 'plan-abc123', title: 'Launch plan', content: 'Body text', creator: viewer, projectIds: [], teamIds: [team.id], subscriberIds: [viewer.id],
  favorite: false, createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-02T00:00:00Z', revisions: [], ...over,
}) as FlowDocument
const template = (over: Partial<DocumentTemplate> = {}): DocumentTemplate => ({
  id: 'tpl-1', teamId: '', name: 'Weekly notes', title: 'Weekly', icon: 'Page', content: 'Agenda', contentState: 'state', contentData: { type: 'doc', content: [] },
  creator: viewer, createdAt: '', updatedAt: '', ...over,
}) as DocumentTemplate

const launch = doc()
const untitled = doc({ id: 'doc-2', slugId: 'untitled-def456', title: '', updatedAt: '2026-01-03T00:00:00Z', teamIds: [] })
const data = (over: Partial<BootstrapData> = {}) => makeBootstrap({
  documents: [launch, untitled], teams: [team, second], teamMembers: [{ teamId: team.id, userId: viewer.id }, { teamId: second.id, userId: viewer.id }],
  favorites: [], documentTemplates: [template(), template({ id: 'tpl-2', teamId: second.id, name: 'Retro' })], initiatives: [{ id: 'init-1', slugId: 'init', name: 'Big goal', color: '#5e6ad2', icon: 'Initiative', resources: [] }],
  teamPinnedResources: [], ...over,
} as unknown as Partial<BootstrapData>)

function Register({ context }: { context?: CommandContext }) { useRegisterCommandContext(context); return null }

function setup(context?: CommandContext, workspace = data(), path = '/workspace') {
  const host = { reload: vi.fn().mockResolvedValue(undefined), navigate: vi.fn() }
  const onOpenChange = vi.fn()
  const noop = vi.fn()
  function Harness() {
    const [open, setOpen] = useState(true)
    return <>
      <Register context={context}/>
      <CommandMenu open={open} onOpenChange={value => { onOpenChange(value); setOpen(value) }} data={workspace} documentHost={host}
        onCreateIssue={noop} onCreateIssueTemplate={noop} onCreateProject={noop} onCreateView={noop} onCreateInitiative={noop} onSearchWorkspace={noop}
        onNavigateInbox={noop} onNavigateMyIssues={noop} onNavigateProjects={noop} onNavigateInitiatives={noop} onNavigateViews={noop} onNavigateMembers={noop}
        onNavigateCustomers={noop} onNavigateAgent={noop} onOpenResult={noop}/>
    </>
  }
  render(<MemoryRouter initialEntries={[path]}><I18nProvider><Harness/></I18nProvider></MemoryRouter>)
  return { host, onOpenChange, user: userEvent.setup() }
}

const documentContext = (document = launch): CommandContext => ({ kind: 'document', document })
const labels = () => screen.getAllByRole('option').map(option => option.textContent ?? '')
const option = (name: RegExp | string) => screen.getByRole('option', { name })
const hint = (row: HTMLElement) => [...row.querySelectorAll('kbd')].map(key => key.textContent).join(' ')

describe('document commands in ⌘K', () => {
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    api.searchWorkspace.mockResolvedValue({ results: [] })
    api.listIssueRecords.mockResolvedValue({ items: [] })
    for (const mock of [api.createDocument, api.updateDocument, api.deleteDocument, api.replaceDocumentPermissions, api.createDocumentTemplate, api.createDocumentReminder, api.createInitiativeResource, api.pinTeamResource, api.deleteTeamResource]) mock.mockResolvedValue(doc({ id: 'created', slugId: 'untitled-999' }))
    dialogs.confirmAction.mockResolvedValue(true)
    dialogs.promptAction.mockResolvedValue('Renamed plan')
  })
  afterEach(() => { resetCommandContext(); vi.unstubAllGlobals(); vi.clearAllMocks() })

  describe('without a document context', () => {
    it('lists the global document commands with their shortcut hints and no document-scoped ones', () => {
      setup()
      expect(hint(option(/Open document…/))).toBe('O D')
      for (const name of ['Go to recently deleted documents', 'Create new document in…', 'Create new document template…', 'Create new document from template…', 'Add document to team overview…']) expect(option(name)).toBeInTheDocument()
      expect(screen.queryByRole('option', { name: /Change owner/ })).not.toBeInTheDocument()
      expect(screen.queryByRole('option', { name: /Copy document URL/ })).not.toBeInTheDocument()
    })

    it('opens a searchable document picker with an Untitled fallback and navigates to the pick', async () => {
      const { user, host } = setup()
      await user.click(option(/Open document…/))
      expect(screen.getByLabelText('Command context')).toHaveTextContent('Open document…')
      expect(labels().some(label => label.startsWith('Untitled'))).toBe(true)
      await user.type(document.querySelector<HTMLInputElement>('[cmdk-input]')!, 'Launch')
      expect(screen.queryByRole('option', { name: /Untitled/ })).not.toBeInTheDocument()
      await user.click(option(/Launch plan/))
      expect(host.navigate).toHaveBeenCalledWith('/workspace/document/plan-abc123')
    })

    it('opens straight on the picker for the O then D shortcut', () => {
      const host = { reload: vi.fn(), navigate: vi.fn() }
      const noop = vi.fn()
      render(<MemoryRouter><I18nProvider><CommandMenu open initialDocumentPicker data={data()} documentHost={host} onOpenChange={noop}
        onCreateIssue={noop} onCreateIssueTemplate={noop} onCreateProject={noop} onCreateView={noop} onCreateInitiative={noop} onSearchWorkspace={noop}
        onNavigateInbox={noop} onNavigateMyIssues={noop} onNavigateProjects={noop} onNavigateInitiatives={noop} onNavigateViews={noop} onNavigateMembers={noop}
        onNavigateCustomers={noop} onNavigateAgent={noop} onOpenResult={noop}/></I18nProvider></MemoryRouter>)
      expect(screen.getByLabelText('Command context')).toHaveTextContent('Open document…')
      expect(option(/Launch plan/)).toBeInTheDocument()
    })

    it('creates an empty-titled document under the picked team and opens it', async () => {
      const { user, host } = setup()
      await user.click(option(/Create new document in…/))
      expect(screen.getByText('My teams')).toBeInTheDocument()
      expect(screen.getByText('Projects')).toBeInTheDocument()
      expect(screen.getByText('Initiatives')).toBeInTheDocument()
      await user.click(option(/Second team/))
      await waitFor(() => expect(api.createDocument).toHaveBeenCalledWith({ title: '', teamIds: [second.id], projectIds: [] }))
      await waitFor(() => expect(host.navigate).toHaveBeenCalledWith('/workspace/document/untitled-999'))
    })

    it('creates a document in a project and links one created under an initiative', async () => {
      const first = setup()
      await first.user.click(option(/Create new document in…/))
      await first.user.click(option(new RegExp(project.name)))
      await waitFor(() => expect(api.createDocument).toHaveBeenCalledWith({ title: '', teamIds: [], projectIds: [project.id] }))
      document.body.innerHTML = ''
      const second = setup()
      await second.user.click(option(/Create new document in…/))
      await second.user.click(option(/Big goal/))
      await waitFor(() => expect(api.createInitiativeResource).toHaveBeenCalledWith('init-1', { type: 'document', documentId: 'created' }))
    })

    it('creates from a template: pick the template, then the parent, and sends templateId', async () => {
      const { user, host } = setup()
      await user.click(option(/Create new document from template…/))
      expect(screen.getByText('Workspace')).toBeInTheDocument()
      expect(screen.getByText('Second team')).toBeInTheDocument()
      await user.click(option(/Retro/))
      expect(screen.getByLabelText('Command context')).toHaveTextContent('Create new document in…')
      await user.click(option(/Test team/))
      await waitFor(() => expect(api.createDocument).toHaveBeenCalledWith({ title: '', teamIds: [team.id], projectIds: [], templateId: 'tpl-2' }))
      await waitFor(() => expect(host.navigate).toHaveBeenCalled())
    })

    it('adds a document to a team overview: pick the team, then a document that is not pinned yet', async () => {
      const { user } = setup(undefined, data({ teamPinnedResources: [{ id: 'pin-1', teamId: team.id, resourceType: 'document', resourceId: 'doc-1' }] } as unknown as Partial<BootstrapData>))
      await user.click(option(/Add document to team overview…/))
      await user.click(option(/Test team/))
      expect(screen.queryByRole('option', { name: /Launch plan/ })).not.toBeInTheDocument()
      await user.click(option(/Untitled/))
      await waitFor(() => expect(api.pinTeamResource).toHaveBeenCalledWith(team.id, { resourceType: 'document', resourceId: 'doc-2', title: '' }))
    })

    it('goes to the team page of recently deleted documents and opens the template settings', async () => {
      const first = setup()
      await first.user.click(option(/Go to recently deleted documents/))
      expect(first.host.navigate).toHaveBeenCalledWith('/workspace/team/TST/archive/recently-deleted-documents')
      document.body.innerHTML = ''
      const second = setup()
      await second.user.click(option(/Create new document template…/))
      expect(second.host.navigate).toHaveBeenCalledWith(expect.stringContaining('/workspace/settings/documents?newTemplate=1'))
    })
  })

  describe('with a document as the context', () => {
    it('shows the document chip and the scoped commands in Linear order with their shortcut hints', () => {
      setup(documentContext())
      expect(screen.getByLabelText('Command context')).toHaveTextContent('Launch plan')
      const rows = screen.getAllByRole('option')
      const names = rows.map(row => row.textContent ?? '')
      const expected = ['Change owner', 'Favorite document', 'Copy document URL', 'Copy document title', 'Copy title as link', 'Copy document content as Markdown', 'Remind me about this document…', 'Duplicate as new document', 'Document history', 'Change document subscribers…', 'Unsubscribe from document updates', 'Show author names', 'Rename document', 'Move to…', 'Pin to team…', 'Apply document template…', 'New template from document…', 'Delete document']
      expected.forEach((name, index) => expect(names[index]).toContain(name))
      const hints: Record<string, string> = { 'Change owner': 'Ctrl ⌘ O', 'Favorite document': '⌥ F', 'Copy document URL': '⌘ ⇧ ,', 'Copy document title': "⌘ ⇧ '", 'Copy title as link': '⌘ C', 'Copy document content as Markdown': '⌘ ⌥ C', 'Remind me about this document…': '⇧ H', 'Change document subscribers…': '⌘ ⇧ S', 'Unsubscribe from document updates': '⇧ S', 'Show author names': '⇧ A', 'Rename document': '⇧ R', 'Move to…': '⇧ P' }
      for (const [name, keys] of Object.entries(hints)) expect(hint(option(new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`)))).toBe(keys)
    })

    it('labels the favorite and subscribe commands by their current state', () => {
      setup(documentContext(doc({ favorite: true, subscriberIds: [] })), data({ documents: [doc({ favorite: true, subscriberIds: [] })] } as unknown as Partial<BootstrapData>))
      expect(option(/Unfavorite document/)).toBeInTheDocument()
      expect(option(/Subscribe to document updates/)).toBeInTheDocument()
    })

    it.each([
      ['Copy document URL', /Copy document URL/, `${window.location.origin}/workspace/document/plan-abc123`],
      ['Copy document title', /Copy document title/, 'Launch plan'],
      ['Copy title as link', /Copy title as link/, `[Launch plan](${window.location.origin}/workspace/document/plan-abc123)`],
      ['Copy document content as Markdown', /Copy document content as Markdown/, 'Body text'],
    ])('%s writes to the clipboard and closes the palette', async (_label, name, text) => {
      const { user, onOpenChange } = setup(documentContext())
      await user.click(option(name))
      await waitFor(async () => expect(await navigator.clipboard.readText()).toBe(text))
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })

    it('toggles favorite through the shared action', async () => {
      const { user } = setup(documentContext())
      await user.click(option(/Favorite document/))
      expect(favorites.toggleFavoriteFor).toHaveBeenCalledWith(expect.anything(), 'document', 'doc-1', true, false)
    })

    it('changes the owner from a page with No owner, Creator and Members', async () => {
      const { user } = setup(documentContext(), data({ users: [viewer, teammate] } as unknown as Partial<BootstrapData>))
      await user.click(option(/Change owner/))
      expect(screen.getByText('Creator')).toBeInTheDocument()
      expect(screen.getByText('Members')).toBeInTheDocument()
      await user.click(option(/Teammate/))
      await waitFor(() => expect(api.replaceDocumentPermissions).toHaveBeenCalledWith('doc-1', expect.arrayContaining([{ subjectType: 'user', subjectId: teammate.id, role: 'owner' }])))
    })

    it('clears the owner with "No owner"', async () => {
      const { user } = setup(documentContext(doc({ permissions: [{ id: 'p', documentId: 'doc-1', subjectType: 'user', subjectId: viewer.id, role: 'owner' }] as never })))
      await user.click(option(/Change owner/))
      await user.click(option(/No owner/))
      await waitFor(() => expect(api.replaceDocumentPermissions).toHaveBeenCalledWith('doc-1', [{ subjectType: 'user', subjectId: viewer.id, role: 'editor' }]))
    })

    it('sets a reminder from the presets page', async () => {
      const { user } = setup(documentContext())
      await user.click(option(/Remind me about this document…/))
      await user.click(screen.getAllByRole('option', { name: /Tomorrow|Next week|A month from now/ })[0])
      await waitFor(() => expect(api.createDocumentReminder).toHaveBeenCalledWith('doc-1', expect.stringMatching(/^\d{4}-\d{2}-\d{2}T/)))
    })

    it('duplicates as a new document and opens the copy', async () => {
      const { user, host } = setup(documentContext())
      await user.click(option(/Duplicate as new document/))
      await waitFor(() => expect(api.createDocument).toHaveBeenCalledWith(expect.objectContaining({ title: 'Launch plan (copy)' })))
      await waitFor(() => expect(host.navigate).toHaveBeenCalledWith('/workspace/document/untitled-999'))
    })

    it('opens the history on the document page, or navigates to it with ?history when no page handled it', async () => {
      const { user, host } = setup(documentContext())
      await user.click(option(/Document history/))
      expect(host.navigate).toHaveBeenCalledWith('/workspace/document/plan-abc123?history')
    })

    it('asks the open page for the history, subscribers and author names surfaces', async () => {
      const seen: string[] = []
      const listener = (event: Event) => { const detail = (event as CustomEvent).detail; seen.push(detail.action); detail.handled = true }
      window.addEventListener('flow:document-ui-action', listener)
      for (const name of [/Document history/, /Change document subscribers…/, /Show author names/]) {
        const { user, host } = setup(documentContext())
        await user.click(option(name))
        expect(host.navigate).not.toHaveBeenCalled()
        document.body.innerHTML = ''
        resetCommandContext()
      }
      window.removeEventListener('flow:document-ui-action', listener)
      expect(seen).toEqual(['history', 'subscribers', 'authors'])
    })

    it('unsubscribes the viewer', async () => {
      const { user } = setup(documentContext())
      await user.click(option(/Unsubscribe from document updates/))
      await waitFor(() => expect(api.updateDocument).toHaveBeenCalledWith('doc-1', { subscriberIds: [] }))
    })

    it('renames through the prompt dialog', async () => {
      const { user } = setup(documentContext())
      await user.click(option(/Rename document/))
      await waitFor(() => expect(api.updateDocument).toHaveBeenCalledWith('doc-1', { title: 'Renamed plan' }))
    })

    it('moves to a project (replacing the team) from the My teams / Projects / Initiatives page', async () => {
      const { user } = setup(documentContext())
      await user.click(option(/Move to…/))
      expect(screen.getByText('My teams')).toBeInTheDocument()
      expect(within(option(/Test team/)).getByLabelText('Current')).toBeInTheDocument()
      await user.click(option(new RegExp(project.name)))
      await waitFor(() => expect(api.updateDocument).toHaveBeenCalledWith('doc-1', { teamIds: [], projectIds: [project.id], issueId: '' }))
    })

    it('pins to a team overview', async () => {
      const { user } = setup(documentContext())
      await user.click(option(/Pin to team…/))
      await user.click(option(/Second team/))
      await waitFor(() => expect(api.pinTeamResource).toHaveBeenCalledWith(second.id, { resourceType: 'document', resourceId: 'doc-1', title: 'Launch plan' }))
    })

    it('applies a workspace or team template to the document', async () => {
      const { user } = setup(documentContext())
      await user.click(option(/Apply document template…/))
      expect(screen.getByText('Workspace')).toBeInTheDocument()
      await user.click(option(/Weekly notes/))
      await waitFor(() => expect(api.updateDocument).toHaveBeenCalledWith('doc-1', { title: 'Weekly', icon: 'Page', content: 'Agenda' }))
    })

    it('creates a workspace template from the document', async () => {
      const { user } = setup(documentContext())
      await user.click(option(/New template from document…/))
      await user.click(option(/Workspace/))
      await waitFor(() => expect(api.createDocumentTemplate).toHaveBeenCalledWith(expect.objectContaining({ teamId: '', name: 'Launch plan', content: 'Body text' })))
    })

    it('creates a team template from the document', async () => {
      const { user } = setup(documentContext())
      await user.click(option(/New template from document…/))
      await user.click(option(/Second team/))
      await waitFor(() => expect(api.createDocumentTemplate).toHaveBeenCalledWith(expect.objectContaining({ teamId: second.id })))
    })

    it('deletes after confirming and leaves the deleted document page for its team documents', async () => {
      const { user, host } = setup(documentContext(), data(), '/workspace/document/plan-abc123')
      await user.click(option(/Delete document/))
      await waitFor(() => expect(api.deleteDocument).toHaveBeenCalledWith('doc-1'))
      expect(dialogs.confirmAction).toHaveBeenCalledWith('Delete "Launch plan"?', expect.objectContaining({ description: expect.stringContaining('Recently deleted') }))
      await waitFor(() => expect(host.navigate).toHaveBeenCalledWith('/workspace/team/TST/documents'))
    })

    it('does nothing when the delete confirmation is declined', async () => {
      dialogs.confirmAction.mockResolvedValue(false)
      const { user } = setup(documentContext())
      await user.click(option(/Delete document/))
      await waitFor(() => expect(dialogs.confirmAction).toHaveBeenCalled())
      expect(api.deleteDocument).not.toHaveBeenCalled()
    })
  })

  describe('with several documents selected', () => {
    const many = (): CommandContext => ({ kind: 'document', document: launch, documents: [launch, untitled] })

    it('shows "N documents" and only the commands that apply to a selection', () => {
      setup(many())
      expect(screen.getByLabelText('Command context')).toHaveTextContent('2 documents')
      for (const gone of [/Rename document/, /Document history/, /Duplicate as new document/, /Copy title as link/, /Apply document template…/]) expect(screen.queryByRole('option', { name: gone })).not.toBeInTheDocument()
      for (const kept of [/Change owner/, /Favorite document/, /Move to…/, /Pin to team…/, /Delete documents/]) expect(option(kept)).toBeInTheDocument()
    })

    it('moves every selected document and deletes them after one confirmation', async () => {
      const first = setup(many())
      await first.user.click(option(/Move to…/))
      await first.user.click(option(/Second team/))
      await waitFor(() => expect(api.updateDocument).toHaveBeenCalledTimes(2))
      document.body.innerHTML = ''
      resetCommandContext()
      const second = setup(many())
      await second.user.click(option(/Delete documents/))
      await waitFor(() => expect(api.deleteDocument).toHaveBeenCalledTimes(2))
      expect(dialogs.confirmAction).toHaveBeenCalledTimes(1)
      expect(dialogs.confirmAction.mock.calls[0][0]).toBe('Delete 2 documents?')
    })
  })
})

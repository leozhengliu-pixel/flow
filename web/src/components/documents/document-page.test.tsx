import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project, teammate, viewer } from '@/test/fixtures'
import type { DocumentRevision, FlowDocument, TrashEntry } from '@/types/flow'

const api = vi.hoisted(() => ({
  refreshResourcePreferences: vi.fn().mockResolvedValue(undefined),
  addFavorite: vi.fn(),
  addSubscription: vi.fn(),
  removeFavorite: vi.fn(),
  removeSubscription: vi.fn(),
  listDocumentPermissions: vi.fn(),
  replaceDocumentPermissions: vi.fn(),
  updateDocument: vi.fn(),
  restoreTrashEntry: vi.fn(),
}))
const toasts = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast: toasts }))
vi.mock('@/lib/resource-preferences', () => ({ refreshResourcePreferences: api.refreshResourcePreferences }))

vi.mock('@/components/documents/collaborative-editor', () => ({
  CollaborativeEditor: ({ value }: { value: string }) => <div aria-label="Document content" className="flow-prosemirror">{value.split('\n\n').filter(Boolean).map(text => <p key={text}>{text}</p>)}</div>,
}))
vi.mock('@/components/issue/issue-description-editor', () => ({
  IssueDescriptionEditor: (props: { readOnly?: boolean; value: string }) => <div aria-label="Read-only content" data-readonly={String(Boolean(props.readOnly))}>{props.value}</div>,
}))
vi.mock('@/components/views/view-icon-picker', () => ({ ViewGlyph: () => <svg aria-hidden="true"/>, ViewIconPicker: () => <button aria-label="Document icon"/> }))
vi.mock('./document-history-dialog', () => ({ DocumentHistoryDialog: ({ open }: { open: boolean }) => open ? <div aria-label="History dialog" role="dialog"/> : null }))
vi.mock('./document-template-chip', async importOriginal => ({ ...(await importOriginal<typeof import('./document-template-chip')>()), DocumentTemplateChip: () => <span data-testid="template-chip"/> }))
vi.mock('./document-owner-picker', async importOriginal => ({
  ...(await importOriginal<typeof import('./document-owner-picker')>()),
  DocumentOwnerPicker: ({ trigger, open }: { trigger: React.ReactNode; open?: boolean }) => <button aria-label="Change owner" data-open={String(Boolean(open))}>{trigger}</button>,
}))
vi.mock('@/lib/api', async importOriginal => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  ...api,
}))

import { requestDocumentUiAction } from './document-actions'
import { DocumentPage, type DocumentPageProps } from './document-page'

const flowDocument = {
  id: 'document-1', slugId: 'document-one', title: 'Document one', content: '', color: '#8b8b90', creator: viewer,
  projectIds: [], teamIds: ['team-1'], subscriberIds: [viewer.id], favorite: false,
  createdAt: '2026-09-01T02:30:00.000Z', updatedAt: '2026-09-01T04:49:00.000Z',
  revisions: [{ id: 'revision-1', documentId: 'document-1', title: 'Document one', content: '', author: viewer, createdAt: '2026-09-01T04:49:00.000Z' }],
} as FlowDocument

function renderPage(overrides: Partial<DocumentPageProps> & { document?: FlowDocument; dataOverrides?: Parameters<typeof makeBootstrap>[0] } = {}) {
  const document = overrides.document ?? flowDocument
  const data = overrides.data ?? makeBootstrap({ comments: { [document.id]: [] }, documents: [document], favorites: [], subscriptions: [], ...overrides.dataOverrides })
  const props = { onBack: vi.fn(), onReload: vi.fn().mockResolvedValue(undefined), ...overrides, data, document }
  const view = render(<I18nProvider><DocumentPage {...props}/></I18nProvider>)
  return { ...view, props, data }
}

const fs = await import('node:' + 'fs') as { readFileSync: (path: string, encoding: 'utf8') => string }
const here = import.meta.url
const readStyles = (name: string) => fs.readFileSync(decodeURIComponent(new URL(name, here).pathname), 'utf8')
const documentPageCss = readStyles('document-page.css')
const documentPrintCss = readStyles('document-print.css')
class ResizeObserverStub { observe() {} unobserve() {} disconnect() {} }
vi.stubGlobal('ResizeObserver', (globalThis as { ResizeObserver?: unknown }).ResizeObserver ?? ResizeObserverStub)
const writeText = vi.fn().mockResolvedValue(undefined)
const menuRows = () => within(screen.getByRole('menu')).getAllByRole('menuitem').map(row => row.getAttribute('data-menu-item'))

describe('DocumentPage header', () => {
  beforeEach(() => {
    localStorage.clear()
    Object.values(api).forEach(mock => mock.mockReset())
    api.refreshResourcePreferences.mockResolvedValue(undefined)
    api.addFavorite.mockResolvedValue(undefined)
    api.addSubscription.mockResolvedValue(undefined)
    api.removeFavorite.mockResolvedValue(undefined)
    api.removeSubscription.mockResolvedValue(undefined)
    api.listDocumentPermissions.mockResolvedValue([])
    api.replaceDocumentPermissions.mockResolvedValue([])
    api.updateDocument.mockResolvedValue({})
    api.restoreTrashEntry.mockResolvedValue({})
    toasts.success.mockReset()
    toasts.error.mockReset()
    writeText.mockClear()
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  })

  it('persists the document favorite switch before refreshing the sidebar', async () => {
    const user = userEvent.setup()
    const { props } = renderPage()
    const favoriteSwitch = screen.getByRole('switch', { name: 'Add to favorites' })
    expect(favoriteSwitch).toHaveAttribute('aria-checked', 'false')
    await user.click(favoriteSwitch)
    await waitFor(() => expect(api.addFavorite).toHaveBeenCalledWith('document', flowDocument.id))
    expect(api.refreshResourcePreferences).not.toHaveBeenCalled()
    expect(props.onReload).not.toHaveBeenCalled()
  })

  it('shows Team › Documents › title for a team document and Project › title for a project document', () => {
    const { unmount } = renderPage()
    expect(screen.getByRole('link', { name: 'Test team' })).toHaveAttribute('href', '/workspace/team/TST/overview')
    expect(screen.getByRole('link', { name: /Documents|文档/ })).toHaveAttribute('href', '/workspace/team/TST/documents')
    expect(within(screen.getByRole('navigation', { name: /breadcrumb|面包屑/i })).getByText('Document one')).toBeVisible()
    unmount()

    const inProject = { ...flowDocument, teamIds: [], projectIds: [project.id] } as FlowDocument
    renderPage({ document: inProject })
    expect(screen.getByRole('link', { name: project.name })).toHaveAttribute('href', expect.stringContaining('/workspace/project/'))
    expect(screen.queryByRole('link', { name: /Documents|文档/ })).not.toBeInTheDocument()
  })

  it('falls back to the navigation origin when the document has no parent', async () => {
    const user = userEvent.setup()
    const orphan = { ...flowDocument, teamIds: [] } as FlowDocument
    const { props } = renderPage({ document: orphan, origin: { label: 'Inbox' } })
    await user.click(screen.getByRole('button', { name: 'Inbox' }))
    expect(props.onBack).toHaveBeenCalled()
  })

  it('moves the Edited metadata into the header popover: author names, owner, last edit and history (no comments toggle)', async () => {
    const user = userEvent.setup()
    renderPage()
    expect(document.querySelector('.document-header .document-edited')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Edited|已编辑|编辑/ }))
    expect(screen.queryByRole('checkbox', { name: /Show comments|显示评论/ })).not.toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: /Show author names|显示作者姓名/ })).not.toBeChecked()
    expect(screen.getByText(/Owned by|所有者/)).toBeVisible()
    expect(within(screen.getByRole('button', { name: 'Change owner' })).getByText('Viewer')).toBeVisible()
    expect(screen.getByText(/Last edit by|最后编辑者/)).toBeVisible()
    expect(screen.getAllByText('Viewer').length).toBeGreaterThanOrEqual(2)

    await user.click(screen.getByRole('checkbox', { name: /Show author names|显示作者姓名/ }))
    expect(localStorage.getItem('flow:document:document-1:authors')).toBe('true')

    await user.click(screen.getByRole('button', { name: /Show document history|显示文档历史/ }))
    expect(await screen.findByRole('dialog', { name: 'History dialog' })).toBeVisible()
  })

  it('shows "No owner" when the document has no owner permission', async () => {
    const user = userEvent.setup()
    const noOwner = { ...flowDocument, permissions: [{ id: 'p', documentId: 'document-1', subjectType: 'user', subjectId: teammate.id, role: 'editor' }] } as FlowDocument
    renderPage({ document: noOwner })
    await user.click(screen.getByRole('button', { name: /Edited|已编辑|编辑/ }))
    expect(within(screen.getByRole('button', { name: 'Change owner' })).getByText(/No owner|无所有者/)).toBeVisible()
  })

  it('opens the history dialog on arrival when a resource menu requests it', async () => {
    const handled = vi.fn()
    renderPage({ openHistoryRequest: true, onHistoryRequestHandled: handled })
    expect(await screen.findByRole('dialog', { name: 'History dialog' })).toBeVisible()
    expect(handled).toHaveBeenCalledTimes(1)
  })

  it('copies the canonical URL from the link button with its tooltip and toast', async () => {
    const user = userEvent.setup()
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    renderPage()
    const link = screen.getByRole('button', { name: 'Copy document URL' })
    await user.hover(link)
    const tooltip = await screen.findByRole('tooltip', undefined, { timeout: 2000 })
    expect(tooltip).toHaveTextContent('Copy document URL')
    expect(tooltip.textContent).toMatch(/⌘\s*⇧\s*,/)
    await user.click(link)
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(`${location.origin}/workspace/document/document-one`))
    expect(toasts.success).toHaveBeenCalledWith('Copied document link to clipboard')
  })

  it('opens a subscribers popover from the bell: viewer check plus the other members', async () => {
    const user = userEvent.setup()
    const { props } = renderPage()
    await user.click(screen.getByRole('button', { name: 'Document subscribers' }))
    const list = await screen.findByRole('group', { name: 'Document subscribers' })
    expect(within(list).getByRole('checkbox', { name: 'Viewer' })).toBeChecked()
    expect(within(list).getByRole('checkbox', { name: 'Teammate' })).not.toBeChecked()

    await user.click(within(list).getByRole('checkbox', { name: 'Teammate' }))
    await waitFor(() => expect(api.updateDocument).toHaveBeenCalledWith(flowDocument.id, { subscriberIds: [viewer.id, teammate.id] }))
    expect(props.onReload).toHaveBeenCalled()

    await user.click(within(list).getByRole('checkbox', { name: 'Viewer' }))
    await waitFor(() => expect(api.removeSubscription).toHaveBeenCalledWith('document', flowDocument.id))
    expect(api.refreshResourcePreferences).toHaveBeenCalledWith('workspace')
  })

  it('removes a subscriber through the subscribers popover', async () => {
    const user = userEvent.setup()
    const subscribed = { ...flowDocument, subscriberIds: [viewer.id, teammate.id] } as FlowDocument
    renderPage({ document: subscribed })
    await user.click(screen.getByRole('button', { name: 'Document subscribers' }))
    await user.click(await screen.findByRole('checkbox', { name: 'Teammate' }))
    await waitFor(() => expect(api.updateDocument).toHaveBeenCalledWith(flowDocument.id, { subscriberIds: [viewer.id] }))
  })

  it('subscribes the viewer from the popover when not subscribed yet', async () => {
    const user = userEvent.setup()
    const unsubscribed = { ...flowDocument, subscriberIds: [] } as FlowDocument
    renderPage({ document: unsubscribed })
    await user.click(screen.getByRole('button', { name: 'Document subscribers' }))
    await user.click(await screen.findByRole('checkbox', { name: 'Viewer' }))
    await waitFor(() => expect(api.addSubscription).toHaveBeenCalledWith('document', flowDocument.id))
  })

  it('lists the "…" menu in Linear order and keeps People with access for owners', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('button', { name: 'Document options' }))
    expect(menuRows()).toEqual(['Move to', 'Pin to team', 'Duplicate', 'New template from document', 'Rename…', 'Favorite', 'Copy', 'Download', 'Remind me', 'Show author names', 'Show document history', 'People with access', 'Delete'])
  })

  it('hides People with access and Delete from viewers who cannot manage the document', async () => {
    const user = userEvent.setup()
    const foreign = { ...flowDocument, creator: teammate, permissions: [{ id: 'p', documentId: 'document-1', subjectType: 'user', subjectId: teammate.id, role: 'owner' }] } as FlowDocument
    renderPage({ document: foreign })
    await user.click(screen.getByRole('button', { name: 'Document options' }))
    expect(menuRows()).not.toContain('People with access')
    expect(menuRows()).not.toContain('Delete')
  })

  it('opens the access dialog from the menu and changes a role', async () => {
    const user = userEvent.setup()
    api.listDocumentPermissions.mockResolvedValue([{ id: 'owner-grant', documentId: flowDocument.id, subjectType: 'user', subjectId: viewer.id, role: 'owner' }])
    api.replaceDocumentPermissions.mockResolvedValue([{ id: 'owner-grant', documentId: flowDocument.id, subjectType: 'user', subjectId: viewer.id, role: 'owner' }, { id: 'editor-grant', documentId: flowDocument.id, subjectType: 'user', subjectId: teammate.id, role: 'editor' }])
    renderPage({ dataOverrides: { members: [{ user: viewer, role: 'owner', status: 'active' }, { user: teammate, role: 'member', status: 'active' }] as never } })
    await user.click(screen.getByRole('button', { name: 'Document options' }))
    await user.click(screen.getByRole('menuitem', { name: 'People with access' }))
    expect(await screen.findByRole('heading', { name: 'People with access' })).toBeVisible()
    await user.click(screen.getByRole('combobox', { name: 'Access for Teammate' }))
    await user.click(screen.getByRole('option', { name: 'Can edit' }))
    await waitFor(() => expect(api.replaceDocumentPermissions).toHaveBeenCalledWith(flowDocument.id, [{ subjectType: 'user', subjectId: viewer.id, role: 'owner' }, { subjectType: 'user', subjectId: teammate.id, role: 'editor' }]))
  })

  it('downloads Markdown and prints the PDF from the Download submenu', async () => {
    const user = userEvent.setup()
    const create = vi.fn(() => 'blob:markdown')
    Object.assign(URL, { createObjectURL: create, revokeObjectURL: vi.fn() })
    const anchors: HTMLAnchorElement[] = []
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) { anchors.push(this) })
    const print = vi.fn()
    window.print = print
    renderPage({ document: { ...flowDocument, content: 'Hello' } as FlowDocument })

    await user.click(screen.getByRole('button', { name: 'Document options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Download' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Markdown' }))
    expect(create).toHaveBeenCalledWith(expect.any(Blob))
    expect(anchors[0]?.download).toBe('Document one.md')
    expect(click).toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Document options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Download' }))
    await user.click(await screen.findByRole('menuitem', { name: 'PDF' }))
    expect(print).toHaveBeenCalledTimes(1)
    click.mockRestore()
  })

  it('shows the template chip beside the icon', () => {
    renderPage()
    expect(screen.getByTestId('template-chip')).toBeInTheDocument()
  })
})

describe('DocumentPage shortcuts and UI requests', () => {
  beforeEach(() => {
    vi.spyOn(navigator, 'platform', 'get').mockReturnValue('MacIntel')
    localStorage.clear()
    Object.values(api).forEach(mock => mock.mockReset())
    api.addFavorite.mockResolvedValue(undefined)
    api.refreshResourcePreferences.mockResolvedValue(undefined)
    api.listDocumentPermissions.mockResolvedValue([])
    api.updateDocument.mockResolvedValue({})
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    writeText.mockClear()
  })

  it('runs page shortcuts: ⌥F favorites, ⇧A toggles author names, ⌘⇧, copies the URL', async () => {
    // A fresh document id: the favorites module remembers confirmed intents per resource across tests.
    renderPage({ document: { ...flowDocument, id: 'document-shortcuts', slugId: 'document-shortcuts' } as FlowDocument })
    fireEvent.keyDown(document.body, { code: 'KeyF', key: 'ƒ', altKey: true })
    await waitFor(() => expect(api.addFavorite).toHaveBeenCalledWith('document', 'document-shortcuts'))
    fireEvent.keyDown(document.body, { code: 'KeyA', key: 'A', shiftKey: true })
    expect(localStorage.getItem('flow:document:document-shortcuts:authors')).toBe('true')
    fireEvent.keyDown(document.body, { code: 'Comma', key: ',', metaKey: true, shiftKey: true })
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(`${location.origin}/workspace/document/document-shortcuts`))
  })

  it('⇧P opens the menu at Move to', async () => {
    renderPage()
    fireEvent.keyDown(document.body, { code: 'KeyP', key: 'P', shiftKey: true })
    expect(await screen.findByPlaceholderText(/Move to|移动到/)).toBeInTheDocument()
  })

  it('⇧H opens the menu at Remind me', async () => {
    renderPage()
    fireEvent.keyDown(document.body, { code: 'KeyH', key: 'H', shiftKey: true })
    expect(await screen.findByRole('menuitem', { name: /An hour from now|1 hour|一小时/ }, { timeout: 2000 })).toBeInTheDocument()
  })

  it('⌘⇧S opens the subscribers popover and Ctrl⌘O the owner picker', async () => {
    renderPage()
    fireEvent.keyDown(document.body, { code: 'KeyS', key: 'S', metaKey: true, shiftKey: true })
    expect(await screen.findByRole('group', { name: 'Document subscribers' })).toBeVisible()
    fireEvent.keyDown(document.body, { code: 'Escape', key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('group', { name: 'Document subscribers' })).not.toBeInTheDocument())
    fireEvent.keyDown(document.body, { code: 'KeyO', key: 'o', metaKey: true, ctrlKey: true })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Change owner' })).toHaveAttribute('data-open', 'true'))
  })

  it('answers document UI requests from other surfaces', async () => {
    renderPage()
    expect(requestDocumentUiAction('subscribers', flowDocument.id)).toBe(true)
    expect(await screen.findByRole('group', { name: 'Document subscribers' })).toBeVisible()
    expect(requestDocumentUiAction('history', flowDocument.id)).toBe(true)
    expect(await screen.findByRole('dialog', { name: 'History dialog' })).toBeVisible()
    expect(requestDocumentUiAction('history', 'another-document')).toBe(false)
  })

  it('answers the owner request by opening the Edited popover with the owner picker', async () => {
    renderPage()
    expect(requestDocumentUiAction('owner', flowDocument.id)).toBe(true)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Change owner' })).toHaveAttribute('data-open', 'true'))
  })
})

describe('DocumentPage author names', () => {
  beforeEach(() => { localStorage.clear(); Object.values(api).forEach(mock => mock.mockReset()) })
  const doc = (...texts: string[]) => ({ type: 'doc', content: texts.map(text => ({ type: 'paragraph', content: [{ type: 'text', text }] })) })
  const revision = (id: string, author: typeof viewer, minute: number, texts: string[]): DocumentRevision => ({ id, documentId: 'document-2', title: 'T', content: texts.join('\n\n'), author, createdAt: `2026-09-01T00:0${minute}:00.000Z`, contentData: doc(...texts) })

  it('labels the start of each author run beside the matching blocks', async () => {
    const user = userEvent.setup()
    const authored = { ...flowDocument, id: 'document-2', slugId: 'document-two', content: 'First\n\nSecond', contentData: doc('First', 'Second'), revisions: [revision('r2', teammate, 2, ['First', 'Second']), revision('r1', viewer, 1, ['First'])] } as FlowDocument
    renderPage({ document: authored })
    expect(document.querySelector('.document-author-name')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Edited|已编辑|编辑/ }))
    await user.click(screen.getByRole('checkbox', { name: /Show author names|显示作者姓名/ }))
    await waitFor(() => expect([...document.querySelectorAll('.document-author-name')].map(label => label.textContent)).toEqual(['Viewer', 'Teammate']), { timeout: 3000 })
  })

  it('labels a Markdown-only document with its creator and never an empty one', async () => {
    const user = userEvent.setup()
    const written = { ...flowDocument, id: 'document-3', slugId: 'document-three', content: 'Written content' } as FlowDocument
    const { unmount } = renderPage({ document: written })
    await user.click(screen.getByRole('button', { name: /Edited|已编辑|编辑/ }))
    await user.click(screen.getByRole('checkbox', { name: /Show author names|显示作者姓名/ }))
    expect(await screen.findByText('Viewer', { selector: '.document-author-name' }, { timeout: 3000 })).toBeVisible()
    unmount()

    renderPage({ document: { ...written, id: 'document-4', content: '' } as FlowDocument })
    await user.click(screen.getByRole('button', { name: /Edited|已编辑|编辑/ }))
    await user.click(screen.getByRole('checkbox', { name: /Show author names|显示作者姓名/ }))
    await new Promise(resolve => setTimeout(resolve, 800))
    expect(document.querySelector('.document-author-name')).not.toBeInTheDocument()
  })
})

describe('DocumentPage title', () => {
  beforeEach(() => { localStorage.clear(); Object.values(api).forEach(mock => mock.mockReset()); api.updateDocument.mockResolvedValue({}) })

  it('shows the grey "New document" placeholder and the Untitled fallback for an empty title', () => {
    renderPage({ document: { ...flowDocument, title: '' } as FlowDocument })
    const input = screen.getByRole('textbox', { name: 'Document title' })
    expect(input).toHaveValue('')
    expect(input).toHaveAttribute('placeholder', 'New document')
    expect(within(screen.getByRole('navigation', { name: /breadcrumb|面包屑/i })).getByText('Untitled')).toBeVisible()
  })

  it('saves typed titles and does not force an emptied title back', async () => {
    const user = userEvent.setup()
    const { props } = renderPage({ document: { ...flowDocument, title: '' } as FlowDocument })
    const input = screen.getByRole('textbox', { name: 'Document title' })
    await user.type(input, 'Plan')
    await waitFor(() => expect(api.updateDocument).toHaveBeenCalledWith(flowDocument.id, { title: 'Plan' }), { timeout: 2000 })
    api.updateDocument.mockClear()
    await user.clear(input)
    await user.tab()
    await waitFor(() => expect(api.updateDocument).toHaveBeenCalledWith(flowDocument.id, { title: '' }))
    expect(input).toHaveValue('')
    expect(props.onReload).toHaveBeenCalled()
  })
})

describe('DocumentPage deleted state', () => {
  const entry: TrashEntry = { id: 'trash-1', resourceType: 'document', resourceId: 'document-1', title: 'Document one', deletedBy: viewer, deletedAt: '2026-10-01T00:00:00.000Z', expiresAt: '2026-10-31T00:00:00.000Z' }
  beforeEach(() => {
    localStorage.clear()
    Object.values(api).forEach(mock => mock.mockReset())
    api.restoreTrashEntry.mockResolvedValue({})
    toasts.success.mockReset()
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  })

  it('renders read-only with a Deleted badge, no star, bell, agent or template chip', () => {
    renderPage({ deletedEntry: entry })
    expect(screen.getByText(/Deleted|已删除/)).toBeVisible()
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Document subscribers' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /chat/i })).not.toBeInTheDocument()
    expect(screen.queryByTestId('template-chip')).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Document title' })).toHaveAttribute('readonly')
    expect(screen.getByLabelText('Read-only content')).toHaveAttribute('data-readonly', 'true')
    expect(screen.queryByLabelText('Document content')).not.toBeInTheDocument()
  })

  it('offers only Copy and Restore document, and restores through the trash entry', async () => {
    const user = userEvent.setup()
    const { props } = renderPage({ deletedEntry: entry })
    await user.click(screen.getByRole('button', { name: 'Document options' }))
    expect(menuRows()).toEqual(['Copy', 'Restore document'])
    await user.click(screen.getByRole('menuitem', { name: 'Restore document' }))
    await waitFor(() => expect(api.restoreTrashEntry).toHaveBeenCalledWith('trash-1'))
    expect(props.onReload).toHaveBeenCalled()
    expect(toasts.success).toHaveBeenCalledWith('Document restored')
  })

  it('ignores page shortcuts while deleted', () => {
    renderPage({ deletedEntry: entry })
    fireEvent.keyDown(document.body, { code: 'KeyA', key: 'A', shiftKey: true })
    expect(localStorage.getItem('flow:document:document-1:authors')).toBeNull()
    expect(requestDocumentUiAction('history', flowDocument.id)).toBe(false)
  })
})

describe('DocumentPage mobile header and print', () => {
  it('has a sidebar trigger for the mobile header', async () => {
    const user = userEvent.setup()
    const onOpenSidebar = vi.fn()
    renderPage({ onOpenSidebar })
    const trigger = screen.getByRole('button', { name: 'Open sidebar' })
    expect(trigger).toHaveAttribute('data-sidebar-trigger')
    await user.click(trigger)
    expect(onOpenSidebar).toHaveBeenCalled()
  })

  it('collapses the breadcrumb to the title and the Edited button to an icon at 800px and below', () => {
    const mobile = documentPageCss.slice(documentPageCss.indexOf('@media (max-width: 800px)'))
    expect(mobile).toMatch(/\.document-mobile-menu\s*\{\s*display:\s*grid/)
    expect(mobile).toMatch(/\.document-breadcrumb-parent[\s\S]*display:\s*none/)
    expect(mobile).toMatch(/\.document-edited__label[\s\S]*display:\s*none/)
    expect(documentPageCss).toMatch(/\.document-mobile-menu\s*\{\s*display:\s*none/)
  })

  it('ships a print stylesheet that prints only the document', () => {
    expect(documentPrintCss).toContain('@media print')
    expect(documentPrintCss).toMatch(/\.document-page \.document-header[\s\S]*display:\s*none\s*!important/)
    expect(documentPrintCss).toMatch(/\.document-comments[\s\S]*display:\s*none\s*!important/)
    expect(documentPrintCss).toMatch(/\.document-agent-rail[\s\S]*display:\s*none\s*!important/)
    expect(documentPrintCss).toMatch(/\.entity-activity-panel[\s\S]*display:\s*none\s*!important/)
    expect(documentPrintCss).toMatch(/\.app:has\(\.document-page\)\s*>\s*:not\(\.document-page\)\s*\{\s*display:\s*none/)
    expect(documentPrintCss).not.toMatch(/#[0-9a-f]{3,8}\b/i)
  })

  it('uses the 805px column, 24px title and the measured 32x36 empty-document icon from tokens only', () => {
    expect(documentPageCss).toContain('--document-column: 805px')
    expect(documentPageCss).toMatch(/\.document-title\s*\{[^}]*font:\s*600 24px\/32px/)
    expect(documentPageCss).toMatch(/\.document-icon\.is-empty\s*\{[^}]*width:\s*32px;[^}]*height:\s*36px/)
    expect(documentPageCss).not.toMatch(/384px/)
    expect(documentPageCss).not.toMatch(/rgba?\(/)
    expect(documentPageCss).not.toMatch(/:root\[data-theme/)
  })
})

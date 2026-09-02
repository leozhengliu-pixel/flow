import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { FlowDocument } from '@/types/flow'

const api = vi.hoisted(() => ({
  addFavorite: vi.fn(),
  addSubscription: vi.fn(),
  removeFavorite: vi.fn(),
  removeSubscription: vi.fn(),
  listDocumentPermissions: vi.fn(),
  replaceDocumentPermissions: vi.fn(),
}))

vi.mock('@/components/issue/issue-description-editor', () => ({ IssueDescriptionEditor: () => <div aria-label="Document content"/> }))
vi.mock('@/components/views/view-icon-picker', () => ({ ViewGlyph: () => <svg aria-hidden="true"/>, ViewIconPicker: () => <button aria-label="Document icon"/> }))
vi.mock('@/lib/api', async importOriginal => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  ...api,
}))

import { DocumentPage } from './document-page'

const flowDocument = {
  id: 'document-1', slugId: 'document-one', title: 'Document one', content: '', color: '#8b8b90', creator: viewer,
  projectIds: [], teamIds: ['team-1'], subscriberIds: [viewer.id], favorite: false,
  createdAt: '2026-09-01T02:30:00.000Z', updatedAt: '2026-09-01T04:49:00.000Z',
  revisions: [{ id: 'revision-1', documentId: 'document-1', title: 'Document one', content: '', author: viewer, createdAt: '2026-09-01T04:49:00.000Z' }],
} as FlowDocument

describe('DocumentPage edited details', () => {
  beforeEach(() => {
    api.addFavorite.mockReset().mockResolvedValue(undefined)
    api.addSubscription.mockReset().mockResolvedValue(undefined)
    api.removeFavorite.mockReset().mockResolvedValue(undefined)
    api.removeSubscription.mockReset().mockResolvedValue(undefined)
    api.listDocumentPermissions.mockReset().mockResolvedValue([])
    api.replaceDocumentPermissions.mockReset().mockResolvedValue([])
  })

  it('persists the document favorite switch before refreshing the sidebar', async () => {
    const user = userEvent.setup()
    const onReload = vi.fn().mockResolvedValue(undefined)
    const data = makeBootstrap({ comments: { [flowDocument.id]: [] }, documents: [flowDocument], favorites: [], subscriptions: [] })
    render(<I18nProvider><DocumentPage data={data} document={flowDocument} onBack={vi.fn()} onReload={onReload}/></I18nProvider>)

    const favoriteSwitch = screen.getByRole('switch', { name: 'Add to favorites' })
    expect(favoriteSwitch).toHaveAttribute('aria-checked', 'false')
    await user.click(favoriteSwitch)

    await waitFor(() => expect(api.addFavorite).toHaveBeenCalledWith('document', flowDocument.id))
    expect(onReload).toHaveBeenCalledOnce()
  })

  it('matches the Linear metadata popover and opens history as a second action', async () => {
    const user = userEvent.setup()
    const data = makeBootstrap({ comments: { [flowDocument.id]: [] }, documents: [flowDocument], favorites: [], subscriptions: [] })
    render(<I18nProvider><DocumentPage data={data} document={flowDocument} onBack={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)

    expect(screen.getByRole('link', { name: 'Test team' })).toHaveAttribute('href', '/workspace/team/TST/overview')
    expect(screen.getByRole('link', { name: /Documents|文档/ })).toHaveAttribute('href', '/workspace/team/TST/documents')

    await user.click(screen.getByRole('button', { name: /Edited|已编辑/ }))
    expect(screen.getByRole('checkbox', { name: /Show comments|显示评论/ })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: /Show author names|显示作者姓名/ })).not.toBeChecked()
    expect(screen.getByText(/Owned by|所有者/)).toBeVisible()
    expect(screen.getByText(/Last edit by|最后编辑者/)).toBeVisible()

    await user.click(screen.getByRole('checkbox', { name: /Show author names|显示作者姓名/ }))
    expect(document.querySelector('.document-author-name')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Show document history|显示文档历史/ }))
    await waitFor(() => expect(screen.getByRole('dialog')).toBeVisible())
  })

  it('shows author names beside authored content, never beside an empty placeholder', async () => {
    const user = userEvent.setup()
    const authored = { ...flowDocument, id: 'document-2', slugId: 'document-two', content: 'Written content' }
    const data = makeBootstrap({ comments: { [authored.id]: [] }, documents: [authored], favorites: [], subscriptions: [] })
    render(<I18nProvider><DocumentPage data={data} document={authored} onBack={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: /Edited|已编辑/ }))
    await user.click(screen.getByRole('checkbox', { name: /Show author names|显示作者姓名/ }))
    expect(screen.getByText('Viewer', { selector: '.document-author-name' })).toBeVisible()
  })

  it('copies the canonical document URL from the header action', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const data = makeBootstrap({ comments: { [flowDocument.id]: [] }, documents: [flowDocument], favorites: [], subscriptions: [] })
    render(<I18nProvider><DocumentPage data={data} document={flowDocument} onBack={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Copy document URL' }))

    await waitFor(() => expect(writeText).toHaveBeenCalledWith(`${location.origin}/workspace/document/document-one`))
  })

  it('persists direct subscribe and unsubscribe actions before refreshing data', async () => {
    const user = userEvent.setup()
    const onReload = vi.fn().mockResolvedValue(undefined)
    const data = makeBootstrap({ comments: { [flowDocument.id]: [] }, documents: [flowDocument], favorites: [], subscriptions: [] })
    const { rerender } = render(<I18nProvider><DocumentPage data={data} document={flowDocument} onBack={vi.fn()} onReload={onReload}/></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Unsubscribe' }))
    await waitFor(() => expect(api.removeSubscription).toHaveBeenCalledWith('document', flowDocument.id))
    expect(onReload).toHaveBeenCalledOnce()

    const unsubscribedDocument = { ...flowDocument, subscriberIds: [] }
    rerender(<I18nProvider><DocumentPage data={{ ...data, documents: [unsubscribedDocument] }} document={unsubscribedDocument} onBack={vi.fn()} onReload={onReload}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Subscribe' }))
    await waitFor(() => expect(api.addSubscription).toHaveBeenCalledWith('document', flowDocument.id))
    expect(onReload).toHaveBeenCalledTimes(2)
  })

  it('lets the document owner manage member roles through the access dialog', async () => {
    const user = userEvent.setup()
    api.listDocumentPermissions.mockResolvedValue([{ id: 'owner-grant', documentId: flowDocument.id, subjectType: 'user', subjectId: viewer.id, role: 'owner' }])
    api.replaceDocumentPermissions.mockResolvedValue([{ id: 'owner-grant', documentId: flowDocument.id, subjectType: 'user', subjectId: viewer.id, role: 'owner' }, { id: 'editor-grant', documentId: flowDocument.id, subjectType: 'user', subjectId: teammate.id, role: 'editor' }])
    const data = makeBootstrap({ members: [{ user: viewer, role: 'owner', status: 'active' }, { user: teammate, role: 'member', status: 'active' }] as never, comments: { [flowDocument.id]: [] }, documents: [flowDocument], favorites: [], subscriptions: [] })
    render(<I18nProvider><DocumentPage data={data} document={flowDocument} onBack={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Document options' }))
    await user.click(screen.getByRole('menuitem', { name: 'People with access' }))
    expect(await screen.findByRole('heading', { name: 'People with access' })).toBeVisible()
    await user.click(screen.getByRole('combobox', { name: 'Access for Teammate' }))
    await user.click(screen.getByRole('option', { name: 'Can edit' }))
    await waitFor(() => expect(api.replaceDocumentPermissions).toHaveBeenCalledWith(flowDocument.id, [{ subjectType: 'user', subjectId: viewer.id, role: 'owner' }, { subjectType: 'user', subjectId: teammate.id, role: 'editor' }]))
  })
})

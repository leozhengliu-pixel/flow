import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { FlowDocument } from '@/types/flow'

vi.mock('@/components/issue/issue-description-editor', () => ({ IssueDescriptionEditor: () => <div aria-label="Document content"/> }))
vi.mock('@/components/views/view-icon-picker', () => ({ ViewGlyph: () => <svg aria-label="Document breadcrumb icon"/>, ViewIconPicker: () => <button aria-label="Document icon"/> }))

import { DocumentPage } from './document-page'

const flowDocument = {
  id: 'document-1', slugId: 'document-one', title: 'Document one', content: '', color: '#8b8b90', creator: viewer,
  projectIds: [], teamIds: ['team-1'], subscriberIds: [viewer.id], favorite: false,
  createdAt: '2026-09-01T02:30:00.000Z', updatedAt: '2026-09-01T04:49:00.000Z',
  revisions: [{ id: 'revision-1', documentId: 'document-1', title: 'Document one', content: '', author: viewer, createdAt: '2026-09-01T04:49:00.000Z' }],
} as FlowDocument

describe('DocumentPage edited details', () => {
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
})

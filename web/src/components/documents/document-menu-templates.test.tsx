import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { BootstrapData, DocumentTemplate, FlowDocument } from '@/types/flow'
import type { DocumentActionContext } from './document-actions'
import { DocumentRowMenu } from './document-menu'

const api = vi.hoisted(() => ({ updateDocument: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api }))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const team = { id: 'team-1', key: 'TST', name: 'Test team', color: '#5e6ad2', icon: 'Team' }
const document_ = { id: 'doc-1', slugId: 'plan-abc123', title: 'Plan', content: 'Body', creator: viewer, projectIds: [], teamIds: [team.id], subscriberIds: [], favorite: false, createdAt: '', updatedAt: '', revisions: [] } as unknown as FlowDocument
const template = { id: 'tpl', teamId: '', name: 'Weekly notes', title: 'Weekly', icon: 'Page', content: 'Agenda', creator: viewer, createdAt: '', updatedAt: '' } as DocumentTemplate

function ctx(templates: DocumentTemplate[]): DocumentActionContext {
  const data = makeBootstrap({ teams: [team], teamMembers: [{ teamId: team.id, userId: viewer.id }], documentTemplates: templates, documents: [document_], favorites: [] } as unknown as Partial<BootstrapData>)
  return { data, reload: vi.fn().mockResolvedValue(undefined), navigate: vi.fn(), t: value => value }
}

function open(templates: DocumentTemplate[]) {
  render(<I18nProvider><DocumentRowMenu ctx={ctx(templates)} document={document_}><div data-testid="row">Plan</div></DocumentRowMenu></I18nProvider>)
  fireEvent.contextMenu(screen.getByTestId('row'), { clientX: 20, clientY: 20 })
}

describe('document menu: Apply document template…', () => {
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    api.updateDocument.mockResolvedValue({})
  })
  afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks() })

  it('is offered between New template from document and Rename when templates exist', async () => {
    open([template])
    const rows = (await screen.findAllByRole('menuitem')).map(row => row.textContent ?? '')
    const apply = rows.findIndex(label => label.includes('Apply document template…'))
    expect(apply).toBeGreaterThan(rows.findIndex(label => label.includes('New template from document')))
    expect(apply).toBeLessThan(rows.findIndex(label => label.includes('Rename…')))
  })

  it('is hidden when the workspace has no templates', async () => {
    open([])
    await screen.findAllByRole('menuitem')
    expect(screen.queryByRole('menuitem', { name: /Apply document template…/ })).not.toBeInTheDocument()
  })

  it('applies the chosen template to the document', async () => {
    const user = userEvent.setup()
    open([template])
    const item = await screen.findByRole('menuitem', { name: /Apply document template…/ })
    await user.click(item)
    await user.click(await screen.findByText('Weekly notes'))
    await waitFor(() => expect(api.updateDocument).toHaveBeenCalledWith('doc-1', expect.objectContaining({ title: 'Weekly', icon: 'Page', content: 'Agenda' })))
  })
})

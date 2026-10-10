import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, FlowDocument, InitiativeResource } from '@/types/flow'

const api = vi.hoisted(() => ({ createDocument: vi.fn(), createInitiativeResource: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api }))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
const route = vi.hoisted(() => ({ assign: vi.fn() }))

import { InitiativeResources } from './initiative-resources'

const doc = (over: Partial<FlowDocument>) => ({
  id: 'doc-1', slugId: 'notes-abc', title: 'Notes', content: '', projectIds: [], teamIds: [], subscriberIds: [], favorite: false, revisions: [],
  creator: { id: 'u' }, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z', ...over,
}) as unknown as FlowDocument
const resource = (over: Partial<InitiativeResource>) => ({ id: 'res-1', type: 'document', title: 'Stale', url: '/workspace/document/notes-abc', documentId: 'doc-1', createdAt: '2026-09-01T00:00:00Z', ...over }) as unknown as InitiativeResource

function mount(over: { documents?: FlowDocument[]; resources?: InitiativeResource[]; data?: BootstrapData } = {}) {
  const data = over.data ?? makeBootstrap({ favorites: [] })
  const props = { onCreate: vi.fn().mockResolvedValue({}), onUpdate: vi.fn(), onDelete: vi.fn().mockResolvedValue(undefined), onReload: vi.fn().mockResolvedValue(undefined) }
  render(<I18nProvider><InitiativeResources data={data} documents={over.documents ?? [doc({})]} initiativeId="init-1" resources={over.resources ?? [resource({})]} {...props}/></I18nProvider>)
  return { ...props, data }
}

beforeEach(() => {
  localStorage.setItem('flow:locale', 'en-US')
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('location', { ...window.location, assign: route.assign, origin: window.location.origin })
  api.createDocument.mockReset().mockResolvedValue(doc({ id: 'doc-new', slugId: 'untitled-123abc', title: '' }))
  api.createInitiativeResource.mockReset().mockResolvedValue({})
  route.assign.mockReset()
})
afterEach(() => vi.unstubAllGlobals())

it('shows the live document title with its icon and the shared document menu', async () => {
  const user = userEvent.setup()
  mount()
  expect(screen.getByRole('link', { name: 'Notes' })).toHaveAttribute('href', '/workspace/document/notes-abc')
  await user.click(screen.getByRole('button', { name: 'Notes actions' }))
  const menu = await screen.findByRole('menu')
  expect(menu).toHaveAttribute('aria-label', 'Document actions')
  expect(within(menu).getAllByRole('menuitem').map(item => item.querySelector('.linear-menu__text')?.textContent)).toEqual(['Move to', 'Pin to team', 'Duplicate', 'New template from document', 'Rename…', 'Favorite', 'Copy', 'Remind me', 'Show document history', 'Delete', 'Remove resource'])
})

it('labels an untitled document Untitled, also when the stored title is stale', () => {
  mount({ documents: [doc({ title: '' })] })
  expect(screen.getByRole('link', { name: 'Untitled' })).toBeInTheDocument()
})

it('removes only the initiative link from "Remove resource"', async () => {
  const user = userEvent.setup()
  const { onDelete } = mount()
  await user.click(screen.getByRole('button', { name: 'Notes actions' }))
  await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Remove resource' }))
  expect(onDelete).toHaveBeenCalledWith('init-1', 'res-1')
})

it('keeps the short link menu for link resources', async () => {
  const user = userEvent.setup()
  mount({ resources: [{ id: 'res-2', type: 'link', title: 'Spec', url: 'https://example.com/spec' } as unknown as InitiativeResource] })
  await user.click(screen.getByRole('button', { name: 'Spec actions' }))
  expect(within(await screen.findByRole('menu')).getAllByRole('menuitem').map(item => item.textContent)).toEqual(['Open', 'Edit link', 'Delete'])
})

it('creates an untitled document under the initiative and opens it', async () => {
  const user = userEvent.setup()
  const { onReload } = mount({ resources: [], documents: [] })
  await user.click(screen.getByRole('button', { name: /Add document or link/ }))
  await user.click(await screen.findByRole('menuitem', { name: /Create new document/ }))
  await waitFor(() => expect(api.createInitiativeResource).toHaveBeenCalledWith('init-1', { type: 'document', documentId: 'doc-new' }))
  expect(api.createDocument).toHaveBeenCalledWith(expect.objectContaining({ title: '' }))
  await waitFor(() => expect(onReload).toHaveBeenCalled())
  // Outside a router the navigation falls back to a full page load of the document URL.
  await waitFor(() => expect(route.assign).toHaveBeenCalledWith('/workspace/document/untitled-123abc'))
})

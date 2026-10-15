import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { BootstrapData, FlowDocument, TrashEntry } from '@/types/flow'

const api = vi.hoisted(() => ({ restoreTrashEntry: vi.fn() }))
const confirm = vi.hoisted(() => vi.fn())
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: confirm }))

import { TeamArchivePage } from './team-archive-page'

const NOW = Date.parse('2026-10-09T12:00:00Z')
const minutesAgo = (minutes: number) => new Date(NOW - minutes * 60_000).toISOString()

function documentPayload(overrides: Partial<FlowDocument>): FlowDocument {
  return { id: 'doc', slugId: 'doc', title: 'Roadmap', icon: '', color: '', content: '', creator: viewer, teamIds: ['team-1'], projectIds: [], subscriberIds: [], favorite: false, revisions: [], createdAt: '', updatedAt: '', ...overrides } as FlowDocument
}

function entry(id: string, document: FlowDocument, deletedAt: string, teamIds: string[] = ['team-1']): TrashEntry {
  return { id, resourceType: 'document', resourceId: document.id, title: document.title, payload: document, teamIds, deletedBy: viewer, deletedAt, expiresAt: '2026-11-08T00:00:00Z' }
}

function renderArchive(trash: TrashEntry[], overrides: Partial<BootstrapData> = {}, onReload = vi.fn().mockResolvedValue(undefined), tab: 'recently-deleted-documents' | 'recently-deleted-projects' = 'recently-deleted-documents') {
  const data = makeBootstrap({ trash, ...overrides })
  const view = render(<I18nProvider><TeamArchivePage data={data} team={data.teams[0]} tab={tab} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onReload={onReload}/></I18nProvider>)
  return { ...view, data, onReload }
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'], now: NOW })
  localStorage.clear()
  api.restoreTrashEntry.mockReset().mockResolvedValue(undefined)
  confirm.mockReset().mockResolvedValue(true)
})
afterEach(() => { vi.useRealTimers(); localStorage.clear() })

it('lists deleted documents like the documents list: glyph, title, muted parent and a relative date', () => {
  const base = makeBootstrap()
  const project = base.projects[0]
  const { container } = renderArchive([
    entry('trash-1', documentPayload({ id: 'doc-1', title: 'Roadmap', icon: 'Rocket', color: '#5e6ad2' }), minutesAgo(31)),
    entry('trash-2', documentPayload({ id: 'doc-2', title: '  ', teamIds: [], projectIds: [project.id] }), minutesAgo(0.2)),
  ], {}, undefined)

  const rows = [...container.querySelectorAll<HTMLElement>('.archive-doc-row')]
  expect(rows).toHaveLength(2)
  expect(rows[0].querySelector('svg.archive-doc-glyph')).not.toBeNull()
  expect(rows[0].querySelector('strong')).toHaveTextContent('Roadmap')
  expect(rows[0].querySelector('.archive-doc-parent')).toHaveTextContent(base.teams[0].name)
  const time = rows[0].querySelector('time')!
  expect(time).toHaveTextContent('31min ago')
  expect(time.getAttribute('title')).toMatch(/2026/)
  expect(rows[1].querySelector('strong')).toHaveTextContent('Untitled')
  expect(rows[1].querySelector('.archive-doc-parent')).toHaveTextContent(project.name)
  expect(rows[1].querySelector('time')).toHaveTextContent('just now')
  // The tab counts both rows and does not fall back to the generic archive row layout.
  expect(container.querySelectorAll('.archive-row')).toHaveLength(0)
})

it('shows no parent label for a document without a resolvable parent and formats the date in Chinese', () => {
  localStorage.setItem('flow:locale', 'zh-CN')
  const { container } = renderArchive([entry('trash-1', documentPayload({ id: 'doc-1', title: '', teamIds: [] }), minutesAgo(31), ['team-1'])])
  const row = container.querySelector('.archive-doc-row')!
  expect(row.querySelector('.archive-doc-parent')).toBeNull()
  expect(row.querySelector('strong')).toHaveTextContent('无标题')
  expect(row.querySelector('time')).toHaveTextContent('31分钟前')
})

it("restores a deleted document from the reference app's menu (Copy ▸ / Restore document, no permanent delete)", async () => {
  const user = userEvent.setup()
  const { onReload } = renderArchive([entry('trash-1', documentPayload({ id: 'doc-1' }), minutesAgo(5))])
  await user.click(screen.getByRole('button', { name: 'Open actions' }))
  const menu = await screen.findByRole('menu')
  expect(within(menu).getAllByRole('menuitem').map(item => item.textContent?.replace(/[▶⌘⇧⌥].*$/, '').trim())).toEqual(['Copy', 'Restore document'])
  await user.click(within(menu).getByRole('menuitem', { name: 'Restore document' }))
  await waitFor(() => expect(api.restoreTrashEntry).toHaveBeenCalledWith('trash-1'))
  await waitFor(() => expect(onReload).toHaveBeenCalled())
})

it("offers the reference app's Copy items and Restore project for a deleted project", async () => {
  const user = userEvent.setup()
  const project = makeBootstrap().projects[0]
  const deleted: TrashEntry = { id: 'trash-p', resourceType: 'project', resourceId: project.id, title: project.name, payload: { project, updates: [] }, teamIds: ['team-1'], deletedBy: viewer, deletedAt: minutesAgo(5), expiresAt: '2026-11-08T00:00:00Z' }
  const { onReload } = renderArchive([deleted], {}, undefined, 'recently-deleted-projects')
  await user.click(screen.getByRole('button', { name: 'Open actions' }))
  const menu = await screen.findByRole('menu')
  expect(within(menu).queryByText('Delete permanently')).toBeNull()
  await user.hover(within(menu).getByRole('menuitem', { name: /Copy/ }))
  const submenu = await screen.findByRole('menu', { name: 'Copy' })
  expect(within(submenu).getAllByRole('menuitem').map(item => item.getAttribute('data-menu-item'))).toEqual(['Copy ID', 'Copy URL', 'Copy title', 'Copy title as link', 'Copy overview as Markdown'])
  await user.click(within(menu).getByRole('menuitem', { name: /Restore project/ }))
  await waitFor(() => expect(api.restoreTrashEntry).toHaveBeenCalledWith('trash-p'))
  await waitFor(() => expect(onReload).toHaveBeenCalled())
})

it('says when trash rows are deleted permanently, using the server retention', () => {
  const project = makeBootstrap().projects[0]
  const deleted: TrashEntry = { id: 'trash-p', resourceType: 'project', resourceId: project.id, title: project.name, payload: { project, updates: [] }, teamIds: ['team-1'], deletedBy: viewer, deletedAt: minutesAgo(5), expiresAt: '2026-10-16T12:00:00Z' }
  renderArchive([deleted], { trashRetentionDays: 7 }, undefined, 'recently-deleted-projects')
  expect(screen.getByText(/deleted permanently after 7 days/)).toBeTruthy()
})

it('falls back to 30 days when the server does not say', () => {
  const { container } = renderArchive([entry('trash-1', documentPayload({ id: 'doc-1' }), minutesAgo(5))])
  expect(container.querySelector('time.archive-doc-date')?.getAttribute('title')).toMatch(/deleted permanently after 30 days$/)
})

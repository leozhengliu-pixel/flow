import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project } from '@/test/fixtures'
import type { FlowDocument, ProjectResource } from '@/types/flow'

const api = vi.hoisted(() => ({
  createDocument: vi.fn(),
  createDocumentTemplate: vi.fn(),
  deleteDocument: vi.fn(),
  updateDocument: vi.fn(),
}))
vi.mock('@/lib/api', () => api)
const dialogs = vi.hoisted(() => ({ confirmAction: vi.fn(), promptAction: vi.fn() }))
vi.mock('@/components/ui/action-dialog-service', () => dialogs)
const favorites = vi.hoisted(() => ({ toggleFavoriteFor: vi.fn() }))
vi.mock('@/lib/favorites', () => favorites)

import { ProjectResourceMenu } from './project-resource-menu'

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  for (const fn of [...Object.values(api), ...Object.values(favorites)]) fn.mockReset().mockResolvedValue({})
  dialogs.confirmAction.mockReset().mockResolvedValue(true)
  dialogs.promptAction.mockReset().mockResolvedValue('Renamed notes')
})
afterEach(() => vi.unstubAllGlobals())

const labelsOf = (menu: HTMLElement) => within(menu).getAllByRole('menuitem').map(item => item.querySelector('.project-menu-label')?.textContent)
const glyphsOf = (menu: HTMLElement) => within(menu).getAllByRole('menuitem').map(item => item.querySelector('svg[data-action-glyph]')?.getAttribute('data-action-glyph') ?? item.querySelector('svg')?.tagName)

function setup(kind: 'link'|'document', overrides: { favorited?: boolean } = {}) {
  const data = makeBootstrap()
  const teams = [{ ...data.teams[0], id: 'team-a', name: 'Alpha', key: 'ALP' }, { ...data.teams[0], id: 'team-b', name: 'Beta', key: 'BET' }]
  const document = { id: 'doc-1', slugId: 'notes-abc', title: 'Notes', content: '# Notes', contentData: { type: 'doc' }, icon: undefined, color: undefined, projectIds: [project.id], teamIds: [], subscriberIds: [], favorite: false, revisions: [], creator: data.viewer, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z' } as unknown as FlowDocument
  if (overrides.favorited) data.favorites = [{ id: 'fav-1', userId: data.viewer.id, resourceType: 'document', resourceId: 'doc-1', position: 0, createdAt: '2026-09-01T00:00:00Z' }]
  const resource: ProjectResource = kind === 'link'
    ? { id: 'link-1', projectId: project.id, type: 'link', title: 'GitHub', url: 'https://github.com/acme/repo', pinnedTeamIds: ['team-b'], createdAt: '2026-09-01T00:00:00Z' }
    : { id: 'doc-1', projectId: project.id, type: 'document', title: 'Notes', url: '/acme/document/notes-abc', pinnedTeamIds: [], createdAt: '2026-09-01T00:00:00Z' }
  const other = { ...project, id: 'project-2', name: 'Other project' }
  const props = {
    onDeleteLink: vi.fn(), onEditLink: vi.fn(), onOpenDocumentHistory: vi.fn(), onReload: vi.fn().mockResolvedValue(undefined),
    onUpdate: vi.fn().mockResolvedValue(resource),
  }
  render(<I18nProvider><ProjectResourceMenu {...props} data={data} document={kind === 'document' ? document : undefined} project={project} projects={[project, other]} resource={resource} teams={teams}/></I18nProvider>)
  return { props, data, document, resource }
}

it('matches Linear’s link resource menu: items, order, glyphs and actions', async () => {
  const user = userEvent.setup()
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  const { props } = setup('link')
  await user.click(screen.getByRole('button', { name: 'GitHub actions' }))
  const menu = await screen.findByRole('menu')
  expect(menu).toHaveClass('project-action-menu', 'project-resource-action-menu')
  expect(menu).toHaveAttribute('data-kind', 'link')
  expect(labelsOf(menu)).toEqual(['Copy link', 'Pin to team', 'Edit', 'Delete'])
  expect(glyphsOf(menu)).toEqual(['link', 'pin', 'pencil', 'delete'])
  // No separator and no red Delete in Linear's link menu; the submenu arrow is the ▶ glyph.
  expect(within(menu).queryByRole('separator')).toBeNull()
  expect(within(menu).getByRole('menuitem', { name: /Delete/ })).not.toHaveClass('is-danger')
  expect(within(menu).getByRole('menuitem', { name: /Pin to team/ }).querySelector('.project-menu-chevron')).toHaveTextContent('▶')

  await user.click(within(menu).getByRole('menuitem', { name: /Copy link/ }))
  expect(writeText).toHaveBeenCalledWith('https://github.com/acme/repo')

  await user.click(screen.getByRole('button', { name: 'GitHub actions' }))
  await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: /Edit/ }))
  expect(props.onEditLink).toHaveBeenCalled()

  await user.click(screen.getByRole('button', { name: 'GitHub actions' }))
  await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: /Delete/ }))
  expect(props.onDeleteLink).toHaveBeenCalled()
})

it('pins a link resource to a team from the Pin to team submenu', async () => {
  const user = userEvent.setup()
  const { props } = setup('link')
  await user.click(screen.getByRole('button', { name: 'GitHub actions' }))
  const menu = await screen.findByRole('menu')
  within(menu).getByRole('menuitem', { name: /Pin to team/ }).focus()
  await user.keyboard('{ArrowRight}')
  const teams = await screen.findAllByRole('menuitemcheckbox')
  expect(teams.map(item => item.textContent)).toEqual(['AlphaALP', 'BetaBET'])
  expect(teams[1]).toHaveAttribute('aria-checked', 'true')
  await user.click(teams[0])
  expect(props.onUpdate).toHaveBeenCalledWith('link-1', { pinnedTeamIds: ['team-b', 'team-a'] })
})

it('matches Linear’s document resource menu: items, order, glyphs and shortcuts', async () => {
  const user = userEvent.setup()
  setup('document')
  await user.click(screen.getByRole('button', { name: 'Notes actions' }))
  const menu = await screen.findByRole('menu')
  expect(menu).toHaveAttribute('data-kind', 'document')
  expect(labelsOf(menu)).toEqual(['Move to', 'Pin to team', 'Duplicate', 'New template from document', 'Rename…', 'Favorite', 'Copy', 'Show document history', 'Delete'])
  expect(glyphsOf(menu)).toEqual(['moveTo', 'pin', 'duplicate', 'newTemplate', 'pencil', 'svg', 'copy', 'documentHistory', 'delete'])
  expect(within(menu).getAllByRole('separator')).toHaveLength(2)
  // No document reminders exist in Flow, so Linear's "Remind me" row is omitted.
  expect(within(menu).queryByText('Remind me')).toBeNull()
  const shortcut = (name: RegExp) => [...within(menu).getByRole('menuitem', { name }).querySelectorAll('.project-menu-shortcut kbd')].map(key => key.textContent).join(' ')
  expect(shortcut(/Move to/)).toBe('⇧ P')
  expect(shortcut(/Rename/)).toBe('⇧ R')
  expect(shortcut(/Favorite/)).toBe('⌥ F')
})

it('wires the document actions to the document APIs and reloads', async () => {
  const user = userEvent.setup()
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  const { props, data } = setup('document')
  const open = async () => { await user.click(screen.getByRole('button', { name: 'Notes actions' })); return screen.findByRole('menu') }
  const openSub = async (name: RegExp) => { within(await open()).getByRole('menuitem', { name }).focus(); await user.keyboard('{ArrowRight}'); return (await screen.findAllByRole('menu')).at(-1)! }

  await user.click(within(await open()).getByRole('menuitem', { name: /Duplicate/ }))
  await waitFor(() => expect(api.createDocument).toHaveBeenCalledWith(expect.objectContaining({ title: 'Notes (copy)', content: '# Notes', projectIds: [project.id] })))
  await waitFor(() => expect(props.onReload).toHaveBeenCalled())

  await user.click(within(await open()).getByRole('menuitem', { name: /Rename/ }))
  expect(dialogs.promptAction).toHaveBeenCalledWith('Rename document', 'Notes', { confirmLabel: 'Save' })
  await waitFor(() => expect(api.updateDocument).toHaveBeenCalledWith('doc-1', { title: 'Renamed notes' }))

  await user.click(within(await open()).getByRole('menuitem', { name: /Favorite/ }))
  expect(favorites.toggleFavoriteFor).toHaveBeenCalledWith(data, 'document', 'doc-1', true, false)

  await user.click(within(await open()).getByRole('menuitem', { name: /Show document history/ }))
  expect(props.onOpenDocumentHistory).toHaveBeenCalledWith(expect.objectContaining({ id: 'doc-1' }))

  await user.click(within(await open()).getByRole('menuitem', { name: /Delete/ }))
  expect(dialogs.confirmAction).toHaveBeenCalledWith('Delete "Notes"?', expect.objectContaining({ confirmLabel: 'Delete' }))
  await waitFor(() => expect(api.deleteDocument).toHaveBeenCalledWith('doc-1'))

  const move = await openSub(/Move to/)
  expect(move.textContent).toContain('My teams')
  expect(move.textContent).toContain('Projects')
  expect(within(move).getAllByRole('menuitem').map(item => item.textContent)).toEqual(['AlphaALP', 'BetaBET', 'Other project'])
  await user.click(within(move).getByRole('menuitem', { name: /Beta/ }))
  await waitFor(() => expect(api.updateDocument).toHaveBeenCalledWith('doc-1', { teamIds: ['team-b'], projectIds: [] }))

  const template = await openSub(/New template from document/)
  await user.click(within(template).getByRole('menuitem', { name: /Alpha/ }))
  await waitFor(() => expect(api.createDocumentTemplate).toHaveBeenCalledWith(expect.objectContaining({ teamId: 'team-a', name: 'Notes', content: '# Notes' })))

  const copy = await openSub(/^Copy/)
  expect(within(copy).getAllByRole('menuitem').map(item => item.querySelector('.project-menu-label')?.textContent)).toEqual(['Copy URL', 'Copy title', 'Copy title as link', 'Copy content as Markdown'])
  await user.click(within(copy).getByRole('menuitem', { name: /Copy title as link/ }))
  expect(writeText).toHaveBeenCalledWith(`[Notes](${window.location.origin}/acme/document/notes-abc)`)
})

it('offers Remove from favorites with a filled star once the document is favorited', async () => {
  const user = userEvent.setup()
  const { data } = setup('document', { favorited: true })
  await user.click(screen.getByRole('button', { name: 'Notes actions' }))
  const item = within(await screen.findByRole('menu')).getByRole('menuitem', { name: /Remove from favorites/ })
  expect(item.querySelector('svg')).toHaveAttribute('fill', 'currentColor')
  await user.click(item)
  expect(favorites.toggleFavoriteFor).toHaveBeenCalledWith(data, 'document', 'doc-1', false, true)
})

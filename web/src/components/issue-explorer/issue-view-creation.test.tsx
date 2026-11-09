import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { VirtuosoMockContext } from 'react-virtuoso'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { SavedView, SavedViewMutationInput } from '@/types/flow'
import { IssueExplorerPage } from './issue-explorer-page'
class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

beforeEach(() => { localStorage.clear(); localStorage.setItem('flow:locale', 'en-US') })
it.each(['Personal', 'Workspace', 'Test team'])('persists configured filters and the %s destination and restores preview', async destination => {
  const user = userEvent.setup()
  const data = makeBootstrap({ favorites: [], issues: [makeIssue({ id: 'high', title: 'Included high priority', priority: 2 }), makeIssue({ id: 'low', title: 'Excluded low priority', priority: 4 })] })
  const create = vi.fn(async (input: SavedViewMutationInput) => ({ ...input, id: 'saved-view' }) as SavedView)
  const renderPage = (savedView?: SavedView) => <I18nProvider><VirtuosoMockContext.Provider value={{ viewportHeight: 440, itemHeight: 44 }}><IssueExplorerPage data={data} scope={{ kind: 'workspace' }} view="all" viewHref={() => '#'} creatingView={!savedView} savedView={savedView} onNavigateView={vi.fn()} onCreateSavedView={create} onOpenIssue={vi.fn()} onUpdateIssue={vi.fn()} onUpdateIssues={vi.fn()} onDeleteIssues={vi.fn()}/></VirtuosoMockContext.Provider></I18nProvider>
  const mounted = render(renderPage())
  fireEvent.change(screen.getByRole('textbox', { name: 'View name' }), { target: { value: 'Priority view' } })
  await user.click(screen.getByRole('button', { name: 'Add filter' }))
  fireEvent.mouseMove(screen.getByRole('option', { name: 'Priority' }))
  await user.click(await screen.findByRole('option', { name: /^High/ }))
  await waitFor(() => expect(screen.queryByText('Excluded low priority')).not.toBeInTheDocument())
  expect(screen.getByText('Included high priority')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: /^Save to / }))
  await user.click(screen.getByRole('menuitemradio', { name: destination }))
  await user.click(screen.getByRole('button', { name: /^Create view$/ }))
  await waitFor(() => expect(create).toHaveBeenCalledOnce())
  const input = create.mock.calls[0][0]
  expect(input).toMatchObject({ name: 'Priority view', resource: 'issues', scope: destination === 'Personal' ? 'personal' : destination === 'Workspace' ? 'workspace' : 'team', teamId: destination === 'Test team' ? data.teams[0].id : '', ownerId: data.viewer.id })
  expect(input.filters).toEqual(expect.arrayContaining([expect.objectContaining({ field: 'priority', value: '2' })]))
  mounted.unmount()
  render(renderPage({ ...input, id: 'saved-view' } as SavedView))
  expect(screen.getByText('Included high priority')).toBeInTheDocument()
  expect(screen.queryByText('Excluded low priority')).not.toBeInTheDocument()
}, 15000)

it.each([
  ['the workspace Views page', false, 'personal' as const, 'Save to Personal'],
  ['a team Views page', true, 'team' as const, 'Save to Test team'],
])('a new view from %s starts at the reference default destination and keeps a changed choice', async (_page, teamPage, defaultSaveScope, name) => {
  const user = userEvent.setup()
  const data = makeBootstrap({ favorites: [], issues: [makeIssue({ id: 'one', title: 'One' })] })
  const scope = teamPage ? { kind: 'team' as const, team: data.teams[0] } : { kind: 'workspace' as const }
  render(<I18nProvider><VirtuosoMockContext.Provider value={{ viewportHeight: 440, itemHeight: 44 }}><IssueExplorerPage data={data} scope={scope} view="all" viewHref={() => '#'} creatingView defaultSaveScope={defaultSaveScope} onNavigateView={vi.fn()} onCreateSavedView={vi.fn()} onOpenIssue={vi.fn()} onUpdateIssue={vi.fn()} onUpdateIssues={vi.fn()} onDeleteIssues={vi.fn()}/></VirtuosoMockContext.Provider></I18nProvider>)
  expect(screen.getByRole('button', { name })).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: /^Save to / }))
  await user.click(screen.getByRole('menuitemradio', { name: 'Workspace' }))
  expect(screen.getByRole('button', { name: 'Save to Workspace' })).toBeInTheDocument()
})

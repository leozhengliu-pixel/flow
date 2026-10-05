import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { VirtuosoMockContext } from 'react-virtuoso'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, teammate } from '@/test/fixtures'
import type { BootstrapData, SavedView, SavedViewMutationInput } from '@/types/flow'
import { IssueExplorerPage, type IssueExplorerPageProps } from './issue-explorer-page'
import { decodeFiltersParam, encodeFiltersParam } from './advanced-filter'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })
vi.mock('@nivo/bar', () => ({ ResponsiveBar: () => <svg/> }))

const urgent = { id: 'urgent-chip', field: 'priority', fieldLabel: 'Priority', operator: 'is', value: '1', valueLabel: 'Urgent', values: [{ value: '1', valueLabel: 'Urgent' }] }
const high = { id: 'high-chip', field: 'priority', fieldLabel: 'Priority', operator: 'is', value: '2', valueLabel: 'High', values: [{ value: '2', valueLabel: 'High' }] }

function setup(overrides: Partial<SavedView> = {}) {
  const data = makeBootstrap({ favorites: [], issues: [
    makeIssue({ id: 'a', identifier: 'TST-1', title: 'High one', priority: 2 }),
    makeIssue({ id: 'b', identifier: 'TST-2', title: 'Low one', priority: 4 }),
    makeIssue({ id: 'c', identifier: 'TST-3', title: 'Urgent one', priority: 1 }),
  ] })
  const savedView = { id: 'view-1', slugId: 'mine-view-1', name: 'Mine', description: '', resource: 'issues', scope: 'personal', teamId: '', ownerId: teammate.id, view: 'all', filters: [], display: {}, createdAt: '', updatedAt: '', ...overrides } as SavedView
  return { data, savedView }
}

function page(data: BootstrapData, props: Partial<IssueExplorerPageProps>) {
  return <I18nProvider><VirtuosoMockContext.Provider value={{ viewportHeight: 440, itemHeight: 44 }}><IssueExplorerPage data={data} scope={{ kind: 'workspace' }} view="all" viewHref={view => `#${view}`} onNavigateView={vi.fn()} onOpenIssue={vi.fn()} onUpdateIssue={vi.fn()} onUpdateIssues={vi.fn()} onDeleteIssues={vi.fn()} {...props}/></VirtuosoMockContext.Provider></I18nProvider>
}

async function addPriorityFilter(user: ReturnType<typeof userEvent.setup>, value: RegExp, trigger = 'Add filter') {
  await user.click(screen.getAllByRole('button', { name: trigger })[0])
  fireEvent.mouseMove(screen.getByRole('option', { name: 'Priority' }))
  await user.click(await screen.findByRole('option', { name: value }))
}

beforeEach(() => { localStorage.clear(); localStorage.setItem('flow:locale', 'en-US'); history.replaceState(null, '', '/workspace/view/mine-view-1') })
afterEach(() => history.replaceState(null, '', '/'))

describe('editing a saved view (Linear)', () => {
  it('hides the page toolbar, keeps the count row, and saves without resetting scope or owner', async () => {
    const user = userEvent.setup()
    const { data, savedView } = setup({ filters: [urgent] })
    const update = vi.fn(async (_id: string, input: SavedViewMutationInput) => ({ ...savedView, ...input }) as SavedView)
    const finish = vi.fn()
    render(page(data, { savedView, editingView: true, onUpdateSavedView: update, onFinishEditSavedView: finish }))
    const card = screen.getByRole('region', { name: 'Edit view' })
    expect(screen.queryByRole('button', { name: /view details/ })).not.toBeInTheDocument()
    expect(screen.getByText('1 issue')).toBeInTheDocument()
    // The card has its own filter / display buttons and the view's chips.
    expect(within(card).getByRole('button', { name: 'Add filter' })).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Display options' })).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Priority values' })).toHaveTextContent('Urgent')
    expect(within(card).getByRole('textbox', { name: 'View name' })).not.toHaveFocus()
    // "Save to" is available while editing and starts at the view's own scope.
    expect(within(card).getByRole('button', { name: 'Save to Personal' })).toBeInTheDocument()
    fireEvent.change(within(card).getByRole('textbox', { name: 'View name' }), { target: { value: 'Renamed' } })
    // The header follows the draft name.
    expect(screen.getAllByText('Renamed').length).toBeGreaterThan(0)
    await user.click(within(card).getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(update).toHaveBeenCalledOnce())
    const input = update.mock.calls[0][1]
    expect(input).toMatchObject({ name: 'Renamed', scope: 'personal', teamId: '', filters: [urgent] })
    expect(input).not.toHaveProperty('ownerId')
    expect(input).not.toHaveProperty('resource')
    expect(finish).toHaveBeenCalled()
  })

  it('moves the view through "Save to" while editing', async () => {
    const user = userEvent.setup()
    const { data, savedView } = setup()
    const update = vi.fn(async (_id: string, input: SavedViewMutationInput) => ({ ...savedView, ...input }) as SavedView)
    render(page(data, { savedView, editingView: true, onUpdateSavedView: update }))
    await user.click(screen.getByRole('button', { name: 'Save to Personal' }))
    await user.click(screen.getByRole('menuitemradio', { name: 'Workspace' }))
    await user.click(screen.getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(update).toHaveBeenCalledOnce())
    expect(update.mock.calls[0][1]).toMatchObject({ scope: 'workspace', teamId: '' })
  })

  it('Escape leaves edit mode and discards the draft', async () => {
    const user = userEvent.setup()
    const { data, savedView } = setup()
    const update = vi.fn(), finish = vi.fn()
    render(page(data, { savedView, editingView: true, onUpdateSavedView: update, onFinishEditSavedView: finish }))
    fireEvent.change(screen.getByRole('textbox', { name: 'View name' }), { target: { value: 'Draft' } })
    await user.keyboard('{Escape}')
    expect(finish).toHaveBeenCalledOnce()
    expect(update).not.toHaveBeenCalled()
    expect(screen.queryByRole('region', { name: 'Edit view' })).not.toBeInTheDocument()
  })
})

describe('temporary filters on a saved view (Linear band)', () => {
  it('shows the band with Clear and Save ⌄, the hidden-by-filters footer and a shareable ?filter=', async () => {
    const user = userEvent.setup()
    const { data, savedView } = setup()
    render(page(data, { savedView, onUpdateSavedView: vi.fn() }))
    expect(screen.queryByRole('button', { name: 'Clear all filters' })).not.toBeInTheDocument()
    await addPriorityFilter(user, /^High/)
    await waitFor(() => expect(screen.queryByText('Low one')).not.toBeInTheDocument())
    expect(screen.getAllByRole('button', { name: 'Add another filter' })).toHaveLength(2)
    expect(screen.getByRole('status')).toHaveTextContent('2 issues hidden by filters')
    const param = new URLSearchParams(location.search).get('filter')
    expect(decodeFiltersParam(param).map(filter => filter.values?.[0].value)).toEqual(['2'])
    await user.click(screen.getAllByRole('button', { name: 'Clear all filters' })[0])
    await waitFor(() => expect(screen.getByText('Low one')).toBeInTheDocument())
    expect(new URLSearchParams(location.search).has('filter')).toBe(false)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('"Save to this view" merges only the filters into the view', async () => {
    const user = userEvent.setup()
    const { data, savedView } = setup({ filters: [high] })
    const update = vi.fn(async (_id: string, input: SavedViewMutationInput) => ({ ...savedView, ...input }) as SavedView)
    history.replaceState(null, '', `/workspace/view/mine-view-1?filter=${encodeFiltersParam([urgent as never])}`)
    render(page(data, { savedView, onUpdateSavedView: update }))
    // The ?filter= parameter is read on load: High (view) AND Urgent (temporary) match nothing.
    expect(screen.getAllByRole('button', { name: 'Priority values' }).at(-1)).toHaveTextContent('Urgent')
    expect(screen.queryByText('High one')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save view options' }))
    await user.click(screen.getByRole('menuitem', { name: /Save to this view/ }))
    await waitFor(() => expect(update).toHaveBeenCalledOnce())
    expect(update.mock.calls[0][1]).toEqual({ filters: [high, expect.objectContaining({ field: 'priority', value: '1' })] })
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Save view options' })).not.toBeInTheDocument())
  })

  it('"Create new view…" opens the new-view card with the view and temporary filters', async () => {
    const user = userEvent.setup()
    const { data, savedView } = setup({ filters: [high] })
    const create = vi.fn(async (input: SavedViewMutationInput) => ({ ...savedView, ...input, id: 'new' }) as SavedView)
    render(page(data, { savedView, onUpdateSavedView: vi.fn(), onCreateSavedView: create, onNavigateSavedView: vi.fn() }))
    await addPriorityFilter(user, /^Urgent/)
    await user.click(screen.getByRole('button', { name: 'Save view options' }))
    await user.click(screen.getByRole('menuitem', { name: /Create new view/ }))
    const card = await screen.findByRole('region', { name: 'New view' })
    expect(within(card).getAllByRole('button', { name: 'Priority values' }).map(button => button.textContent)).toEqual(['High', 'Urgent'])
    expect(within(card).getByRole('button', { name: 'Save to Personal' })).toBeInTheDocument()
    await user.click(within(card).getByRole('button', { name: 'Create view' }))
    await waitFor(() => expect(create).toHaveBeenCalledOnce())
    expect(create.mock.calls[0][0].filters).toHaveLength(2)
    expect(create.mock.calls[0][0]).toMatchObject({ scope: 'personal' })
  })
})

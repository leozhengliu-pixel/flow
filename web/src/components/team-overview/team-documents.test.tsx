import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { VirtuosoMockContext } from 'react-virtuoso'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { getCommandContext, resetCommandContext, OPEN_COMMAND_MENU_EVENT } from '@/components/command/command-context'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, DocumentPermission, FlowDocument } from '@/types/flow'

const api = vi.hoisted(() => ({
  replaceDocumentPermissions: vi.fn(),
  pinTeamResource: vi.fn(),
  deleteTeamResource: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
const prompt = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))
vi.mock('@/components/ui/action-dialog-service', async importOriginal => ({ ...(await importOriginal<typeof import('@/components/ui/action-dialog-service')>()), promptAction: prompt }))
vi.mock('@/lib/route-pages', () => ({
  AgentChatPanel: ({ pageContext }: { pageContext?: unknown }) => <div data-testid="agent-panel">{JSON.stringify(pageContext)}</div>,
}))

import { encodeFiltersParam } from '@/components/issue-explorer/advanced-filter'
import { TeamDocuments } from './team-documents'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const now = new Date('2026-10-09T12:00:00Z').getTime()
const iso = (offsetMs: number) => new Date(now - offsetMs).toISOString()
const minute = 60_000
const day = 86_400_000

function makeDocument(overrides: Partial<FlowDocument> = {}): FlowDocument {
  return {
    id: 'doc-1', slugId: 'roadmap', title: 'Roadmap', content: '', creator: viewer, teamIds: ['team-1'], projectIds: [], subscriberIds: [], favorite: false,
    revisions: [], createdAt: iso(31 * minute), updatedAt: iso(2 * minute), ...overrides,
  }
}
const permission = (documentId: string, subjectId: string, role: DocumentPermission['role']): DocumentPermission => ({ id: `${documentId}:${subjectId}`, documentId, subjectType: 'user', subjectId, role, createdAt: '', updatedAt: '' })

function setup(documents: FlowDocument[], overrides: Partial<BootstrapData> = {}) {
  const data = makeBootstrap({
    documents, favorites: [], subscriptions: [], initiatives: [], teamPinnedResources: [],
    teamMembers: [{ teamId: 'team-1', userId: viewer.id, role: 'owner', joinedAt: '' }],
    ...overrides,
  } as Partial<BootstrapData>)
  const onNavigate = vi.fn()
  const onReload = vi.fn().mockResolvedValue(undefined)
  const onReloadResources = vi.fn().mockResolvedValue(undefined)
  const view = render(
    <I18nProvider><TooltipProvider><VirtuosoMockContext.Provider value={{ viewportHeight: 600, itemHeight: 48 }}>
      <TeamDocuments creating={false} data={data} documents={documents} onNavigate={onNavigate} onNew={vi.fn()} onReload={onReload} onReloadResources={onReloadResources} resources={[]} team={data.teams[0]}/>
    </VirtuosoMockContext.Provider></TooltipProvider></I18nProvider>,
  )
  return { ...view, data, onNavigate, onReload, onReloadResources }
}

const menuLabels = () => within(screen.getByRole('menu')).getAllByRole('menuitem').map(item => item.getAttribute('data-menu-item') ?? item.textContent)

describe('team documents list', () => {
  beforeEach(() => {
    vi.useRealTimers()
    vi.setSystemTime?.(now)
    localStorage.clear()
    window.history.replaceState(null, '', '/')
    resetCommandContext()
    for (const mock of Object.values(api)) mock.mockReset().mockResolvedValue(undefined)
  })

  it('opens the full document menu on right click, with Linear order and "Pin to overview"', async () => {
    const { container } = setup([makeDocument()])
    fireEvent.contextMenu(container.querySelector('.team-docs-row')!)
    expect(menuLabels()).toEqual(['Move to', 'Pin to overview', 'Duplicate', 'New template from document', 'Rename…', 'Favorite', 'Copy', 'Remind me', 'Show document history', 'Delete'])
    const separators = within(screen.getByRole('menu')).getAllByRole('separator')
    expect(separators).toHaveLength(3)
  })

  it('says "Remove from overview" for a document that is already pinned', () => {
    const pin = { id: 'pin-1', teamId: 'team-1', resourceType: 'document' as const, resourceId: 'doc-1', title: 'Roadmap', position: 0, createdAt: '', updatedAt: '' }
    const { container } = setup([makeDocument()], { teamPinnedResources: [pin] })
    fireEvent.contextMenu(container.querySelector('.team-docs-row')!)
    expect(menuLabels()).toContain('Remove from overview')
    expect(menuLabels()).not.toContain('Pin to overview')
  })

  it('pins to the team overview from the row menu', async () => {
    const user = userEvent.setup()
    const { container, onReload, onReloadResources } = setup([makeDocument()])
    fireEvent.contextMenu(container.querySelector('.team-docs-row')!)
    await user.click(within(screen.getByRole('menu')).getByText('Pin to overview'))
    await waitFor(() => expect(api.pinTeamResource).toHaveBeenCalledWith('team-1', expect.objectContaining({ resourceType: 'document', resourceId: 'doc-1' })))
    await waitFor(() => expect(onReload).toHaveBeenCalled())
    expect(onReloadResources).toHaveBeenCalled()
  })

  it('runs a row key hint (⇧R renames) on the focused row', async () => {
    setup([makeDocument()])
    await userEvent.keyboard('{ArrowDown}')
    await userEvent.keyboard('{Shift>}R{/Shift}')
    await waitFor(() => expect(prompt).toHaveBeenCalledWith('Rename document', 'Roadmap', expect.anything()))
  })

  it('opens the same menu from the row "…" button', async () => {
    const user = userEvent.setup()
    setup([makeDocument()])
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    expect(menuLabels()).toEqual(['Move to', 'Pin to overview', 'Duplicate', 'New template from document', 'Rename…', 'Favorite', 'Copy', 'Remind me', 'Show document history', 'Delete'])
  })

  it('shows "Untitled" for an empty title and keeps the row a link', () => {
    const { container } = setup([makeDocument({ title: '' })])
    const row = container.querySelector<HTMLAnchorElement>('.team-docs-row')!
    expect(row.tagName).toBe('A')
    expect(row.href).toContain('/document/')
    expect(within(row).getByText('Untitled')).toBeVisible()
  })

  it('navigates on click but not when clicking the checkbox or owner pill', async () => {
    const user = userEvent.setup()
    const { onNavigate } = setup([makeDocument()])
    await user.click(screen.getByText('Roadmap'))
    expect(onNavigate).toHaveBeenCalledWith(expect.stringContaining('/document/roadmap'))
    onNavigate.mockClear()
    await user.click(screen.getByRole('checkbox', { name: 'Select document' }))
    expect(onNavigate).not.toHaveBeenCalled()
  })

  it('shows relative times through the i18n formatter with a full-date tooltip', () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(now)
    const { container } = setup([makeDocument({ createdAt: iso(9 * day), updatedAt: iso(30 * 1000) })])
    const [created, updated] = [...container.querySelectorAll<HTMLElement>('.team-docs-row time')]
    expect(created).toHaveTextContent('9d ago')
    expect(updated).toHaveTextContent('just now')
    expect(created.title).not.toBe('')
    vi.useRealTimers()
  })

  it('formats relative times in Chinese', () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(now)
    const { container } = setup([makeDocument({ createdAt: iso(10 * day), updatedAt: iso(31 * minute) })])
    const [created, updated] = [...container.querySelectorAll<HTMLElement>('.team-docs-row time')]
    expect(created).toHaveTextContent('10天前')
    expect(updated).toHaveTextContent('31分钟前')
    vi.useRealTimers()
  })

  it('shows "No owner" for a document whose owner was cleared and lets you pick one', async () => {
    const user = userEvent.setup()
    const doc = makeDocument({ permissions: [permission('doc-1', teammate.id, 'editor')] })
    const { container } = setup([doc])
    const pill = container.querySelector<HTMLElement>('.team-docs-owner')!
    expect(pill).toHaveTextContent('No owner')
    await user.click(pill)
    const option = await screen.findByRole('option', { name: /Teammate/ })
    await user.click(option)
    await waitFor(() => expect(api.replaceDocumentPermissions).toHaveBeenCalledWith('doc-1', expect.arrayContaining([{ subjectType: 'user', subjectId: teammate.id, role: 'owner' }])))
  })

  it('shows the owner name with an 18px avatar for owned documents', () => {
    const doc = makeDocument({ permissions: [permission('doc-1', teammate.id, 'owner')] })
    const { container } = setup([doc])
    const pill = container.querySelector<HTMLElement>('.team-docs-owner')!
    expect(pill).toHaveTextContent('Teammate')
    expect(pill.querySelector('.team-docs-avatar')).not.toBeNull()
  })

  it('groups by owner including a "No owner" group', () => {
    window.history.replaceState(null, '', '/?doc-group=owner')
    const owned = makeDocument({ id: 'doc-a', slugId: 'a', title: 'Alpha', permissions: [permission('doc-a', teammate.id, 'owner')] })
    const orphan = makeDocument({ id: 'doc-b', slugId: 'b', title: 'Beta', permissions: [permission('doc-b', teammate.id, 'editor')] })
    const { container } = setup([owned, orphan])
    const titles = [...container.querySelectorAll('.team-docs-group-title')].map(item => item.textContent)
    expect(titles).toEqual(['teammate', 'No owner'].map(name => name === 'teammate' ? teammate.displayName : name))
  })

  describe('keyboard', () => {
    const docs = [makeDocument({ id: 'doc-a', slugId: 'a', title: 'Alpha' }), makeDocument({ id: 'doc-b', slugId: 'b', title: 'Beta' }), makeDocument({ id: 'doc-c', slugId: 'c', title: 'Gamma' })]
    const rows = (container: HTMLElement) => [...container.querySelectorAll<HTMLElement>('.team-docs-row')]

    it('moves a focus ring through rows only, not group headers', async () => {
      const { container } = setup(docs)
      await userEvent.keyboard('{ArrowDown}')
      expect(document.activeElement).toBe(rows(container)[0])
      expect(rows(container)[0]).toHaveAttribute('data-active')
      await userEvent.keyboard('{ArrowDown}{ArrowDown}')
      expect(document.activeElement).toBe(rows(container)[2])
      await userEvent.keyboard('{ArrowDown}')
      expect(document.activeElement).toBe(rows(container)[2])
      await userEvent.keyboard('{ArrowUp}')
      expect(document.activeElement).toBe(rows(container)[1])
    })

    it('opens the focused document with Enter', async () => {
      const { container, onNavigate } = setup(docs)
      await userEvent.keyboard('{ArrowDown}{Enter}')
      expect(document.activeElement).toBe(rows(container)[0])
      await waitFor(() => expect(onNavigate).toHaveBeenCalledWith(expect.stringContaining('/document/a')))
    })

    it('toggles selection with Space and x, and clears it with Escape', async () => {
      const { container } = setup(docs)
      await userEvent.keyboard('{ArrowDown}x')
      expect(rows(container)[0]).toHaveAttribute('data-selected', 'true')
      await userEvent.keyboard('{ArrowDown} ')
      expect(rows(container)[1]).toHaveAttribute('data-selected', 'true')
      expect(screen.getByRole('toolbar', { name: 'Selected documents' })).toHaveTextContent('2 selected')
      await userEvent.keyboard('x')
      expect(rows(container)[1]).toHaveAttribute('data-selected', 'false')
      await userEvent.keyboard('{Escape}')
      expect(screen.queryByRole('toolbar', { name: 'Selected documents' })).toBeNull()
    })

    it('ignores keys typed into the search field', async () => {
      const { container } = setup(docs)
      fireEvent.keyDown(window, { key: 'f', metaKey: true })
      await userEvent.type(screen.getByRole('textbox', { name: 'Find documents' }), 'x')
      expect(container.querySelectorAll('[data-selected="true"]')).toHaveLength(0)
    })
  })

  describe('selection bar', () => {
    it('shows the count, Actions, an agent button and a clear button', async () => {
      const user = userEvent.setup()
      setup([makeDocument()])
      await user.click(screen.getByRole('checkbox', { name: 'Select document' }))
      const bar = screen.getByRole('toolbar', { name: 'Selected documents' })
      expect(bar).toHaveTextContent('1 selected')
      expect(within(bar).getByRole('button', { name: /Actions/ })).toBeVisible()
      expect(within(bar).getByRole('button', { name: 'Ask agent' })).toBeVisible()
      expect(within(bar).getByRole('button', { name: 'Clear selection' })).toBeVisible()
    })

    it('Actions scopes the command menu to the selected document and opens it', async () => {
      const user = userEvent.setup()
      const opened = vi.fn()
      window.addEventListener(OPEN_COMMAND_MENU_EVENT, opened)
      setup([makeDocument()])
      await user.click(screen.getByRole('checkbox', { name: 'Select document' }))
      await user.click(screen.getByRole('button', { name: /Actions/ }))
      expect(opened).toHaveBeenCalledTimes(1)
      const context = getCommandContext()
      expect(context?.kind).toBe('document')
      expect(context?.kind === 'document' && context.document.id).toBe('doc-1')
      window.removeEventListener(OPEN_COMMAND_MENU_EVENT, opened)
    })

    it('scopes the command menu to every selected document', async () => {
      const user = userEvent.setup()
      setup([makeDocument(), makeDocument({ id: 'doc-2', slugId: 'two', title: 'Two' })])
      for (const box of screen.getAllByRole('checkbox', { name: 'Select document' })) await user.click(box)
      const context = getCommandContext()
      expect(context?.kind === 'document' && context.documents?.map(item => item.id).sort()).toEqual(['doc-1', 'doc-2'])
    })

    it('drops the command context when the selection is cleared', async () => {
      const user = userEvent.setup()
      setup([makeDocument()])
      await user.click(screen.getByRole('checkbox', { name: 'Select document' }))
      await user.click(screen.getByRole('button', { name: 'Clear selection' }))
      expect(getCommandContext()).toBeUndefined()
    })

    it('opens the agent with the selected documents as context', async () => {
      const user = userEvent.setup()
      setup([makeDocument(), makeDocument({ id: 'doc-2', slugId: 'two', title: '' })])
      for (const box of screen.getAllByRole('checkbox', { name: 'Select document' })) await user.click(box)
      await act(async () => { await user.click(screen.getByRole('button', { name: 'Ask agent' })) })
      const panel = await screen.findByTestId('agent-panel')
      expect(JSON.parse(panel.textContent ?? '[]')).toEqual([
        { type: 'document', id: 'doc-1', label: 'Roadmap' },
        { type: 'document', id: 'doc-2', label: 'Untitled' },
      ])
    })
  })

  describe('filter and display menus (the issue list primitives)', () => {
    const agent = { ...teammate, id: 'agent-flow', name: 'Flow', displayName: 'Flow', email: 'flow@agent.local', app: true, active: true }
    const withAgent = () => ({ users: [viewer, teammate, agent] } as Partial<BootstrapData>)
    const openFilter = async () => {
      await userEvent.click(screen.getByRole('button', { name: 'Add filter' }))
      return await screen.findByRole('dialog')
    }
    const hoverField = async (name: string) => {
      const item = within(document.querySelector<HTMLElement>('[data-variant="root"]')!).getByRole('option', { name: new RegExp(`^${name}`) })
      fireEvent.mouseMove(item)
      return item
    }
    const glyph = (element: Element) => element.querySelector('[data-linear-glyph]')?.getAttribute('data-linear-glyph')

    it('lists Linear\'s filter menu: search with the F key, Advanced filter, Creator / Owner, Project / Dates', async () => {
      setup([makeDocument()], withAgent())
      const menu = await openFilter()
      const search = menu.querySelector('[cmdk-input]')!
      expect(search).toHaveAttribute('placeholder', 'Add Filter…')
      expect(menu.querySelector('kbd')).toHaveTextContent('F')
      const rows = within(menu).getAllByRole('option')
      expect(rows.map(row => row.textContent?.replace('▶', ''))).toEqual(['Advanced filter', 'Creator', 'Owner', 'Project', 'Dates'])
      expect(menu.querySelectorAll('[cmdk-separator]')).toHaveLength(2)
      expect(rows.map(row => row.querySelectorAll('.rootChevron, [class*="rootChevron"]').length)).toEqual([0, 1, 1, 1, 1])
      // Linear glyphs, not lucide: Advanced filter / Creator / Dates carry their glyph name.
      expect([glyph(rows[0]), glyph(rows[1]), glyph(rows[4])]).toEqual(['advancedFilter', 'creator', 'dates'])
      expect(rows[2].querySelector('svg')).toBeInTheDocument()
      expect(rows[3].querySelector('svg')).toBeInTheDocument()
      expect(menu.querySelector('svg.lucide')).toBeNull()
    })

    it('opens the menu with F and closes it with Escape', async () => {
      setup([makeDocument()])
      await userEvent.keyboard('f')
      expect(await screen.findByRole('dialog')).toBeVisible()
      await userEvent.keyboard('{Escape}')
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    })

    it('Owner submenu: search, "No owner", "Current user", people with avatars and agents with the Agent pill', async () => {
      setup([makeDocument()], withAgent())
      await openFilter()
      await hoverField('Owner')
      const list = await screen.findByRole('listbox', { name: 'Owner' })
      const submenu = list.closest('[data-field="owner"]') as HTMLElement
      expect(within(submenu).getByRole('searchbox', { name: 'Filter Owner' })).toHaveAttribute('placeholder', 'Filter…')
      expect(submenu.querySelector('[data-hidden]')).toBeNull()
      const rows = within(list).getAllByRole('option')
      expect(rows.map(row => row.querySelector('[class*="valueLabel"]')?.textContent)).toEqual(['No owner', 'Current user', viewer.displayName, teammate.displayName, 'Flow'])
      // Every row reserves the checkbox column (shown on hover or when selected).
      expect(rows.every(row => row.querySelector('[role="checkbox"]'))).toBe(true)
      expect(glyph(rows[0])).toBe('owner')
      expect(rows[1].querySelector('svg')).toBeInTheDocument()
      expect(rows[2].querySelector('[class*="optionAvatar"]')).toBeInTheDocument()
      expect(rows.slice(0, 4).some(row => row.querySelector('[data-agent-badge]'))).toBe(false)
      expect(rows[4].querySelector('[data-agent-badge]')).toHaveTextContent('Agent')
    })

    it('Creator submenu has no search band while it lists three people (Linear)', async () => {
      setup([makeDocument()], { users: [viewer, agent] } as Partial<BootstrapData>)
      await openFilter()
      await hoverField('Creator')
      const list = await screen.findByRole('listbox', { name: 'Creator' })
      const submenu = list.closest('[data-field="creator"]') as HTMLElement
      expect(submenu.querySelector('[data-hidden]')).not.toBeNull()
      expect(within(list).getAllByRole('option').map(row => row.querySelector('[class*="valueLabel"]')?.textContent)).toEqual(['Current user', viewer.displayName, 'Flow'])
    })

    it('Project submenu offers "No project" and the projects; Dates offers Created / Updated date presets', async () => {
      const { data } = setup([makeDocument()])
      await openFilter()
      await hoverField('Project')
      const list = await screen.findByRole('listbox', { name: 'Project' })
      const labels = within(list).getAllByRole('option').map(row => row.querySelector('[class*="valueLabel"]')?.textContent)
      expect(labels[0]).toBe('No project')
      expect(labels.slice(1)).toEqual(data.projects.filter(project => !project.archivedAt).map(project => project.name))
      await hoverField('Dates')
      const dates = await screen.findByRole('listbox', { name: 'Dates' })
      const categories = within(dates).getAllByRole('option')
      expect(categories.map(row => row.querySelector('[class*="valueLabel"]')?.textContent)).toEqual(['Created date', 'Updated date'])
      expect([glyph(categories[0]), glyph(categories[1])]).toEqual(['createdDate', 'updatedDate'])
      fireEvent.mouseMove(categories[1])
      const presets = await screen.findByRole('listbox', { name: 'Updated date' })
      expect(within(presets).getAllByRole('option').map(row => row.textContent).slice(0, 3)).toEqual(['1 day ago', '3 days ago', '1 week ago'])
    })

    it('applies a value as a segmented chip with the operator menu and filters the list', async () => {
      const documents = [
        makeDocument({ id: 'doc-1', title: 'Mine', permissions: [permission('doc-1', viewer.id, 'owner')] }),
        makeDocument({ id: 'doc-2', slugId: 'theirs', title: 'Theirs', permissions: [permission('doc-2', teammate.id, 'owner')] }),
      ]
      setup(documents, withAgent())
      await openFilter()
      await hoverField('Owner')
      const list = await screen.findByRole('listbox', { name: 'Owner' })
      await userEvent.click(within(list).getByRole('option', { name: /Current user/ }))
      const bar = await screen.findByLabelText('Applied filters')
      expect(bar).toHaveTextContent('Owner')
      expect(bar).toHaveTextContent('is')
      expect(bar).toHaveTextContent('Current user')
      expect(screen.getByText('Mine')).toBeVisible()
      expect(screen.queryByText('Theirs')).toBeNull()
      await userEvent.click(within(bar).getByRole('button', { name: 'Owner operator' }))
      expect((await screen.findAllByRole('menuitemradio')).map(item => item.textContent)).toEqual(['is', 'is not'])
      await userEvent.click(screen.getByRole('menuitemradio', { name: 'is not' }))
      expect(screen.getByText('Theirs')).toBeVisible()
      expect(screen.queryByText('Mine')).toBeNull()
      await userEvent.click(within(bar).getByRole('button', { name: 'Clear all filters' }))
      expect(screen.getByText('Mine')).toBeVisible()
      expect(screen.getByText('Theirs')).toBeVisible()
    })

    it('adds an advanced filter chip whose editor only offers the document fields', async () => {
      setup([makeDocument()])
      await openFilter()
      await userEvent.click(within(document.querySelector<HTMLElement>('[data-variant="root"]')!).getByRole('option', { name: 'Advanced filter' }))
      const bar = await screen.findByLabelText('Applied filters')
      expect(bar.querySelector('[data-advanced-chip]')).toBeInTheDocument()
      await userEvent.click(within(await screen.findByRole('dialog', { name: 'Advanced filter' })).getByRole('button', { name: 'Add filter' }))
      const menu = (await screen.findAllByRole('dialog')).find(dialog => dialog.getAttribute('data-variant') === 'advanced')!
      expect(within(menu).getAllByRole('option').map(row => row.textContent?.replace('▶', ''))).toEqual(['Add filter group', 'Creator', 'Owner', 'Project', 'Dates'])
      expect(menu.querySelector('[cmdk-input]')).toHaveAttribute('placeholder', 'Filter')
      expect(menu.querySelector('kbd')).toBeNull()
    })

    it('shows Linear\'s display popover: Grouping, Ordering with direction, toggles and display properties', async () => {
      const { container } = setup([makeDocument()])
      await userEvent.click(screen.getByRole('button', { name: 'Display options' }))
      const menu = await screen.findByRole('dialog', { name: 'Display options' })
      expect(within(menu).getByRole('combobox', { name: 'Grouping' })).toHaveTextContent('Project')
      expect(within(menu).getByRole('combobox', { name: 'Ordering' })).toHaveTextContent('Name')
      expect(menu.querySelector('[data-sort-direction]')).toBeInTheDocument()
      expect(within(menu).getAllByRole('checkbox').map(item => item.getAttribute('aria-label'))).toEqual(['Show inactive projects', 'Show only my projects'])
      expect(within(menu).getAllByRole('button', { pressed: true }).map(item => item.textContent)).toEqual(['Owner', 'Last edited', 'Created'])
      await userEvent.click(within(menu).getByRole('combobox', { name: 'Grouping' }))
      expect((await screen.findAllByRole('option')).map(option => option.textContent)).toEqual(['None', 'Owner', 'Cycle', 'Project', 'Recency'])
      await userEvent.keyboard('{Escape}')
      await userEvent.click(within(menu).getByRole('combobox', { name: 'Ordering' }))
      expect((await screen.findAllByRole('option')).map(option => option.textContent)).toEqual(['Owner', 'Last edited', 'Created', 'Name', 'Project'])
      await userEvent.keyboard('{Escape}')
      await userEvent.click(within(menu).getByRole('button', { name: 'Owner' }))
      expect(container.querySelector('.team-docs-heading.is-owner')).toBeNull()
      await userEvent.click(within(menu).getByRole('checkbox', { name: 'Show only my projects' }))
      expect(within(menu).getByRole('checkbox', { name: 'Show only my projects' })).toBeChecked()
    })
  })

  describe('empty states', () => {
    it('shows the onboarding state when the team has no documents', () => {
      setup([])
      expect(screen.getByRole('heading', { name: 'Team documents' })).toBeVisible()
      expect(screen.getByText('Create documents to share notes, decisions, and plans with your team.')).toBeVisible()
      expect(screen.getByRole('button', { name: 'Create document' })).toBeVisible()
    })

    it('says "No documents matching your filters" when filters hide everything', () => {
      window.history.replaceState(null, '', `/?doc-filter=${encodeFiltersParam([{ id: 'c', field: 'creator', fieldLabel: 'Creator', operator: 'is', value: 'nobody', valueLabel: 'Nobody', values: [{ value: 'nobody', valueLabel: 'Nobody' }] }])}`)
      setup([makeDocument()])
      expect(screen.getByRole('heading', { name: 'No documents matching your filters' })).toBeVisible()
    })
  })
})

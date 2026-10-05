import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { Loop } from '@/types/flow'

const api = vi.hoisted(() => ({ listLoops: vi.fn(), listLoopTemplates: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { LoopList } from './loop-list'

const base: Pick<Loop, 'connectorIds' | 'teamAccess' | 'allowChangesOutsideTrigger' | 'allowExternalSync' | 'instructions' | 'createdAt' | 'updatedAt'> = {
  connectorIds: [], teamAccess: 'allPublic', allowChangesOutsideTrigger: true, allowExternalSync: false, instructions: 'Do it',
  createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-29T00:00:00Z',
}
const loops: Loop[] = [
  { ...base, id: 'loop-1', name: 'Triage agent', description: 'Routes incoming issues.', status: 'published', level: 'team', teamId: 'team-1', triggerType: 'issue', triggerConfig: { event: 'triage' }, enabled: true, ownerId: viewer.id, creator: viewer, runCount30d: 4, lastRunAt: new Date(Date.now() - 2 * 3600_000).toISOString() },
  { ...base, id: 'loop-2', name: 'Weekly wrap', description: 'Shares the highlights.', status: 'published', level: 'workspace', triggerType: 'schedule', triggerConfig: { unit: 'week', interval: 1 }, enabled: true, ownerId: teammate.id, creator: teammate, runCount30d: 1 },
  { ...base, id: 'loop-3', name: '', status: 'draft', level: 'workspace', triggerType: 'schedule', triggerConfig: {}, enabled: false, creator: viewer },
  { ...base, id: 'loop-4', name: 'Someone else draft', status: 'draft', level: 'workspace', triggerType: 'schedule', triggerConfig: {}, enabled: false, creator: teammate },
]

function renderList(items: Loop[] = loops) {
  api.listLoops.mockResolvedValue(items)
  const onNavigate = vi.fn()
  render(<I18nProvider><TooltipProvider><LoopList data={makeBootstrap({ loops: items, favorites: [] })} embedded={false} onNavigate={onNavigate} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></TooltipProvider></I18nProvider>)
  return { onNavigate }
}

type User = ReturnType<typeof userEvent.setup>
// jsdom has no submenu geometry for Radix's pointer grace polygon: open submenus on hover, choose with the keyboard.
async function openSubmenu(user: User, name: string) {
  const trigger = await screen.findByRole('menuitem', { name })
  await user.hover(trigger)
  await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'true'))
  return document.getElementById(trigger.getAttribute('aria-controls')!)!
}
async function choose(user: User, item: HTMLElement) {
  item.focus()
  await user.keyboard('{Enter}')
}
async function enterRuns(user: User, value: string) {
  const input = await screen.findByRole('textbox', { name: 'Enter runs (30d)…' })
  input.focus()
  await user.type(input, `${value}{Enter}`, { skipClick: true })
  return input
}

describe('LoopList', () => {
  beforeEach(() => {
    localStorage.clear()
    api.listLoopTemplates.mockReset().mockResolvedValue([])
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  })

  it('shows the Linear table grouped by workspace and team', async () => {
    const user = userEvent.setup()
    const { onNavigate } = renderList()
    expect(screen.getByRole('heading', { name: 'Loops' })).toBeVisible()
    expect(screen.getAllByRole('button', { name: 'New loop' })[0]).toHaveClass('loops-new-button')
    expect(screen.getByRole('tab', { name: 'My loops' })).toBeVisible()
    expect(screen.getByRole('tab', { name: 'All' })).toHaveAttribute('aria-selected', 'true')
    const headers = screen.getAllByRole('columnheader').map(header => header.textContent)
    expect(headers.slice(0, 5)).toEqual(['Name', 'Trigger', 'Owner', 'Runs (30d)', 'Last executed'])
    const groups = screen.getAllByRole('rowgroup')
    expect(groups[0]).toHaveTextContent('Workspace')
    expect(groups[1]).toHaveTextContent('Test team')
    const triage = within(groups[1]).getAllByRole('row')[1]
    expect(triage).toHaveTextContent('Triage agent')
    expect(triage).toHaveTextContent('Routes incoming issues.')
    expect(triage).toHaveTextContent('Triage')
    expect(triage).toHaveTextContent('Viewer')
    expect(triage).toHaveTextContent('4')
    // Linear's compact age in the right-aligned "Last executed" column.
    expect(within(triage).getAllByRole('cell').at(-1)).toHaveTextContent(/^2h$/)
    // No ⋯ button on rows: the loop menu opens on right-click.
    expect(within(triage).queryByRole('button', { name: 'Open actions' })).toBeNull()
    // The viewer's own draft is listed and marked; other people's drafts stay private.
    expect(screen.getByText('Draft')).toBeVisible()
    expect(screen.queryByText('Someone else draft')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Triage agent' }))
    expect(onNavigate).toHaveBeenCalledWith('/workspace/loop/loop-1')
    await user.click(screen.getByRole('button', { name: 'Untitled loop' }))
    expect(onNavigate).toHaveBeenCalledWith('/workspace/loops/new?draftId=loop-3')
  })

  it('filters to my loops and by search', async () => {
    const user = userEvent.setup()
    renderList()
    await user.click(screen.getByRole('tab', { name: 'My loops' }))
    expect(screen.queryByText('Weekly wrap')).toBeNull()
    await user.click(screen.getByRole('tab', { name: 'All' }))
    // Linear: the search field sits inline right after the tabs; filter and display stay on the right.
    const search = screen.getByRole('searchbox', { name: 'Find loops…' })
    expect(screen.queryByRole('button', { name: 'Find loops…' })).toBeNull()
    const left = search.closest('.loops-toolbar-left')!
    expect(left).toContainElement(screen.getByRole('tablist', { name: 'Loops' }))
    expect(screen.getByRole('button', { name: 'Add filter' }).closest('.loops-toolbar-right')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Display options' }).closest('.loops-toolbar-right')).not.toBeNull()
    await user.type(search, 'weekly')
    expect(screen.getByText('Weekly wrap')).toBeVisible()
    expect(screen.queryByText('Triage agent')).toBeNull()
  })

  it('opens the loop menu on right-click and collapses groups', async () => {
    const user = userEvent.setup()
    renderList()
    const groups = screen.getAllByRole('rowgroup')
    const triage = within(groups[1]).getAllByRole('row')[1]
    await user.pointer({ keys: '[MouseRight]', target: triage })
    const menu = await screen.findByRole('menu')
    expect(within(menu).getByRole('menuitem', { name: 'Edit' })).toBeVisible()
    expect(within(menu).getByRole('menuitem', { name: 'Show run history' })).toBeVisible()
    await user.keyboard('{Escape}')
    const toggle = within(groups[1]).getByRole('button', { name: /Test team/ })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('Triage agent')).toBeNull()
  })

  it('shows the filtered empty state with Clear Filters', async () => {
    const user = userEvent.setup()
    renderList()
    await user.type(screen.getByRole('searchbox', { name: 'Find loops…' }), 'zzz')
    expect(screen.getByRole('heading', { name: 'No loops matching the filters' })).toBeVisible()
    expect(screen.getByText('3 loops')).toBeVisible()
    expect(screen.queryByRole('table')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Clear Filters' }))
    expect(screen.getByRole('table', { name: 'Loops' })).toBeVisible()
  })

  it('shows the creation hub inline when there are no loops', async () => {
    renderList([])
    expect(await screen.findByRole('heading', { name: 'Create a new loop' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Start from scratch' })).toBeVisible()
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('selects rows with checkboxes, shift-click ranges, ⌘A and Escape, and acts on the selection from the menu', async () => {
    const user = userEvent.setup()
    renderList()
    const boxes = await screen.findAllByRole('checkbox', { name: 'Select loop' })
    expect(boxes).toHaveLength(3)
    await user.click(boxes[0])
    expect(boxes[0]).toHaveAttribute('aria-checked', 'true')
    await user.keyboard('{Shift>}')
    await user.click(boxes[2])
    await user.keyboard('{/Shift}')
    expect(screen.getAllByRole('checkbox', { name: 'Select loop' }).map(box => box.getAttribute('aria-checked'))).toEqual(['true', 'true', 'true'])
    await user.keyboard('{Escape}')
    expect(screen.getAllByRole('checkbox', { name: 'Select loop' }).every(box => box.getAttribute('aria-checked') === 'false')).toBe(true)
    await user.keyboard('{Control>}a{/Control}')
    expect(screen.getAllByRole('row', { selected: true })).toHaveLength(3)
    const row = screen.getAllByRole('row', { selected: true })[0]
    await user.pointer({ keys: '[MouseRight]', target: row })
    const menu = await screen.findByRole('menu')
    expect(menu).toHaveTextContent('3 loops selected')
    for (const name of ['Disable', 'Move', 'Change owner', 'Who can edit', 'Delete']) expect(within(menu).getByRole('menuitem', { name })).toBeVisible()
  })

  it('opens Linear\'s filter menu: Add Filter search, Advanced filter, then Owner, Last executed and Runs (30d)', async () => {
    const user = userEvent.setup()
    renderList()
    const trigger = screen.getByRole('button', { name: 'Add filter' })
    expect(trigger).toHaveAttribute('aria-keyshortcuts', 'F')
    // The F key opens it, like Linear.
    await user.keyboard('f')
    const menu = await screen.findByRole('menu')
    const search = within(menu).getByRole('textbox', { name: 'Add Filter…' })
    expect(search).toHaveAttribute('placeholder', 'Add Filter…')
    expect(menu.querySelector('kbd')).toHaveTextContent('F')
    expect(within(menu).getByRole('menuitem', { name: 'Advanced filter' })).toBeVisible()
    expect(within(menu).getAllByRole('menuitem').map(item => item.textContent?.replace('▶', '').trim())).toEqual(['Advanced filter', 'Owner', 'Last executed', 'Runs (30d)'])
    // Typing filters the property list.
    await user.type(search, 'own')
    expect(within(menu).getAllByRole('menuitem').map(item => item.textContent?.replace('▶', '').trim())).toEqual(['Owner'])
  })

  it('filters by owner with Linear\'s per-owner loop counts and shows a removable chip', async () => {
    const user = userEvent.setup()
    renderList()
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    await openSubmenu(user, 'Owner')
    const choices = await screen.findAllByRole('menuitemcheckbox')
    expect(choices.map(choice => choice.textContent)).toEqual([expect.stringMatching(/Current user.*2 loops/), expect.stringMatching(/Teammate.*1 loop$/), expect.stringMatching(/Viewer.*2 loops/)])
    await choose(user, choices[1])
    expect(choices[1]).toHaveAttribute('aria-checked', 'true')
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    const bar = screen.getByRole('group', { name: 'Loop filters' })
    expect(within(bar).getByText('Owner')).toBeVisible()
    expect(within(bar).getByRole('combobox', { name: 'Owner operator' })).toHaveTextContent('is')
    expect(within(bar).getByRole('button', { name: 'Owner values' })).toHaveTextContent('Teammate')
    expect(screen.getByText('Weekly wrap')).toBeVisible()
    expect(screen.queryByText('Triage agent')).toBeNull()
    await user.click(within(bar).getByRole('button', { name: 'Remove Owner filter' }))
    expect(screen.queryByRole('group', { name: 'Loop filters' })).toBeNull()
    expect(screen.getByText('Triage agent')).toBeVisible()
  })

  it('filters by Last executed buckets, including Never executed, and offers a custom date or timeframe', async () => {
    const user = userEvent.setup()
    renderList()
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    const submenu = await openSubmenu(user, 'Last executed')
    expect(within(submenu).getByRole('textbox', { name: 'Filter…' })).toBeVisible()
    const labels = within(submenu).getAllByRole('menuitem').map(item => item.textContent)
    expect(labels).toEqual(['Never executed2 loops', '1 day ago1 loop', '3 days ago1 loop', '1 week ago1 loop', '1 month ago1 loop', '3 months ago1 loop', '6 months ago1 loop', '1 year ago1 loop', 'Custom date or timeframe…'])
    await choose(user, within(submenu).getByRole('menuitem', { name: /Never executed/ }))
    const bar = screen.getByRole('group', { name: 'Loop filters' })
    expect(within(bar).getByRole('button', { name: 'Last executed values' })).toHaveTextContent('Never executed')
    expect(screen.getByText('Weekly wrap')).toBeVisible()
    expect(screen.queryByText('Triage agent')).toBeNull()
    // A relative bucket reads "after 1 day ago".
    await user.click(within(bar).getByRole('button', { name: 'Last executed values' }))
    await choose(user, within(await openSubmenu(user, 'Last executed')).getByRole('menuitem', { name: /1 day ago/ }))
    expect(within(bar).getByRole('combobox', { name: 'Last executed operator' })).toHaveTextContent('after')
    expect(screen.getByText('Triage agent')).toBeVisible()
    expect(screen.queryByText('Weekly wrap')).toBeNull()
    // Custom date or timeframe… opens the shared date dialog.
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    await choose(user, within(await openSubmenu(user, 'Last executed')).getByRole('menuitem', { name: 'Custom date or timeframe…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Last executed' })
    expect(within(dialog).getByRole('button', { name: 'Apply filter' })).toBeVisible()
  })

  it('filters by Runs (30d) from a numeric input with an operator chip, and Clear removes every filter', async () => {
    const user = userEvent.setup()
    renderList()
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    await openSubmenu(user, 'Runs (30d)')
    const input = await enterRuns(user, '2')
    expect(input).toHaveAttribute('placeholder', 'Enter runs (30d)…')
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    const bar = screen.getByRole('group', { name: 'Loop filters' })
    expect(within(bar).getByText('Runs (30d)')).toBeVisible()
    expect(within(bar).getByRole('combobox', { name: 'Runs (30d) operator' })).toHaveTextContent('≥')
    expect(within(bar).getByRole('button', { name: 'Runs (30d) values' })).toHaveTextContent('2')
    expect(screen.getByText('Triage agent')).toBeVisible()
    expect(screen.queryByText('Weekly wrap')).toBeNull()
    await user.click(within(bar).getByRole('combobox', { name: 'Runs (30d) operator' }))
    await user.click(screen.getByRole('option', { name: '<' }))
    expect(screen.getByText('Weekly wrap')).toBeVisible()
    expect(screen.queryByText('Triage agent')).toBeNull()
    await user.click(within(bar).getByRole('button', { name: 'Clear' }))
    expect(screen.queryByRole('group', { name: 'Loop filters' })).toBeNull()
    expect(screen.getByText('Triage agent')).toBeVisible()
    expect(screen.getByText('Weekly wrap')).toBeVisible()
  })

  it('keeps the filtered empty state with the hidden count for menu filters', async () => {
    const user = userEvent.setup()
    renderList()
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    await openSubmenu(user, 'Runs (30d)')
    await enterRuns(user, '99')
    expect(screen.getByRole('heading', { name: 'No loops matching the filters' })).toBeVisible()
    expect(screen.getByText('3 loops')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Clear Filters' }))
    expect(screen.getByRole('table', { name: 'Loops' })).toBeVisible()
  })

  it('opens the advanced filter with Match all/any', async () => {
    const user = userEvent.setup()
    renderList()
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Advanced filter' }))
    const bar = screen.getByRole('group', { name: 'Loop filters' })
    expect(within(bar).getByText('Match')).toBeVisible()
    const conjunction = within(bar).getByRole('combobox', { name: 'Filter conjunction' })
    expect(conjunction).toHaveTextContent('all filters')
    await user.click(conjunction)
    await user.click(screen.getByRole('option', { name: 'any filter' }))
    expect(conjunction).toHaveTextContent('any filter')
    // Any of: owner Teammate OR runs ≥ 4 keeps both published loops.
    await user.click(within(bar).getByRole('button', { name: 'Add another filter' }))
    await openSubmenu(user, 'Owner')
    await choose(user, screen.getAllByRole('menuitemcheckbox')[1])
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    await user.click(within(bar).getByRole('button', { name: 'Add another filter' }))
    await openSubmenu(user, 'Runs (30d)')
    await enterRuns(user, '4')
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    expect(screen.getByText('Weekly wrap')).toBeVisible()
    expect(screen.getByText('Triage agent')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Untitled loop' })).toBeNull()
    await user.click(within(bar).getByRole('button', { name: 'Remove advanced filter' }))
    // Back to "all": nothing is owned by Teammate with ≥ 4 runs.
    expect(screen.getByRole('heading', { name: 'No loops matching the filters' })).toBeVisible()
  })

  it('shows Linear\'s display options: grouping, ordering with direction, and the two switches, persisted per user', async () => {
    const user = userEvent.setup()
    const disabled: Loop = { ...loops[1], id: 'loop-5', name: 'Paused digest', enabled: false, ownerId: viewer.id, creator: viewer, runCount30d: 9 }
    renderList([...loops, disabled])
    await user.click(screen.getByRole('button', { name: 'Display options' }))
    const popover = await screen.findByRole('dialog', { name: 'Display options' })
    expect(within(popover).queryByText('Display properties')).toBeNull()
    const grouping = within(popover).getByRole('combobox', { name: 'Grouping' })
    expect(grouping).toHaveTextContent('Team')
    await user.click(grouping)
    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['No grouping', 'Team', 'Trigger', 'Owner'])
    await user.click(screen.getByRole('option', { name: 'Trigger' }))
    expect(screen.getAllByRole('rowgroup').map(group => within(group).getAllByRole('button')[0].textContent)).toEqual(['Schedule', 'Issue'])
    await user.click(grouping)
    await user.click(screen.getByRole('option', { name: 'Owner' }))
    expect(screen.getAllByRole('rowgroup').map(group => within(group).getAllByRole('button')[0].textContent)).toEqual(['TTeammate', 'VViewer'])
    await user.click(grouping)
    await user.click(screen.getByRole('option', { name: 'No grouping' }))
    expect(screen.queryByRole('button', { name: /Workspace/ })).toBeNull()
    expect(JSON.parse(localStorage.getItem('flow:loops:display:workspace-1:user-1')!)).toMatchObject({ grouping: 'none' })

    const ordering = within(popover).getByRole('combobox', { name: 'Ordering' })
    expect(ordering).toHaveTextContent('Name')
    await user.click(ordering)
    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['Name', 'Last executed', 'Runs', 'Trigger', 'Team', 'Owner'])
    await user.click(screen.getByRole('option', { name: 'Runs' }))
    const names = () => screen.getAllByRole('row').slice(1).map(row => within(row).getAllByRole('button')[0].getAttribute('aria-label'))
    expect(names()).toEqual(['Paused digest', 'Triage agent', 'Weekly wrap', 'Untitled loop'])
    expect(screen.getByRole('columnheader', { name: 'Runs (30d)' })).toHaveClass('is-sorted')
    const direction = within(popover).getByRole('button', { name: 'Direction' })
    expect(direction).toHaveAttribute('aria-pressed', 'true')
    await user.click(direction)
    expect(direction).toHaveAttribute('aria-pressed', 'false')
    expect(names()).toEqual(['Untitled loop', 'Weekly wrap', 'Triage agent', 'Paused digest'])

    // Switches: disabled loops, then team loops.
    await user.click(within(popover).getByRole('checkbox', { name: 'Show disabled loops' }))
    expect(screen.queryByRole('button', { name: 'Paused digest' })).toBeNull()
    await user.click(within(popover).getByRole('checkbox', { name: 'Show team loops' }))
    expect(screen.queryByRole('button', { name: 'Triage agent' })).toBeNull()
    expect(JSON.parse(localStorage.getItem('flow:loops:display:workspace-1:user-1')!)).toEqual({ grouping: 'none', ordering: 'runs', descending: false, showTeamLoops: false, showDisabledLoops: false })
  })

  it('keeps column header sorting in sync with Ordering', async () => {
    const user = userEvent.setup()
    renderList()
    await user.click(screen.getByRole('columnheader', { name: 'Last executed' }))
    await user.click(screen.getByRole('button', { name: 'Display options' }))
    const popover = await screen.findByRole('dialog', { name: 'Display options' })
    expect(within(popover).getByRole('combobox', { name: 'Ordering' })).toHaveTextContent('Last executed')
    expect(within(popover).getByRole('button', { name: 'Direction' })).toHaveAttribute('aria-pressed', 'true')
    await user.click(within(popover).getByRole('combobox', { name: 'Ordering' }))
    await user.click(screen.getByRole('option', { name: 'Owner' }))
    expect(screen.getByRole('columnheader', { name: 'Owner' })).toHaveClass('is-sorted')
    await user.click(screen.getByRole('columnheader', { name: 'Owner' }))
    expect(within(popover).getByRole('button', { name: 'Direction' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('restores display options saved for this user', () => {
    localStorage.setItem('flow:loops:display:workspace-1:user-1', JSON.stringify({ grouping: 'trigger', ordering: 'runs', descending: true, showTeamLoops: true, showDisabledLoops: true }))
    renderList()
    expect(screen.getAllByRole('rowgroup').map(group => within(group).getAllByRole('button')[0].textContent)).toEqual(['Schedule', 'Issue'])
    expect(screen.getByRole('columnheader', { name: 'Runs (30d)' })).toHaveClass('is-sorted')
  })
})

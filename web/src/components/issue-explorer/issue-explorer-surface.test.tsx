import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { IssueExplorerSurface } from './issue-explorer-surface'
import { setWorkspaceRuntimePreferences } from '@/lib/runtime-preferences'
import styles from './issue-explorer.module.css'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const display = { layout: 'list', grouping: 'status', subGrouping: 'none', properties: new Set(), hiddenGroupIds: [] } as never
beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))
it('opens the filter shortcut while its tooltip is visible', async () => {
  const user = userEvent.setup()
  render(<I18nProvider><TooltipProvider delayDuration={0}><IssueExplorerSurface scopeName="Workspace" activeView="all" viewHref={() => '#'} filters={[]} displayOptions={display} detailsOpen={false} onFilterToggle={vi.fn()} onDisplayOptionsChange={vi.fn()} onDetailsOpenChange={vi.fn()} onNavigateView={vi.fn()} filterOptions={() => []}>Content</IssueExplorerSurface></TooltipProvider></I18nProvider>)
  await user.hover(screen.getByRole('button', { name: 'Add filter' }))
  expect(await screen.findByRole('tooltip')).toBeVisible()
  await user.keyboard('f')
  expect(await screen.findByRole('combobox', { name: 'Add Filter…' })).toBeVisible()
})

it('keeps the create-view filter menu clickable outside the fixed editor panel', async () => {
  const user = userEvent.setup()
  render(<I18nProvider><IssueExplorerSurface creatingView scopeName="Workspace" activeView="all" viewHref={() => '#'} filters={[]} displayOptions={display} detailsOpen={false} onFilterToggle={vi.fn()} onDisplayOptionsChange={vi.fn()} onDetailsOpenChange={vi.fn()} onNavigateView={vi.fn()} filterOptions={() => [{ id: 'open', label: 'Open' }] as never} viewEditor={controls => <div aria-label="New issue view">Editor{controls.filterButton}</div>} onNewViewResourceChange={vi.fn()} onOpenSidebar={vi.fn()}>Content</IssueExplorerSurface></I18nProvider>)
  expect(screen.getByRole('main')).toHaveAttribute('data-inset', 'tall')
  await user.click(screen.getByRole('button', { name: 'Add filter' }))
  expect(document.querySelector('.rootSearch input, input[placeholder="Add Filter…"]')).toBeTruthy()
})

it.each(['mouse', 'keyboard'])('applies a filter from its portaled submenu using %s', async mode => {
  const user = userEvent.setup(), toggle = vi.fn()
  render(<I18nProvider><IssueExplorerSurface creatingView scopeName="Workspace" activeView="all" viewHref={() => '#'} filters={[]} displayOptions={display} detailsOpen={false} onFilterToggle={toggle} onDisplayOptionsChange={vi.fn()} onDetailsOpenChange={vi.fn()} onNavigateView={vi.fn()} filterOptions={field => field === 'priority' ? [{ id: '2', label: 'High' }] : undefined} viewEditor={controls => <div>Editor{controls.filterButton}</div>}>Content</IssueExplorerSurface></I18nProvider>)
  const trigger = screen.getByRole('button', { name: 'Add filter' })
  if (mode === 'mouse') {
    await user.click(trigger)
    fireEvent.mouseMove(screen.getByRole('option', { name: 'Priority' }))
    const option = await screen.findByRole('option', { name: /^High/ })
    const panel = document.querySelector(`.${styles.createPanel}`)
    expect(panel).not.toBeNull()
    expect(panel?.contains(option)).toBe(false)
    await user.click(option)
  } else {
    trigger.focus()
    await user.keyboard('{Enter}')
    await user.type(await screen.findByRole('combobox', { name: 'Add Filter…' }), 'Priority')
    // The first match is active; the search always ends with `AI filter "<query>"`.
    expect(screen.getByRole('option', { name: /AI filter/ })).toBeInTheDocument()
    await user.keyboard('{Enter}')
    const search = await screen.findByRole('searchbox', { name: 'Filter Priority' })
    search.focus()
    await user.keyboard('{ArrowDown}{Enter}')
  }
  expect(toggle).toHaveBeenCalledWith('priority', { id: '2', label: 'High' })
})

it('keeps project milestone off the team issue filter menu', async () => {
  const user = userEvent.setup()
  render(<I18nProvider><IssueExplorerSurface creatingView scopeName="Workspace" activeView="all" viewHref={() => '#'} filters={[]} displayOptions={display} detailsOpen={false} onFilterToggle={vi.fn()} onDisplayOptionsChange={vi.fn()} onDetailsOpenChange={vi.fn()} onNavigateView={vi.fn()} filterOptions={() => []} viewEditor={controls => <div>Editor{controls.filterButton}</div>}>Content</IssueExplorerSurface></I18nProvider>)
  await user.click(screen.getByRole('button', { name: 'Add filter' }))
  expect(screen.getByRole('option', { name: 'Project' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Customers' })).toBeInTheDocument()
  expect(screen.queryByRole('option', { name: 'Project milestone' })).not.toBeInTheDocument()
})

it('renders the team header favorite, notifications, and New issue action', async () => {
  const user = userEvent.setup(), onCreateIssue = vi.fn(), onToggleTeamFavorite = vi.fn()
  render(<I18nProvider><TooltipProvider><IssueExplorerSurface scopeName="Verify" scopeTeam={{ id: 'team-1', key: 'VER', name: 'Verify', color: '#fff', icon: 'Team' } as never} activeView="all" viewHref={() => '#'} filters={[]} displayOptions={display} detailsOpen={false} favorite={false} teamFavorite={false} onToggleTeamFavorite={onToggleTeamFavorite} teamNotificationsHref="/verify/settings/team/VER/notifications" onToggleFavorite={vi.fn()} onFilterToggle={vi.fn()} onDisplayOptionsChange={vi.fn()} onDetailsOpenChange={vi.fn()} onNavigateView={vi.fn()} filterOptions={() => []} onCreateIssue={onCreateIssue}>Content</IssueExplorerSurface></TooltipProvider></I18nProvider>)
  await user.click(screen.getByRole('switch', { name: 'Add to favorites' }))
  expect(onToggleTeamFavorite).toHaveBeenCalledOnce()
  expect(screen.getByRole('link', { name: 'Setup team notifications' })).toHaveAttribute('href', '/verify/settings/team/VER/notifications')
  const newIssue = screen.getAllByRole('button', { name: 'New issue' }).at(-1)!
  await user.click(newIssue)
  expect(onCreateIssue).toHaveBeenCalledOnce()
})

it('visibly fills a selected team favorite and restores the outline when deselected', () => {
  const page = (selected: boolean) => <I18nProvider><TooltipProvider><IssueExplorerSurface scopeName="Test team" scopeTeam={{ id: 'team-1', key: 'TST', name: 'Test team' } as never} activeView="all" viewHref={() => '#'} filters={[]} filterOptions={() => []} displayOptions={display} detailsOpen={false} teamFavorite={selected} onToggleTeamFavorite={vi.fn()} onFilterToggle={vi.fn()} onDisplayOptionsChange={vi.fn()} onDetailsOpenChange={vi.fn()} onNavigateView={vi.fn()}>Content</IssueExplorerSurface></TooltipProvider></I18nProvider>
  const { rerender } = render(page(false))
  const outline = screen.getByRole('switch').querySelector('path')?.getAttribute('d')
  rerender(page(true))
  expect(screen.getByRole('switch', { name: 'Remove from favorites' })).toHaveAttribute('aria-checked', 'true')
  expect(screen.getByRole('switch').querySelector('path')?.getAttribute('d')).not.toBe(outline)
  rerender(page(false))
  expect(screen.getByRole('switch').querySelector('path')?.getAttribute('d')).toBe(outline)
})

it.each([
  ['HIPAA mode', { hipaaCompliance: true, featureFlags: {} }],
  ['the AI switch off', { featureFlags: { 'ai-agent': false } }],
])('hides the AI filter under %s', async (_, settings) => {
  const user = userEvent.setup()
  setWorkspaceRuntimePreferences(settings)
  try {
    render(<I18nProvider><IssueExplorerSurface creatingView scopeName="Workspace" activeView="all" viewHref={() => '#'} filters={[]} displayOptions={display} detailsOpen={false} onFilterToggle={vi.fn()} onDisplayOptionsChange={vi.fn()} onDetailsOpenChange={vi.fn()} onNavigateView={vi.fn()} filterOptions={field => field === 'priority' ? [{ id: '2', label: 'High' }] : undefined} viewEditor={controls => <div>Editor{controls.filterButton}</div>}>Content</IssueExplorerSurface></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    expect(screen.getByRole('option', { name: 'Priority' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'AI filter' })).toBeNull()
    await user.type(screen.getByRole('combobox', { name: 'Add Filter…' }), 'urgent bugs')
    expect(screen.queryByRole('option', { name: /AI filter/ })).toBeNull()
  } finally {
    setWorkspaceRuntimePreferences({})
  }
})

it('offers the AI filter when the workspace allows AI', async () => {
  const user = userEvent.setup()
  render(<I18nProvider><IssueExplorerSurface creatingView scopeName="Workspace" activeView="all" viewHref={() => '#'} filters={[]} displayOptions={display} detailsOpen={false} onFilterToggle={vi.fn()} onDisplayOptionsChange={vi.fn()} onDetailsOpenChange={vi.fn()} onNavigateView={vi.fn()} filterOptions={() => undefined} viewEditor={controls => <div>Editor{controls.filterButton}</div>}>Content</IssueExplorerSurface></I18nProvider>)
  await user.click(screen.getByRole('button', { name: 'Add filter' }))
  expect(screen.getByRole('option', { name: 'AI filter' })).toBeInTheDocument()
})

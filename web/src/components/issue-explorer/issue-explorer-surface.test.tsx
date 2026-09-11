import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { IssueExplorerSurface } from './issue-explorer-surface'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const display = { layout: 'list', grouping: 'status', subGrouping: 'none', properties: new Set(), hiddenGroupIds: [] } as never
beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))
it('keeps the create-view filter menu clickable outside the fixed editor panel', async () => {
  const user = userEvent.setup()
  render(<I18nProvider><IssueExplorerSurface creatingView scopeName="Workspace" activeView="all" viewHref={() => '#'} filters={[]} displayOptions={display} detailsOpen={false} onFilterToggle={vi.fn()} onDisplayOptionsChange={vi.fn()} onDetailsOpenChange={vi.fn()} onNavigateView={vi.fn()} filterOptions={() => [{ id: 'open', label: 'Open' }] as never} viewEditor={<div aria-label="New issue view">Editor</div>} onNewViewResourceChange={vi.fn()} onOpenSidebar={vi.fn()}>Content</IssueExplorerSurface></I18nProvider>)
  await user.click(screen.getByRole('button', { name: 'Add filter' }))
  expect(document.querySelector('.rootSearch input, input[placeholder="Add Filter…"]')).toBeTruthy()
})

it.each(['mouse', 'keyboard'])('applies a filter from its portaled submenu using %s', async mode => {
  const user = userEvent.setup(), toggle = vi.fn()
  render(<I18nProvider><IssueExplorerSurface creatingView scopeName="Workspace" activeView="all" viewHref={() => '#'} filters={[]} displayOptions={display} detailsOpen={false} onFilterToggle={toggle} onDisplayOptionsChange={vi.fn()} onDetailsOpenChange={vi.fn()} onNavigateView={vi.fn()} filterOptions={field => field === 'priority' ? [{ id: '2', label: 'High' }] : undefined} viewEditor={<div>Editor</div>}>Content</IssueExplorerSurface></I18nProvider>)
  const trigger = screen.getByRole('button', { name: 'Add filter' })
  if (mode === 'mouse') {
    await user.click(trigger)
    await user.hover(screen.getByRole('option', { name: 'Priority' }))
    const option = await screen.findByRole('option', { name: 'High' })
    expect(document.querySelector('.createPanel')?.contains(option)).toBe(false)
    await user.click(option)
  } else {
    trigger.focus()
    await user.keyboard('{Enter}')
    await user.type(await screen.findByRole('textbox', { name: 'Add Filter…' }), 'Priority')
    await user.keyboard('{ArrowDown}{Enter}')
    const search = await screen.findByRole('searchbox', { name: 'Filter Priority' })
    search.focus()
    await user.keyboard('{ArrowDown}{Enter}')
  }
  expect(toggle).toHaveBeenCalledWith('priority', { id: '2', label: 'High' })
})

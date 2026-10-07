import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, teammate, viewer, completed, backlog, started } from '@/test/fixtures'
import { issueToExplorerRow } from './issue-explorer-model'
import { SavedViewInsightsPanel } from './saved-view-panels'
import { INSIGHT_MENU_POSITION, INSIGHTS_FULLSCREEN_SHORTCUT, dimensionOptions, timeInStatusTree } from './insight-options'
import { parseInsightsConfig } from './insight-config'
import type { SavedView } from '@/types/flow'

vi.mock('@/lib/api', () => ({ listIssueRecords: vi.fn() }))
const issues = [
  makeIssue({ id: 'one', identifier: 'TST-1', priority: 1, assignee: viewer, state: backlog }),
  makeIssue({ id: 'two', identifier: 'TST-2', priority: 0, assignee: undefined, state: started }),
  makeIssue({ id: 'three', identifier: 'TST-3', priority: 1, assignee: teammate, state: completed }),
]
const data = makeBootstrap({ issues })
const rows = issues.map(issue => issueToExplorerRow(issue, 'workspace', issues, data))

function setup(insights: Record<string, unknown> = {}) {
  const view = { id: 'menus', name: 'Menus', insights } as unknown as SavedView
  return render(<MemoryRouter><I18nProvider><SavedViewInsightsPanel data={data} allRows={rows} rows={rows} view={view} onSave={vi.fn(async () => undefined)} onClose={vi.fn()}/></I18nProvider></MemoryRouter>)
}
async function openSelect(name: string) {
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name }))
  return { user, menu: await screen.findByRole('menu') }
}

beforeEach(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
  localStorage.clear()
  localStorage.setItem('flow:saved-view:menus:insights-intro', 'dismissed')
})

describe('Insights menus (Linear parity)', () => {
  it('lists the measures with a divider after Issue count, a check on the selected row and a small ▶ marker on Time in status', async () => {
    setup()
    const { menu } = await openSelect('Measure')
    const children = [...menu.querySelectorAll('[role="menuitem"], [role="separator"]')].map(node => node.getAttribute('role') === 'separator' ? '|' : node.textContent)
    expect(children).toEqual(['Issue count', '|', 'Cycle time', 'Lead time', 'Issue age', 'Time in status▶'])
    const selected = within(menu).getByRole('menuitem', { name: /Issue count/ })
    expect(selected).toHaveAttribute('data-selected')
    expect(selected.querySelector('svg path')?.getAttribute('d')).toMatch(/^M4\.2996 7\.23968/)
    const marker = within(menu).getByRole('menuitem', { name: /Time in status/ }).querySelector('[aria-hidden="true"]')
    expect(marker).toHaveTextContent('▶')
    expect(marker?.className).toMatch(/menuChevron/)
  })

  it('opens every select below it, flush with its right edge', async () => {
    expect(INSIGHT_MENU_POSITION).toEqual({ align: 'end', side: 'bottom', sideOffset: 5.5 })
    setup()
    const { menu } = await openSelect('Slice')
    expect(menu).toHaveAttribute('data-align', 'end')
    expect(menu).toHaveAttribute('data-side', 'bottom')
  })

  it('draws no focus ring on the highlighted row (only the inset hover background)', async () => {
    // CSS modules compile to class maps under Vitest, so read the stylesheet's source (tests run from web/).
    const fs = await import('node:' + 'fs') as { readFileSync: (path: string, encoding: 'utf8') => string }
    const css = fs.readFileSync('src/components/issue-explorer/insights-panel.module.css', 'utf8')
    expect(css).toMatch(/\.menu \.menuItem:focus-visible[^{]*\{ outline: 0; box-shadow: none; \}/)
    expect(css).toMatch(/\.menuItem\[data-highlighted\]::before[^{]*\{ background: var\(--pk-hover\); \}/)
    const chevron = css.match(/\.menuChevron \{([^}]*)\}/)?.[1] ?? ''
    expect(chevron).toContain('font-size: 6px')
    expect(chevron).toContain('color: var(--pk-faint)')
  })

  it('offers "No Value" in the Segment menu while the select reads "No value", and hides label groups the workspace lacks', async () => {
    setup()
    expect(screen.getByRole('button', { name: 'Segment' })).toHaveTextContent('No value')
    const { menu } = await openSelect('Segment')
    expect(within(menu).getAllByRole('menuitem')[0]).toHaveTextContent('No Value')
    expect(within(menu).getByRole('menuitem', { name: /Label group/ })).toBeVisible()
    expect(dimensionOptions({ ...data, labelGroups: [] }, true).map(option => option.id)).not.toContain('labelGroup')
  })

  it('builds the Time in status tree and names the Measure after the ticked status', async () => {
    expect(timeInStatusTree(data).map(node => `${'  '.repeat(node.depth)}${node.label}`)).toEqual(['Backlog', '  Backlog', 'Started', '  In progress', 'Completed', '  Done'])
    setup()
    const { menu } = await openSelect('Measure')
    const trigger = within(menu).getByRole('menuitem', { name: /Time in status/ })
    act(() => { trigger.focus(); fireEvent.keyDown(trigger, { key: 'ArrowRight' }) })
    const submenu = (await screen.findAllByRole('menu')).find(node => node !== menu)!
    expect(within(submenu).getByRole('textbox', { name: 'Filter…' })).toBeVisible()
    const rows = within(submenu).getAllByRole('menuitemcheckbox')
    expect(rows.map(row => row.textContent)).toEqual(['Backlog', 'Backlog', 'Started', 'In progress', 'Completed', 'Done'])
    expect(rows[3].getAttribute('data-depth')).toBe('1')
    fireEvent.click(rows[3])
    expect(screen.getByRole('button', { name: 'Measure', hidden: true })).toHaveTextContent('In progress')
    fireEvent.click(within(submenu).getAllByRole('menuitemcheckbox')[5])
    expect(screen.getByRole('button', { name: 'Measure', hidden: true })).toHaveTextContent('2 statuses')
  })

  it('labels the percentiles like Linear and allows switching them all off', async () => {
    setup({ measure: 'cycleTime' })
    expect(screen.getByRole('button', { name: 'Aggregations' })).toHaveTextContent('P50, P75, P95')
    const { user, menu } = await openSelect('Aggregations')
    for (const row of within(menu).getAllByRole('menuitemcheckbox')) await user.click(row)
    expect(screen.getByRole('button', { name: 'Aggregations', hidden: true })).toHaveTextContent('None')
    expect(parseInsightsConfig({ aggregations: [] }).aggregations).toEqual([])
  })

  it('gives the slice and the segment their own "Hide" toggle and uses a log-scale switch for durations', async () => {
    setup({ slice: 'assignee', segment: 'priority' })
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Insights display options' }))
    expect(screen.getByRole('checkbox', { name: 'Hide Unassigned' })).not.toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Hide No Priority' })).not.toBeChecked()
    await user.click(screen.getByRole('checkbox', { name: 'Hide Unassigned' }))
    expect(JSON.parse(localStorage.getItem('flow:saved-view:menus:insights')!)).toMatchObject({ hideEmptySlice: true })
    expect(parseInsightsConfig({ segment: 'none', hideEmptySegment: true })).toMatchObject({ hideEmptySlice: true, hideEmptySegment: false })
  })

  it('keeps "Set default for everyone" available while the view has no shared default', () => {
    setup()
    expect(screen.getByRole('button', { name: 'Save current insight' })).toBeEnabled()
    expect(screen.queryByRole('button', { name: 'Reset' })).toBeNull()
  })

  it('toggles fullscreen with Ctrl ⇧ F', () => {
    expect(INSIGHTS_FULLSCREEN_SHORTCUT).toBe('Ctrl ⇧ F')
    setup()
    act(() => { fireEvent.keyDown(window, { key: 'F', code: 'KeyF', ctrlKey: true, shiftKey: true }) })
    expect(screen.getByRole('heading', { name: 'Insights' })).toBeVisible()
    act(() => { fireEvent.keyDown(window, { key: 'F', code: 'KeyF', ctrlKey: true, shiftKey: true }) })
    expect(screen.queryByRole('heading', { name: 'Insights' })).toBeNull()
  })
})

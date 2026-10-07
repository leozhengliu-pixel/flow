import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, teammate, viewer, completed, backlog } from '@/test/fixtures'
import { issueToExplorerRow } from './issue-explorer-model'
import { SavedViewInsightsPanel } from './saved-view-panels'
import type { SavedView } from '@/types/flow'

vi.mock('@/lib/api', () => ({ listIssueRecords: vi.fn() }))
const confirm = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: confirm }))
const issues = [
  makeIssue({ id: 'one', identifier: 'TST-1', title: 'First urgent', priority: 1, assignee: viewer, state: backlog }),
  makeIssue({ id: 'two', identifier: 'TST-2', title: 'Second unprioritised', priority: 0, assignee: teammate, state: backlog }),
  makeIssue({ id: 'three', identifier: 'TST-3', title: 'Third done', priority: 1, assignee: viewer, state: completed }),
]
const data = makeBootstrap({ issues })
const rows = issues.map(issue => issueToExplorerRow(issue, 'workspace', issues, data))
const view = { id: 'fullscreen-insight', name: 'Test view', insights: { slice: 'status', segment: 'priority' } } as unknown as SavedView

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('flow:saved-view:fullscreen-insight:insights-intro', 'dismissed')
})

function setup(props: Partial<Parameters<typeof SavedViewInsightsPanel>[0]> = {}) {
  const onSave = vi.fn(async () => undefined)
  const result = render(<MemoryRouter><I18nProvider><SavedViewInsightsPanel data={data} allRows={rows} rows={rows} view={view} viewTitle="All issues" onSave={onSave} onClose={vi.fn()} {...props}/></I18nProvider></MemoryRouter>)
  return { ...result, onSave }
}
const openFullscreen = () => fireEvent.click(screen.getByRole('button', { name: 'Expand to fullscreen' }))
const fullscreen = () => screen.getByRole('complementary', { name: 'View insights' })

describe('insights fullscreen (Linear parity)', () => {
  it('opens on the settings panel, not an issue list, with the view name in the breadcrumb', () => {
    setup()
    openFullscreen()
    const panel = fullscreen()
    expect(within(panel).getByRole('heading', { name: 'Insights' })).toBeVisible()
    expect(panel).toHaveTextContent('All issues›Insights')
    const settings = within(panel).getByRole('complementary', { name: 'Insight settings' })
    for (const name of ['Measure', 'Slice', 'Segment']) expect(within(settings).getByRole('button', { name })).toBeVisible()
    expect(within(settings).getByRole('button', { name: 'Segment' })).toHaveTextContent('Priority')
    expect(within(settings).getByRole('checkbox', { name: 'Show archived issues' })).not.toBeChecked()
    expect(within(settings).getByRole('checkbox', { name: 'Hide No Priority' })).not.toBeChecked()
    // Linear hides the footer while you follow the view's shared default.
    expect(within(settings).queryByRole('button', { name: 'Save current insight' })).toBeNull()
    expect(within(panel).queryByRole('list', { name: 'Issues' })).toBeNull()
    expect(panel.querySelector('[aria-live="polite"]')).toHaveTextContent('3 issues')
  })

  it('shows segment columns with their values in the table', () => {
    setup()
    openFullscreen()
    const table = within(fullscreen()).getByRole('table', { name: 'Insights table' })
    const headers = within(table).getAllByRole('columnheader').map(header => header.textContent)
    expect(headers).toEqual(['Status', 'Issue count', 'No priority', 'Urgent'])
    const backlogRow = within(table).getAllByRole('row').find(row => row.textContent?.startsWith('Backlog'))!
    expect(within(backlogRow).getAllByRole('cell').map(cell => cell.textContent)).toEqual(['Backlog', '2', '1', '1'])
  })

  it('replaces the settings with the clicked bar’s issues, and Escape returns to the settings', () => {
    setup()
    openFullscreen()
    const panel = fullscreen()
    fireEvent.click(within(panel).getByRole('button', { name: 'Backlog, No priority: 1' }))
    const selection = within(panel).getByRole('region', { name: 'Selected issues' })
    expect(selection).toHaveTextContent('Backlog·No priority')
    expect(within(selection).getByText('Second unprioritised')).toBeVisible()
    expect(within(selection).queryByText('First urgent')).toBeNull()
    expect(within(panel).queryByRole('complementary', { name: 'Insight settings' })).toBeNull()
    expect(panel.querySelector('[aria-live="polite"]')).toHaveTextContent('1 issue')
    act(() => { fireEvent.keyDown(window, { key: 'Escape' }) })
    expect(within(fullscreen()).getByRole('complementary', { name: 'Insight settings' })).toBeVisible()
    expect(within(fullscreen()).queryByRole('region', { name: 'Selected issues' })).toBeNull()
    act(() => { fireEvent.keyDown(window, { key: 'Escape' }) })
    expect(screen.queryByRole('heading', { name: 'Insights' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Expand to fullscreen' })).toBeVisible()
  })

  it('closes the selection with the panel button and hides the empty segment value on request', () => {
    setup()
    openFullscreen()
    fireEvent.click(within(fullscreen()).getByRole('button', { name: 'Done, Urgent: 1' }))
    fireEvent.click(within(fullscreen()).getByRole('button', { name: 'Close panel' }))
    const settings = within(fullscreen()).getByRole('complementary', { name: 'Insight settings' })
    fireEvent.click(within(settings).getByRole('checkbox', { name: 'Hide No Priority' }))
    const table = within(fullscreen()).getByRole('table', { name: 'Insights table' })
    expect(within(table).getAllByRole('columnheader').map(header => header.textContent)).toEqual(['Status', 'Issue count', 'Urgent'])
    expect(fullscreen().querySelector('[aria-live="polite"]')).toHaveTextContent('2 issues')
  })

  it('keeps personal choices over the shared default until "Set default for everyone"', async () => {
    const { unmount } = setup()
    openFullscreen()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Hide No Priority' }))
    expect(JSON.parse(localStorage.getItem('flow:saved-view:fullscreen-insight:insights')!)).toMatchObject({ segment: 'priority', hideEmptySegment: true })
    unmount()
    const { onSave } = setup()
    openFullscreen()
    expect(screen.getByRole('checkbox', { name: 'Hide No Priority' })).toBeChecked()
    expect(screen.getByRole('button', { name: 'Reset' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Save current insight' })).toHaveTextContent('Set default for everyone')
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Save current insight' })) })
    expect(confirm).toHaveBeenCalledWith('Save insight', expect.objectContaining({ confirmLabel: 'Save', danger: false }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ slice: 'status', segment: 'priority', hideEmptySegment: true }))
    expect(localStorage.getItem('flow:saved-view:fullscreen-insight:insights')).toBeNull()
  })

  it('hides "Set default for everyone" when the viewer cannot change the shared default', () => {
    setup({ canSetDefault: false })
    expect(screen.queryByRole('button', { name: 'Save current insight' })).toBeNull()
  })

  it('translates the fullscreen breadcrumb', () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    setup()
    fireEvent.click(screen.getByRole('button', { name: '展开至全屏' }))
    expect(screen.getByRole('heading', { name: '洞察' })).toBeVisible()
    expect(screen.getByRole('button', { name: '关闭全屏' })).toBeVisible()
  })
})

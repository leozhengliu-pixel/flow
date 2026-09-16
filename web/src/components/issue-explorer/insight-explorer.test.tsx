import { fireEvent, render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, teammate, viewer, completed } from '@/test/fixtures'
import { issueToExplorerRow } from './issue-explorer-model'
import { SavedViewInsightsPanel } from './saved-view-panels'
import type { SavedView } from '@/types/flow'

vi.mock('@nivo/bar', () => ({ ResponsiveBar: () => <svg data-testid="chart"/> }))
vi.mock('@/lib/api', () => ({ listIssueRecords: vi.fn() }))
const issues = [makeIssue({ id: 'one', assignee: viewer }), makeIssue({ id: 'two', assignee: teammate }), makeIssue({ id: 'three', assignee: viewer, state: completed })]
const data = makeBootstrap({ issues })
const rows = issues.map(issue => issueToExplorerRow(issue, 'workspace', issues, data))
const view = { id: 'test-insight', name: 'Test', insights: { segment: 'assignee' } } as unknown as SavedView
beforeEach(() => { localStorage.setItem('flow:saved-view:test-insight:insights-intro', 'dismissed') })
function setup() {
  const drill = vi.fn()
  const result = render(<I18nProvider><SavedViewInsightsPanel data={data} allRows={rows} rows={rows} view={view} onSave={vi.fn()} onClose={vi.fn()} onDrillChange={drill}/></I18nProvider>)
  return { ...result, drill }
}
describe('insight table behavior', () => {
  it('starts unselected, applies a cell intersection, and toggles it off', () => {
    const { drill } = setup()
    expect(drill).toHaveBeenLastCalledWith(undefined)
    const table = screen.getByRole('table', { name: 'Insights table' })
    const row = within(table).getAllByRole('row')[1]
    const cells = within(row).getAllByRole('cell')
    fireEvent.click(within(cells[2]).getByRole('button'))
    expect(drill.mock.lastCall?.[0].map((row: { id: string }) => row.id)).toEqual(['one'])
    fireEvent.click(within(cells[2]).getByRole('button'))
    expect(drill).toHaveBeenLastCalledWith(undefined)
  })
  it('highlights a cell on hover and restores the selected row on leave', () => {
    setup()
    const table = screen.getByRole('table', { name: 'Insights table' })
    const row = within(table).getAllByRole('row')[1]
    const cells = within(row).getAllByRole('cell')
    fireEvent.click(within(cells[0]).getByRole('button'))
    fireEvent.mouseEnter(within(cells[2]).getByRole('button'))
    expect(cells[2]).toHaveAttribute('data-highlight', 'strong')
    expect(cells[3]).toHaveAttribute('data-highlight', 'weak')
    fireEvent.mouseLeave(table)
    expect(cells[3]).toHaveAttribute('data-highlight', 'strong')
  })
  it('sorts table rows and clears a drill with Escape', () => {
    const { drill } = setup()
    const table = screen.getByRole('table', { name: 'Insights table' })
    const header = within(table).getAllByRole('columnheader')[0]
    fireEvent.click(within(header).getByRole('button'))
    expect(header).toHaveAttribute('aria-sort', 'ascending')
    expect(within(table).getAllByRole('row')[1]).toHaveTextContent('Done')
    fireEvent.click(within(within(table).getAllByRole('row')[1]).getAllByRole('button')[0])
    fireEvent.keyDown(table, { key: 'Escape' })
    expect(drill).toHaveBeenLastCalledWith(undefined)
  })
})

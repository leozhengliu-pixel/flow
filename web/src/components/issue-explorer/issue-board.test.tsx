import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { MyIssuesRowData, MyIssuesRowPropertyOptions } from '@/components/my-issues/my-issues-list'
import { IssueBoard } from './issue-board'

const row: MyIssuesRowData = {
  id: 'issue-1', identifier: 'FLOW-1', title: 'Inspect card behavior', teamId: 'team-1', href: '/workspace/issue/FLOW-1/inspect-card-behavior',
  priority: 2, state: { id: 'started', name: 'In progress', type: 'started', color: '#f2c94c' },
  cycleId: 'cycle-1', cycleName: 'Iteration 1', createdAt: '2026-09-01T12:00:00Z', updatedAt: '2026-09-02T12:00:00Z',
}
const options: MyIssuesRowPropertyOptions = {
  status: [
    { id: 'started', label: 'In progress', teamId: 'team-1', kind: 'status', stateType: 'started', color: '#f2c94c' },
    { id: 'backlog', label: 'Backlog', teamId: 'team-1', kind: 'status', stateType: 'backlog', color: '#888888' },
    { id: 'foreign', label: 'Other team status', teamId: 'team-2', kind: 'status', stateType: 'started', color: '#888888' },
  ],
  priority: [], assignee: [], dueDate: [], labels: [], project: [],
  cycle: [{ id: '', label: 'No cycle', kind: 'cycle' }, { id: 'cycle-1', label: 'Iteration 1', kind: 'cycle', teamId: 'team-1' }],
}
function setup(selected = false, properties = new Set<'status' | 'id' | 'cycle' | 'updated'>(['status', 'id'])) {
  const actions = { onOpenIssue: vi.fn(), onSelectIssue: vi.fn(), onMove: vi.fn(), onPropertyChange: vi.fn().mockResolvedValue(undefined) }
  render(<I18nProvider><IssueBoard {...actions} groups={[{ id: 'started', label: 'In progress', issues: [row] }, { id: 'backlog', label: 'Backlog', issues: [] }]} selectedIds={new Set(selected ? [row.id] : [])} properties={properties} propertyOptions={options}/></I18nProvider>)
  return { ...actions, card: screen.getByRole('link', { name: 'FLOW-1 Inspect card behavior' }) }
}

describe('issue board cards', () => {
  beforeEach(() => localStorage.clear())

  it.each([false, true])('keeps the status picker visible when hovered (selected: %s)', async selected => {
    const user = userEvent.setup(); const { card } = setup(selected)
    await user.hover(card)
    expect(screen.getByRole('combobox', { name: 'Change status. Current status is In progress' })).toBeVisible()
    expect(screen.queryByRole('checkbox', { name: 'Select issue' })).not.toBeInTheDocument()
    expect(card).toHaveAttribute('data-selected', String(selected))
  })

  it('changes status using the shared picker without opening or selecting the card', async () => {
    const user = userEvent.setup(); const actions = setup()
    await user.click(screen.getByRole('combobox', { name: 'Change status. Current status is In progress' }))
    expect(screen.queryByRole('option', { name: /Other team status/ })).not.toBeInTheDocument()
    await user.click(screen.getByRole('option', { name: /Backlog/ }))
    await waitFor(() => expect(actions.onPropertyChange).toHaveBeenCalledWith(row, 'status', 'backlog'))
    expect(actions.onOpenIssue).not.toHaveBeenCalled()
    expect(actions.onSelectIssue).not.toHaveBeenCalled()
  })

  it('uses Shift-click for multiselection and ordinary clicks for navigation', () => {
    const actions = setup()
    fireEvent.click(actions.card, { shiftKey: true })
    expect(actions.onSelectIssue).toHaveBeenCalledWith(row.id, true, true)
    expect(actions.onOpenIssue).not.toHaveBeenCalled()
    fireEvent.click(actions.card)
    expect(actions.onOpenIssue).toHaveBeenCalledWith(row)
    expect(actions.card).toHaveAttribute('href', row.href)
  })

  it('supports keyboard selection without taking over picker keyboard events', () => {
    const actions = setup()
    fireEvent.keyDown(actions.card, { key: 'Enter', shiftKey: true })
    expect(actions.onSelectIssue).toHaveBeenCalledWith(row.id, true, true)
    fireEvent.keyDown(screen.getByRole('combobox', { name: 'Change status. Current status is In progress' }), { key: 'Enter' })
    expect(actions.onOpenIssue).not.toHaveBeenCalled()
  })

  it('does not initiate a drag from a property control', () => {
    const { card } = setup()
    fireEvent.pointerDown(screen.getByRole('combobox', { name: 'Change status. Current status is In progress' }))
    const dataTransfer = { setData: vi.fn(), effectAllowed: '' }
    expect(fireEvent.dragStart(card, { dataTransfer })).toBe(false)
    expect(dataTransfer.setData).not.toHaveBeenCalled()
  })

  it('retains cross-column drag-and-drop', () => {
    const actions = setup(); const dataTransfer = { setData: vi.fn(), effectAllowed: '' }
    fireEvent.pointerDown(actions.card)
    fireEvent.dragStart(actions.card, { dataTransfer })
    const target = screen.getByRole('listitem', { name: 'Backlog' }).querySelector('[data-over]')!
    fireEvent.drop(target, { dataTransfer })
    expect(actions.onMove).toHaveBeenCalledWith(row, 'started', 'backlog', 0)
  })

  it('renders and changes the cycle, and honors the updated-date display option', async () => {
    const user = userEvent.setup(); const actions = setup(false, new Set(['status', 'cycle', 'updated']))
    expect(screen.getByText(/Updated/)).toBeVisible()
    await user.click(screen.getByRole('combobox', { name: 'Change cycle. Current cycle is Iteration 1' }))
    await user.click(screen.getByRole('option', { name: 'No cycle' }))
    await waitFor(() => expect(actions.onPropertyChange).toHaveBeenCalledWith(row, 'cycle', ''))
    expect(actions.onOpenIssue).not.toHaveBeenCalled()
  })
})

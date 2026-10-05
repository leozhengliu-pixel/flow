import { useState } from 'react'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { AdvancedFilterGroup } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import { AdvancedFilterChip, AdvancedFilterEditor } from './advanced-filter-editor'
import { createAdvancedFilter, createCondition, treeConditions } from './advanced-filter'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const options = (field: MyIssuesFilterKey): MyIssuesFilterOption[] | undefined => field === 'priority'
  ? [{ id: '1', label: 'Urgent', kind: 'priority', priority: 1 }, { id: '2', label: 'High', kind: 'priority', priority: 2 }]
  : field === 'status' ? [{ id: 'triage', label: 'Triage', kind: 'status', stateType: 'backlog' }] : undefined

function Harness({ initial, onTree }: { initial?: AdvancedFilterGroup; onTree?: (tree: AdvancedFilterGroup) => void }) {
  const [tree, setTree] = useState<AdvancedFilterGroup>(initial ?? { id: 'root', conjunction: 'and', items: [] })
  return <I18nProvider><AdvancedFilterEditor tree={tree} filterOptions={options} onChange={next => { setTree(next); onTree?.(next) }}/></I18nProvider>
}

async function addPriority(user: ReturnType<typeof userEvent.setup>, button: HTMLElement, value: RegExp) {
  await user.click(button)
  fireEvent.mouseMove(screen.getByRole('option', { name: 'Priority' }))
  await user.click(await screen.findByRole('option', { name: value }))
}

beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))

describe('advanced filter editor', () => {
  it('starts with only "+ Filter", adds conditions and toggles and/or for the group', async () => {
    const user = userEvent.setup(), onTree = vi.fn()
    render(<Harness onTree={onTree}/>)
    const add = screen.getAllByRole('button', { name: 'Add filter' })
    expect(add).toHaveLength(1)
    expect(screen.queryByRole('button', { name: /Toggle filter operator/ })).not.toBeInTheDocument()
    await user.click(add[0])
    expect(screen.getByRole('option', { name: /Add filter group/ })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'AI filter' })).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'Advanced filter' })).not.toBeInTheDocument()
    await user.keyboard('{Escape}')
    await addPriority(user, screen.getByRole('button', { name: 'Add filter' }), /^Urgent/)
    expect(onTree.mock.lastCall![0].items).toHaveLength(1)
    // One condition: the "+ Filter" row carries the and/or toggle.
    await user.click(screen.getByRole('button', { name: 'Toggle filter operator, currently and' }))
    expect(onTree.mock.lastCall![0].conjunction).toBe('or')
    expect(screen.getByRole('button', { name: 'Toggle filter operator, currently or' })).toBeInTheDocument()
  })

  it('adds groups up to three levels and deletes a group', async () => {
    const user = userEvent.setup(), onTree = vi.fn()
    render(<Harness onTree={onTree} initial={{ id: 'root', conjunction: 'and', items: [createCondition('status', 'Status', { value: 'triage', valueLabel: 'Triage' })] }}/>)
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    await user.click(screen.getByRole('option', { name: /Add filter group/ }))
    let tree: AdvancedFilterGroup = onTree.mock.lastCall![0]
    expect(tree.items).toHaveLength(2)
    expect((tree.items[1] as AdvancedFilterGroup).conjunction).toBe('or')
    // The group's own "+ Filter" still offers a nested group…
    const groupAdd = screen.getAllByRole('button', { name: 'Add filter' })[0]
    await user.click(groupAdd)
    await user.click(screen.getByRole('option', { name: /Add filter group/ }))
    tree = onTree.mock.lastCall![0]
    const nested = (tree.items[1] as AdvancedFilterGroup).items[0] as AdvancedFilterGroup
    expect(nested.conjunction).toBe('and')
    // …but the nested group's does not (top → group → nested group).
    await user.click(screen.getAllByRole('button', { name: 'Add filter' })[0])
    expect(screen.queryByRole('option', { name: /Add filter group/ })).not.toBeInTheDocument()
    await user.keyboard('{Escape}')
    const deletes = screen.getAllByRole('button', { name: 'Delete group' })
    expect(deletes).toHaveLength(2)
    await user.click(deletes[0])
    expect(onTree.mock.lastCall![0].items).toHaveLength(1)
    expect(screen.queryByRole('button', { name: 'Delete group' })).not.toBeInTheDocument()
  })

  it('summarises the tree on the chip and prunes empty groups when the editor closes', async () => {
    const user = userEvent.setup(), onChange = vi.fn(), onRemove = vi.fn()
    const filter = createAdvancedFilter({ id: 'root', conjunction: 'or', items: [
      createCondition('status', 'Status', { value: 'triage', valueLabel: 'Triage' }),
      createCondition('priority', 'Priority', { value: '1', valueLabel: 'Urgent' }),
      createCondition('priority', 'Priority', { value: '2', valueLabel: 'High' }),
      { id: 'empty', conjunction: 'and', items: [] },
    ] })
    render(<I18nProvider><AdvancedFilterChip filter={filter} filterOptions={options} onChange={onChange} onRemove={onRemove}/></I18nProvider>)
    const chip = screen.getByRole('button', { name: 'Open advanced filter' })
    expect(chip).toHaveTextContent('Status is Triage or Priority is Urgent and 1 more condition')
    await user.click(chip)
    expect(screen.getByRole('dialog', { name: 'Advanced filter' })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(treeConditions(onChange.mock.lastCall![0])).toHaveLength(3)
    expect(onChange.mock.lastCall![0].items.some((item: { id: string }) => item.id === 'empty')).toBe(false)
    await user.click(screen.getByRole('button', { name: 'Remove advanced filter' }))
    expect(onRemove).toHaveBeenCalled()
  })

  it('reads "Advanced filter" while empty', () => {
    render(<I18nProvider><AdvancedFilterChip filter={createAdvancedFilter()} filterOptions={options} onChange={vi.fn()} onRemove={vi.fn()}/></I18nProvider>)
    expect(within(screen.getByRole('button', { name: 'Open advanced filter' })).getByText('Advanced filter')).toBeInTheDocument()
  })
})

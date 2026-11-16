import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { AppliedFilterBar } from './applied-filter-bar'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

describe('selected filter values', () => {
  it.each(['A', '标签', 'A long selected value '.repeat(10).trim(), '9,007,199,254,740,991'])('keeps the complete value available when the chip is truncated: %s', async label => {
    const filter = { id: 'filter', fieldLabel: 'Customer count', operator: 'is' as const, values: [{ id: 'value', label }] }
    render(<AppliedFilterBar ariaLabel="Applied filters" filters={[filter]} onAdd={vi.fn()} onClear={vi.fn()} onRemove={vi.fn()} onOperatorChange={vi.fn()} onValuesChange={vi.fn()} optionsFor={() => []}/>)
    const trigger = screen.getByRole('button', { name: 'Customer count values' })
    expect(trigger).toHaveAccessibleDescription(label)
    expect(trigger).toHaveAttribute('title', label)
    await userEvent.click(trigger)
    expect(screen.getByRole('option', { name: label })).toHaveAttribute('aria-checked', 'true')
  })
})

describe('label filter values', () => {
  it('reads "Group › Label" for a label of a group, with the group muted and the label after the marker', async () => {
    const filter = { id: 'filter', fieldLabel: 'Labels', operator: 'is' as const, values: [{ id: 'a', label: 'Flow parity A', color: '#f2c94c' }] }
    const options = [{ id: 'a', label: 'Flow parity A', color: '#f2c94c', groupLabel: 'Flow parity group', count: 1 }, { id: 'bug', label: 'Bug', color: '#eb5757', count: 0 }]
    render(<AppliedFilterBar ariaLabel="Applied filters" filters={[filter]} onAdd={vi.fn()} onClear={vi.fn()} onRemove={vi.fn()} onOperatorChange={vi.fn()} onValuesChange={vi.fn()} optionsFor={() => options}/>)
    // The chip itself names the label only.
    expect(screen.getByRole('button', { name: 'Labels values' })).toHaveTextContent(/^Flow parity A$/)
    await userEvent.click(screen.getByRole('button', { name: 'Labels values' }))
    const grouped = screen.getAllByRole('option')[0]
    expect(grouped.closest('[data-label-values]')).not.toBeNull()
    const path = grouped.querySelector('span[title="Flow parity group › Flow parity A"]')!
    expect([...path.children].map(child => child.tagName.toLowerCase() + (child.textContent ? `:${child.textContent}` : ''))).toEqual(['span:Flow parity group', 'svg', 'span:Flow parity A'])
    expect(screen.getAllByRole('option')[1].querySelector('span[title="Bug"]')).toHaveTextContent('Bug')
  })
})

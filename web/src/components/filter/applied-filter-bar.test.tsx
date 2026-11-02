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

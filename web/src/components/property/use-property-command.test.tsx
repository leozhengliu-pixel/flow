import { useState } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { usePropertyCommand } from './use-property-command'

const options = [
  { id: 'one', label: 'First option', shortcut: '1' },
  { id: 'disabled', label: 'Disabled option', disabled: true },
  { id: 'two', label: 'Second option', keywords: 'beta', shortcut: '2' },
]

function CommandHarness({ onSelect, onOpenChange }: { onSelect: (id: string) => void; onOpenChange: (open: boolean) => void }) {
  const [open, setOpen] = useState(true)
  const command = usePropertyCommand({ autoFocus: false, open, options, selectedIds: ['one'], onOpenChange: next => { setOpen(next); onOpenChange(next) }, onSelect: option => onSelect(option.id) })
  return <div>
    <input aria-label="Search options" value={command.query} onChange={event => command.onQueryChange(event.target.value)} onKeyDown={command.onKeyDown}/>
    <output>{command.activeId}</output>
    {command.filteredOptions.map(option => <button disabled={option.disabled} key={option.id} onClick={() => command.choose(option)}>{option.label}</button>)}
    <span>{String(open)}</span>
  </div>
}

describe('property command keyboard behavior', () => {
  it('filters, skips disabled options, selects, and closes with keyboard controls', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    const onOpenChange = vi.fn()
    render(<CommandHarness onOpenChange={onOpenChange} onSelect={onSelect}/>)
    await waitFor(() => expect(screen.getByText('one')).toBeVisible())
    const search = screen.getByRole('textbox', { name: 'Search options' })
    await user.type(search, 'beta')
    expect(screen.queryByText('First option')).not.toBeInTheDocument()
    expect(screen.getByText('Second option')).toBeVisible()
    await user.keyboard('{Enter}')
    expect(onSelect).toHaveBeenCalledWith('two')
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('wraps arrow navigation and supports numeric shortcuts and escape', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    const onOpenChange = vi.fn()
    render(<CommandHarness onOpenChange={onOpenChange} onSelect={onSelect}/>)
    const search = screen.getByRole('textbox', { name: 'Search options' })
    search.focus()
    await user.keyboard('{ArrowUp}')
    expect(screen.getByText('two')).toBeVisible()
    await user.keyboard('1')
    expect(onSelect).toHaveBeenCalledWith('one')
    await user.keyboard('{Escape}')
    expect(onOpenChange).toHaveBeenLastCalledWith(false)
  })
})

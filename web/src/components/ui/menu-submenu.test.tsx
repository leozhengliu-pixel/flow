import * as ContextMenu from '@radix-ui/react-context-menu'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { MenuSubmenu } from './menu-submenu'

const layout = vi.hoisted(() => ({ phone: true }))
vi.mock('@/hooks/use-phone-layout', () => ({ usePhoneLayout: () => layout.phone }))

function Actions({ onSelect }: { onSelect: () => void }) {
  return <ContextMenu.Root>
    <ContextMenu.Trigger>Row</ContextMenu.Trigger>
    <ContextMenu.Portal><ContextMenu.Content aria-label="Actions">
      <MenuSubmenu label="Move" trigger="Move">
        <ContextMenu.Item onSelect={onSelect}>Destination</ContextMenu.Item>
      </MenuSubmenu>
    </ContextMenu.Content></ContextMenu.Portal>
  </ContextMenu.Root>
}

describe('MenuSubmenu', () => {
  it('replaces its parent level, returns, and closes on selection', async () => {
    layout.phone = true
    const onSelect = vi.fn()
    render(<Actions onSelect={onSelect}/>)
    fireEvent.contextMenu(screen.getByText('Row'))
    const root = screen.getByRole('menu', { name: 'Actions' })
    fireEvent.click(within(root).getByRole('menuitem', { name: 'Move' }))
    const move = await screen.findByRole('menu', { name: 'Move' })
    expect(root).toHaveAttribute('data-phone-submenu-open')
    expect(move).toHaveAttribute('data-phone-submenu')
    fireEvent.click(within(move).getByRole('button', { name: 'Back' }))
    expect(root).not.toHaveAttribute('data-phone-submenu-open')
    fireEvent.click(within(root).getByRole('menuitem', { name: 'Move' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Destination' }))
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('leaves the desktop cascade without phone attributes or a Back row', async () => {
    layout.phone = false
    const user = userEvent.setup()
    render(<Actions onSelect={vi.fn()}/>)
    fireEvent.contextMenu(screen.getByText('Row'))
    await user.click(screen.getByRole('menuitem', { name: 'Move' }))
    await screen.findByRole('menu', { name: 'Move' })
    expect(document.querySelector('[data-phone-submenu], [data-phone-submenu-open]')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Back' })).not.toBeInTheDocument()
  })
})

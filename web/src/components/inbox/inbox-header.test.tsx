import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { I18nProvider } from '@/i18n/i18n'
import { InboxHeader } from './inbox-page-shell'

const display = { ordering: 'newest', showSnoozed: false, showRead: true, showUnreadFirst: false, priorityInbox: false, unreadGrouping: 'none' } as const

function renderHeader() {
  return render(<I18nProvider><TooltipProvider><InboxHeader displayOptions={display} onDisplayOptionsChange={vi.fn()} onDeleteAll={vi.fn()} onDeleteAllRead={vi.fn()} onDeleteAllReadCompleted={vi.fn()} onMarkAllRead={vi.fn()} onOpenSettings={vi.fn()}/></TooltipProvider></I18nProvider>)
}

describe('inbox header (Linear parity)', () => {
  it('offers exactly Linear’s notification actions with the ⌥ U keys rendered separately', async () => {
    renderHeader()
    await userEvent.setup().click(screen.getByRole('button', { name: 'Notification actions' }))
    const items = screen.getAllByRole('menuitem').map(item => item.textContent)
    expect(items).toEqual(['Mark all as read⌥U', 'Delete all', 'Go to settings'])
    const keys = screen.getByLabelText('⌥U').querySelectorAll('span')
    expect([...keys].map(key => key.textContent)).toEqual(['⌥', 'U'])
  })

  it('opens display options with ⇧V and separates the priority inbox row', async () => {
    renderHeader()
    fireEvent.keyDown(window, { key: 'V', shiftKey: true })
    expect(await screen.findByText('Group unreads by')).toBeInTheDocument()
    const menu = screen.getByRole('menu')
    expect(menu.querySelector('.flow-inbox-menu__priority-row + .flow-inbox-menu__separator')).not.toBeNull()
  })
})

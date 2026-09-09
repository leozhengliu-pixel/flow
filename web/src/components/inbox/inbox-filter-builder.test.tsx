import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'

import { InboxFilterBuilder } from './inbox-filter-builder'

describe('InboxFilterBuilder', () => {
  it('shows the shortcut on the initial toolbar menu and opens a separate command dialog with F', async () => {
    const user = userEvent.setup()
    const { unmount } = render(<I18nProvider><InboxFilterBuilder trigger={<button type="button">Filter</button>} filters={[]} onFiltersChange={vi.fn()} /></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    const pointerMenu = screen.getByRole('dialog', { name: 'Add filter' })
    expect(pointerMenu).toHaveAttribute('data-additional', 'false')
    expect(within(pointerMenu).getByText('F')).toBeInTheDocument()
    expect(within(pointerMenu).getByRole('option', { name: 'Notification type' })).toHaveAttribute('aria-selected', 'false')

    unmount()
    render(<I18nProvider><InboxFilterBuilder filters={[]} onFiltersChange={vi.fn()} /></I18nProvider>)
    await user.keyboard('f')
    const keyboardMenu = screen.getByRole('dialog', { name: 'Filter notifications' })
    expect(within(keyboardMenu).getByPlaceholderText('Filter notifications by…')).toHaveFocus()
    expect(within(keyboardMenu).queryByText('F')).not.toBeInTheDocument()
    expect(within(keyboardMenu).getByRole('option', { name: 'Notification type' })).toHaveAttribute('aria-selected', 'true')
  })

  it('omits the shortcut when adding another condition', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><InboxFilterBuilder filters={[{id:'priority',property:'issuePriority',operator:'is',values:[{value:'1',valueLabel:'Urgent'}]}]} onFiltersChange={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button', {name:'Add another filter'}))
    const menu = screen.getByRole('dialog', {name:'Add filter'})
    expect(menu).toHaveAttribute('data-additional', 'true')
    expect(within(menu).queryByText('F')).not.toBeInTheDocument()
  })

  it('navigates command stages and applies a matching value with the keyboard', async () => {
    const user = userEvent.setup()
    const onFiltersChange = vi.fn()
    render(<I18nProvider><InboxFilterBuilder filters={[]} onFiltersChange={onFiltersChange}/></I18nProvider>)
    await user.keyboard('f')
    await user.type(screen.getByLabelText('Command menu'), 'Issue priority')
    await user.keyboard('{Enter}')
    expect(screen.queryByRole('option', {name:'Notification type'})).not.toBeInTheDocument()
    await waitFor(() => expect(screen.getByLabelText('Command menu')).toHaveFocus())
    await user.keyboard('{ArrowLeft}')
    expect(screen.getByRole('option', {name:'Notification type'})).toBeInTheDocument()
    await user.click(screen.getByRole('option', {name:'Issue priority'}))
    await user.type(screen.getByLabelText('Command menu'), 'Urgent')
    await user.keyboard('{Enter}')
    expect(onFiltersChange).toHaveBeenCalledWith([expect.objectContaining({property:'issuePriority',values:[expect.objectContaining({value:'1'})]})])
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('does not select hidden unmatched options and expands them without losing search', async () => {
    const user = userEvent.setup()
    const onFiltersChange = vi.fn()
    const options = Array.from({length:12},(_,index)=>({id:`type-${index}`,label:`Category ${index}`,count:index===5?3:0}))
    render(<I18nProvider><InboxFilterBuilder filters={[]} options={{notificationType:options}} onFiltersChange={onFiltersChange}/></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'Add filter'}))
    await waitFor(() => expect(screen.getByRole('searchbox', {name:'Add Filter'})).toHaveFocus())
    await user.keyboard('{ArrowRight}')
    const menu = screen.getByRole('dialog',{name:'Filter Notification type'})
    expect(within(menu).getAllByRole('option')).toHaveLength(2)
    await waitFor(() => expect(within(menu).getByRole('searchbox')).toHaveFocus())
    await user.keyboard('{Enter}')
    expect(onFiltersChange).toHaveBeenCalledWith([expect.objectContaining({values:[expect.objectContaining({value:'type-5'})]})])
    await user.click(screen.getByRole('button',{name:'Add filter'}))
    await user.click(screen.getByRole('option',{name:'Notification type'}))
    await user.click(screen.getByRole('option',{name:'11 options not matching any notifications'}))
    const expanded = screen.getByRole('dialog',{name:'Filter Notification type'})
    expect(within(expanded).getAllByRole('option')).toHaveLength(12)
    expect(within(expanded).getByRole('separator')).toBeInTheDocument()
    await user.type(within(expanded).getByRole('searchbox'), 'Category 11')
    expect(within(expanded).getAllByRole('option')).toHaveLength(1)
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('opens submenus on hover without preselecting the first property', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><InboxFilterBuilder open filters={[]} onFiltersChange={vi.fn()} /></I18nProvider>)
    const notificationType = screen.getByRole('option', { name: 'Notification type' })
    const priority = screen.getByRole('option', { name: 'Issue priority' })

    expect(notificationType).toHaveAttribute('aria-selected', 'false')
    await waitFor(() => expect(screen.getByRole('searchbox', {name:'Add Filter'})).toHaveFocus())
    await user.hover(priority)

    await waitFor(() => expect(screen.getByRole('dialog', { name: 'Filter Issue priority' })).toBeVisible())
    expect(priority).toHaveAttribute('aria-selected', 'true')
  })

  it('applies a value selected from a hovered submenu', async () => {
    const user = userEvent.setup()
    const onFiltersChange = vi.fn()
    render(<I18nProvider><InboxFilterBuilder open filters={[]} onFiltersChange={onFiltersChange} /></I18nProvider>)
    await waitFor(() => expect(screen.getByRole('searchbox', {name:'Add Filter'})).toHaveFocus())
    await user.hover(screen.getByRole('option', { name: 'Issue priority' }))
    const submenu = await screen.findByRole('dialog', { name: 'Filter Issue priority' })
    await user.click(within(submenu).getByRole('option', { name: 'Urgent' }))

    expect(onFiltersChange).toHaveBeenCalledWith([
      expect.objectContaining({
        property: 'issuePriority',
        operator: 'is',
        values: [expect.objectContaining({ value: '1', valueLabel: 'Urgent' })],
      }),
    ])
  })

  it('groups the selected value first and opens a compact operator menu', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><div className="flow-inbox__list-pane"><InboxFilterBuilder
      filters={[{ id: 'status-filter', property: 'issueStatusType', operator: 'is', values: [{ value: 'backlog', valueLabel: 'Backlog' }] }]}
      options={{ issueStatusType: [{ id: 'triage', label: 'Triage' }, { id: 'backlog', label: 'Backlog' }] }}
      onFiltersChange={vi.fn()}
    /></div></I18nProvider>)

    await user.click(await screen.findByRole('button', { name: 'Issue status type' }))
    const valueMenu = screen.getByRole('dialog', { name: 'Filter Issue status type' })
    expect(within(valueMenu).getAllByRole('option').map(option => option.textContent)).toEqual(['Backlog', 'Triage'])
    expect(within(valueMenu).getByRole('separator')).toBeInTheDocument()

    await user.keyboard('{Escape}')
    await user.click(screen.getByRole('button', { name: 'Issue status type operator' }))
    const operatorMenu = screen.getByRole('dialog', { name: 'Issue status type operator' })
    expect(within(operatorMenu).getAllByRole('option')).toHaveLength(2)
    expect(within(operatorMenu).getAllByRole('option').every(option => option.getAttribute('aria-selected') === 'false')).toBe(true)

    await user.keyboard('{Escape}')
    await user.click(within(screen.getByRole('region', {name:'Applied filters'})).getByRole('button',{name:'Add another filter'}))
    const addMenu = screen.getByRole('dialog',{name:'Add filter'})
    expect(within(addMenu).getByPlaceholderText('Filter notifications by…')).toBeInTheDocument()
    expect(within(addMenu).getByText('F')).toBeInTheDocument()
  })
})

import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'

import { InboxFilterBuilder } from './inbox-filter-builder'

describe('InboxFilterBuilder', () => {
  it('keeps pointer and keyboard opening states distinct', async () => {
    const user = userEvent.setup()
    const { unmount } = render(<I18nProvider><InboxFilterBuilder trigger={<button type="button">Filter</button>} filters={[]} onFiltersChange={vi.fn()} /></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    const pointerMenu = screen.getByRole('dialog', { name: 'Add filter' })
    expect(pointerMenu).toHaveAttribute('data-keyboard-mode', 'false')
    expect(within(pointerMenu).queryByText('F')).not.toBeInTheDocument()
    expect(within(pointerMenu).getByRole('option', { name: 'Notification type' })).toHaveAttribute('aria-selected', 'false')

    unmount()
    render(<I18nProvider><InboxFilterBuilder filters={[]} onFiltersChange={vi.fn()} /></I18nProvider>)
    await user.keyboard('f')
    const keyboardMenu = screen.getByRole('dialog', { name: 'Add filter' })
    expect(keyboardMenu).toHaveAttribute('data-keyboard-mode', 'true')
    expect(within(keyboardMenu).getByText('F')).toBeInTheDocument()
    expect(within(keyboardMenu).getByRole('option', { name: 'Notification type' })).toHaveAttribute('aria-selected', 'true')
  })

  it('opens submenus on hover without preselecting the first property', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><InboxFilterBuilder open filters={[]} onFiltersChange={vi.fn()} /></I18nProvider>)
    const notificationType = screen.getByRole('option', { name: 'Notification type' })
    const priority = screen.getByRole('option', { name: 'Issue priority' })

    expect(notificationType).toHaveAttribute('aria-selected', 'false')
    await user.hover(priority)

    await waitFor(() => expect(screen.getByRole('dialog', { name: 'Filter Issue priority' })).toBeVisible())
    expect(priority).toHaveAttribute('aria-selected', 'true')
  })

  it('applies a value selected from a hovered submenu', async () => {
    const user = userEvent.setup()
    const onFiltersChange = vi.fn()
    render(<I18nProvider><InboxFilterBuilder open filters={[]} onFiltersChange={onFiltersChange} /></I18nProvider>)
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
  })
})

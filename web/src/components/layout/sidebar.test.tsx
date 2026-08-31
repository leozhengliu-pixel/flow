import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { SidebarOrder, SidebarPreferences } from './sidebar-customization-state'
import { SidebarCustomization } from './sidebar'

const order: SidebarOrder = {
  personal: ['inbox', 'reviews', 'myIssues', 'pulse', 'drafts', 'agent'],
  workspace: ['members', 'initiatives', 'projects', 'teams', 'views', 'releases', 'loops', 'customers'],
}
const preferences = Object.fromEntries([...order.personal, ...order.workspace, 'documents'].map(id => [id, id === 'customers' ? 'never' : 'always'])) as SidebarPreferences

it('matches sidebar customization controls and visibility rules', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<I18nProvider><SidebarCustomization open onOpenChange={vi.fn()} preferences={preferences} order={order} badgeStyle="count" onBadgeStyleChange={vi.fn()} onChange={onChange} onReorder={vi.fn()}/></I18nProvider>)

  expect(screen.getByRole('dialog')).toHaveClass('sidebar-customize-dialog')
  expect(screen.getByRole('combobox', { name: 'Default badge style' })).toHaveTextContent('1Count')
  expect(screen.getByRole('button', { name: 'Inbox' }).querySelector('svg')).toHaveAttribute('viewBox', '0 0 6 10')

  const inboxSelect = within(screen.getByRole('button', { name: 'Inbox' })).getByRole('combobox')
  await user.click(inboxSelect)
  expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['Always show', 'Show when badged'])
  await user.keyboard('{Escape}')

  const reviewsSelect = within(screen.getByRole('button', { name: 'Reviews' })).getByRole('combobox')
  await user.click(reviewsSelect)
  expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['Always show', 'Show when badged', "Don't show"])
  await user.click(screen.getByRole('option', { name: "Don't show" }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ reviews: 'never' }))
})

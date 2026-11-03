import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'
import { MenuFilter } from './menu-filter'

it('filters menu actions and sends arrow focus to visible enabled items', async () => {
  const user = userEvent.setup()
  render(<div role="menu"><MenuFilter/><div role="menuitem" tabIndex={-1}>Status</div><div role="menuitem" tabIndex={-1}>Priority</div><div role="menuitem" data-disabled tabIndex={-1}>Project</div></div>)
  const filter = screen.getByRole('textbox', { name: 'Filter…' })
  await user.type(filter, 'pri')
  expect(screen.getByRole('menuitem', { name: 'Status' })).toHaveAttribute('data-menu-filter-hidden')
  expect(screen.getByRole('menuitem', { name: 'Priority' })).not.toHaveAttribute('data-menu-filter-hidden')
  await user.keyboard('{ArrowDown}')
  expect(screen.getByRole('menuitem', { name: 'Priority' })).toHaveFocus()
  await user.click(filter)
  await user.clear(filter)
  await user.keyboard('{ArrowUp}')
  expect(screen.getByRole('menuitem', { name: 'Priority' })).toHaveFocus()
})

it('removes empty group headings and separators, then restores them when cleared', async () => {
  const user = userEvent.setup()
  render(<div role="menu"><MenuFilter/><div data-menu-heading>First group</div><div role="menuitem">Status</div><div role="separator"/><div data-menu-heading>Second group</div><div role="menuitem">Priority</div></div>)
  const filter = screen.getByRole('textbox')
  await user.type(filter, 'Priority')
  expect(screen.getByText('First group')).toHaveAttribute('data-menu-filter-hidden')
  expect(screen.getByText('Second group')).not.toHaveAttribute('data-menu-filter-hidden')
  expect(screen.getByRole('separator')).toHaveAttribute('data-menu-filter-hidden')
  await user.clear(filter)
  expect(screen.getByRole('separator')).not.toHaveAttribute('data-menu-filter-hidden')
  await user.type(filter, 'zzzz')
  expect(screen.getByRole('status')).toHaveTextContent('No results')
  await user.clear(filter)
  expect(screen.queryByRole('status')).toBeNull()
})

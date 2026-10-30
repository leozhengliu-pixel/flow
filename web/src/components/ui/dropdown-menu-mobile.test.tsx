import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuItem, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger } from './dropdown-menu'

beforeEach(() => {
  localStorage.setItem('flow:locale', 'en-US')
  vi.stubGlobal('matchMedia', (media: string) => ({ media, matches: media === '(max-width: 767px)', addEventListener() {}, removeEventListener() {} }))
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
})
afterEach(() => vi.unstubAllGlobals())

it('filters phone menus and runs selection before closing', async () => {
  const user = userEvent.setup()
  const choose = vi.fn()
  render(<I18nProvider><DropdownMenu><DropdownMenuTrigger>Actions</DropdownMenuTrigger><DropdownMenuContent>
    <DropdownMenuItem onSelect={choose}>Archive</DropdownMenuItem>
    <DropdownMenuItem>Delete</DropdownMenuItem>
  </DropdownMenuContent></DropdownMenu></I18nProvider>)
  await user.click(screen.getByRole('button', { name: 'Actions' }))
  await user.type(screen.getByRole('combobox'), 'Archive')
  expect(screen.getAllByRole('option')).toHaveLength(1)
  await user.click(screen.getByRole('option', { name: 'Archive' }))
  expect(choose).toHaveBeenCalledOnce()
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(screen.getByRole('button', { name: 'Actions' })).toHaveFocus()
})

it('opens nested phone menus and honors keep-open checkbox selections', async () => {
  const user = userEvent.setup()
  const changed = vi.fn()
  render(<I18nProvider><DropdownMenu><DropdownMenuTrigger>Actions</DropdownMenuTrigger><DropdownMenuContent>
    <DropdownMenuSub><DropdownMenuSubTrigger>Visibility</DropdownMenuSubTrigger><DropdownMenuSubContent>
      <DropdownMenuCheckboxItem checked={false} onCheckedChange={changed} onSelect={event => event.preventDefault()}>Show archived</DropdownMenuCheckboxItem>
    </DropdownMenuSubContent></DropdownMenuSub>
  </DropdownMenuContent></DropdownMenu></I18nProvider>)
  await user.click(screen.getByRole('button', { name: 'Actions' }))
  await user.click(screen.getByRole('option', { name: 'Visibility' }))
  await user.click(screen.getByRole('option', { name: 'Show archived' }))
  expect(changed).toHaveBeenCalledWith(true)
  expect(screen.getByRole('option', { name: 'Show archived' })).toBeVisible()
  await user.keyboard('{Escape}')
  expect(screen.getByRole('option', { name: 'Visibility' })).toBeVisible()
})

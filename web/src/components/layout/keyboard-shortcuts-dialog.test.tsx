import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { KeyboardShortcutsDialog } from './keyboard-shortcuts-dialog'

it('shows supported shortcuts in a searchable Help panel', async () => {
  localStorage.setItem('flow:locale', 'en-US')
  const user = userEvent.setup()
  render(<I18nProvider><KeyboardShortcutsDialog open onOpenChange={vi.fn()}/></I18nProvider>)

  expect(screen.getByRole('heading', { name: 'Keyboard shortcuts' })).toBeVisible()
  expect(screen.getByText('Go to settings')).toBeVisible()
  expect(screen.getByText('New project')).toBeVisible()
  expect(screen.queryByText('Customize in Settings')).toBeNull()

  await user.type(screen.getByRole('textbox', { name: 'Search shortcuts' }), 'project')
  expect(screen.getByText('New project')).toBeVisible()
  expect(screen.queryByText('Go to settings')).toBeNull()
  await user.clear(screen.getByRole('textbox', { name: 'Search shortcuts' }))
  await user.type(screen.getByRole('textbox', { name: 'Search shortcuts' }), 'no such shortcut')
  expect(screen.getByText('No shortcuts found')).toBeVisible()
})

it('closes with Escape', async () => {
  localStorage.setItem('flow:locale', 'en-US')
  const user = userEvent.setup()
  const onOpenChange = vi.fn()
  render(<I18nProvider><KeyboardShortcutsDialog open onOpenChange={onOpenChange}/></I18nProvider>)
  await user.keyboard('{Escape}')
  expect(onOpenChange).toHaveBeenCalledWith(false)
})

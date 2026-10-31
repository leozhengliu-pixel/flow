import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { PhoneHelpMenu } from './phone-help-menu'

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
})

afterEach(() => vi.unstubAllGlobals())

it('filters support entries and opens shortcuts through the existing callback', async () => {
  const user = userEvent.setup()
  const onOpenChange = vi.fn()
  const onShortcuts = vi.fn()
  render(<I18nProvider><PhoneHelpMenu open onOpenChange={onOpenChange} onShortcuts={onShortcuts} onSettings={vi.fn()}/></I18nProvider>)
  expect(screen.getAllByRole('option')).toHaveLength(9)
  await user.type(screen.getByRole('combobox'), 'Keyboard')
  expect(screen.getAllByRole('option')).toHaveLength(1)
  await user.click(screen.getByRole('option', { name: 'Keyboard shortcuts' }))
  expect(onOpenChange).toHaveBeenCalledWith(false)
  expect(onShortcuts).toHaveBeenCalledOnce()
})

it('translates the new support row and filter into Chinese', () => {
  localStorage.setItem('flow:locale', 'zh-CN')
  render(<I18nProvider><PhoneHelpMenu open onOpenChange={vi.fn()} onShortcuts={vi.fn()} onSettings={vi.fn()}/></I18nProvider>)
  expect(screen.getByRole('combobox', { name: '需要什么帮助...' })).toBeVisible()
  expect(screen.getByRole('option', { name: '发送诊断信息' })).toBeVisible()
})

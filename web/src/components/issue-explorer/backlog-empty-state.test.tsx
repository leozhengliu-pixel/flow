import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { LOCALE_STORAGE_KEY, prepareInitialLocale } from '@/i18n/locale'
import { BacklogEmptyState } from './backlog-empty-state'

describe('backlog empty state', () => {
  afterEach(() => localStorage.removeItem(LOCALE_STORAGE_KEY))
  it('creates an issue and links to backlog documentation', () => {
    const onCreateIssue = vi.fn()
    render(<I18nProvider><BacklogEmptyState onCreateIssue={onCreateIssue}/></I18nProvider>)
    expect(screen.getByText('Backlog issues')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Create new issue' }))
    expect(onCreateIssue).toHaveBeenCalledOnce()
    expect(screen.getByRole('link', { name: 'Documentation' })).toHaveAttribute('href', 'https://github.com/leozhengliu-pixel/flow/blob/main/docs/backlog.md')
  })

  it('translates the educational copy', async () => {
    localStorage.setItem(LOCALE_STORAGE_KEY, 'zh-CN')
    await prepareInitialLocale()
    render(<I18nProvider><BacklogEmptyState/></I18nProvider>)
    expect(screen.getByText('待办事项')).toBeVisible()
    expect(screen.getByText('待办列表用于存放尚未确定优先级的新事项和想法。')).toBeVisible()
    expect(screen.getByRole('button', { name: '创建新事项' })).toBeDisabled()
  })
})

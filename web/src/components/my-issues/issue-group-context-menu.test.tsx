import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { IssueGroupContextMenu } from './issue-group-context-menu'

describe('issue group context menu', () => {
  it.each(['en-US', 'zh-CN'])('runs every offered group action in %s', async locale => {
    localStorage.setItem('flow:locale', locale)
    const callbacks = { onCollapse: vi.fn(), onCollapseAll: vi.fn(), onSelectAll: vi.fn(), onHideGroup: vi.fn() }
    const { container } = render(<I18nProvider><IssueGroupContextMenu collapsed={false} {...callbacks}><header>Todo</header></IssueGroupContextMenu></I18nProvider>)
    const labels = locale === 'zh-CN' ? ['收起', '全部折叠', '选择组内所有事项', '隐藏分组'] : ['Collapse', 'Collapse all', 'Select all in group', 'Hide group']
    for (const [index, callback] of Object.values(callbacks).entries()) {
      fireEvent.contextMenu(container.querySelector('header')!)
      fireEvent.click(await screen.findByRole('menuitem', { name: labels[index] }))
      expect(callback).toHaveBeenCalledOnce()
    }
    localStorage.setItem('flow:locale', 'en-US')
  })
})

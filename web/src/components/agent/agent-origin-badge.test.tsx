import { render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { LOCALE_STORAGE_KEY } from '@/i18n/locale'
import { translateToChinese } from '@/i18n/translate'
import type { AgentSession } from '@/types/flow'
import { AgentHistoryList } from './agent-history-list'

function session(id: string, title: string, extra: Partial<AgentSession> = {}): AgentSession {
  const updatedAt = new Date(2026, 9, 13, 9, 0, 0).toISOString()
  return { id, slugId: `${id}-slug`, userId: 'user-1', title, favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [], createdAt: updatedAt, updatedAt, lastReadAt: updatedAt, ...extra }
}

const sessions = [
  session('lark', 'Weekly status', { origin: 'lark' }),
  session('feishu', '本周进展', { origin: 'feishu' }),
  session('app', 'Started in Flow'),
]

function renderList() {
  render(<I18nProvider><AgentHistoryList sessions={sessions} hrefFor={(item) => `/acme/agent/${item.slugId}`} onOpen={vi.fn()} onOpenInToolbar={vi.fn()} /></I18nProvider>)
}

describe('AgentOriginBadge', () => {
  it('marks chats started in a Lark or Feishu direct message on the Agent page', () => {
    renderList()
    const lark = within(screen.getByText('Weekly status').closest('a')!).getByText('Lark')
    expect(lark).toHaveAttribute('data-agent-origin', 'lark')
    expect(lark).toHaveAttribute('title', 'Started in a Lark direct message')
    expect(within(screen.getByText('本周进展').closest('a')!).getByText('Feishu')).toHaveAttribute('title', 'Started in a Feishu direct message')
    expect(screen.getByText('Started in Flow').closest('a')!.querySelector('[data-agent-origin]')).toBeNull()
  })

  it('shows 飞书 in Chinese', async () => {
    localStorage.setItem(LOCALE_STORAGE_KEY, 'zh-CN')
    try {
      renderList()
      const feishu = await screen.findByText('飞书')
      expect(feishu).toHaveAttribute('title', '在飞书私聊中发起')
      expect(screen.getByText('Lark')).toHaveAttribute('title', '在 Lark 私聊中发起')
    } finally { localStorage.removeItem(LOCALE_STORAGE_KEY) }
  })

  it('translates its strings', () => {
    for (const source of ['Started in a Lark direct message', 'Started in a Feishu direct message', 'Feishu']) {
      expect(translateToChinese(source), source).not.toBe(source)
    }
  })
})

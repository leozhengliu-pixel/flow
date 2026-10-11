import { render, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { loadChineseDictionary } from '@/i18n/translate'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { CommandMenu } from './command-menu'
import { resetCommandContext } from './command-context'

const api = vi.hoisted(() => ({ searchWorkspace: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api }))

const data = makeBootstrap({ issues: [makeIssue()] })
const noop = vi.fn()

function renderMenu() {
  render(<MemoryRouter><I18nProvider>
    <CommandMenu open onOpenChange={noop} data={data} onUpdateIssue={vi.fn()} onUpdateIssues={vi.fn()}
      onCreateIssue={noop} onCreateIssueTemplate={noop} onCreateProject={noop} onCreateView={noop} onCreateInitiative={noop} onSearchWorkspace={noop}
      onNavigateInbox={noop} onNavigateMyIssues={noop} onNavigateProjects={noop} onNavigateInitiatives={noop} onNavigateViews={noop} onNavigateMembers={noop}
      onNavigateCustomers={noop} onGoToCustomers={noop} onNavigateAgent={noop} onOpenResult={noop}/>
  </I18nProvider></MemoryRouter>)
  return userEvent.setup()
}
const input = () => document.querySelector<HTMLInputElement>('[cmdk-input]')!
const items = () => [...document.querySelectorAll('[cmdk-item]')].map(item => item.textContent ?? '')

describe('command menu search in Chinese', () => {
  beforeAll(async () => { await loadChineseDictionary() })
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'zh-CN')
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    api.searchWorkspace.mockResolvedValue({ results: [] })
    api.listIssueRecords.mockResolvedValue({ items: [] })
  })
  afterEach(() => { resetCommandContext(); vi.unstubAllGlobals(); vi.clearAllMocks() })

  it('finds commands by their displayed Chinese title and by the English one', async () => {
    const user = renderMenu()
    await waitFor(() => expect(items().length).toBeGreaterThan(3))
    await user.type(input(), '复制当前页面链接')
    await waitFor(() => expect(items().filter(text => text.includes('复制当前页面链接'))).toHaveLength(1))
    expect(items().some(text => text.includes('前往客户'))).toBe(false)
    await user.clear(input())
    await user.type(input(), '前往客户')
    await waitFor(() => expect(items().some(text => text.includes('前往客户'))).toBe(true))
    expect(items().some(text => text.includes('复制当前页面链接'))).toBe(false)
    await user.clear(input())
    await user.type(input(), 'copy current page')
    await waitFor(() => expect(items().some(text => text.includes('复制当前页面链接'))).toBe(true))
  })
})

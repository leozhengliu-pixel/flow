import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { loadChineseDictionary } from '@/i18n/translate'
import { MyIssuesFilterMenu } from './my-issues-filter-menu'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from './my-issues-surface'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })
Object.defineProperty(Element.prototype, 'scrollIntoView', { configurable: true, value: () => {} })

const priorities: MyIssuesFilterOption[] = ['No priority', 'Urgent', 'High', 'Medium', 'Low'].map((label, id) => ({ id: String(id), label, kind: 'priority', priority: id as 0 | 1 | 2 | 3 | 4 }))
const statuses: MyIssuesFilterOption[] = [{ id: 'todo', label: 'Todo', kind: 'status', stateType: 'unstarted' }]
const options = (field: MyIssuesFilterKey): MyIssuesFilterOption[] | undefined => field === 'priority' ? priorities : field === 'status' ? statuses : undefined

function renderMenu(extra: Partial<React.ComponentProps<typeof MyIssuesFilterMenu>> = {}) {
  const onToggle = vi.fn()
  render(<I18nProvider><MyIssuesFilterMenu open trigger={<button type="button">Filter</button>} options={options} onOpenChange={vi.fn()} onToggle={onToggle} {...extra}/></I18nProvider>)
  return { onToggle, user: userEvent.setup() }
}
const rootInput = () => document.querySelector<HTMLInputElement>('[cmdk-input]')!
const rootRows = () => [...document.querySelectorAll('[cmdk-item]')].map(item => item.textContent?.replace('▶', '').trim())

describe('issue filter menu search in Chinese', () => {
  beforeAll(async () => { await loadChineseDictionary() })
  beforeEach(() => localStorage.setItem('flow:locale', 'zh-CN'))

  it('finds root fields by their displayed Chinese label and still by the English one', async () => {
    const { user } = renderMenu()
    expect(rootRows()).toContain('状态')
    await user.type(rootInput(), '状态')
    expect(rootRows()).toContain('状态')
    expect(rootRows()).not.toContain('优先级')
    await user.clear(rootInput())
    await user.type(rootInput(), 'prio')
    expect(rootRows()).toContain('优先级')
    expect(rootRows()).not.toContain('状态')
  })

  it('deep-matches values by their Chinese label, listing the translated path', async () => {
    const { user, onToggle } = renderMenu()
    await user.type(rootInput(), '紧急')
    const deep = [...document.querySelectorAll('[cmdk-item]')].filter(item => item.textContent?.includes('紧急') && !item.textContent.startsWith('AI'))
    expect(deep).toHaveLength(1)
    expect(deep[0]).toHaveTextContent('优先级')
    expect(rootRows().filter(row => row?.includes('高'))).toEqual([])
    await user.click(deep[0])
    expect(onToggle).toHaveBeenCalledWith('priority', expect.objectContaining({ id: '1', label: 'Urgent' }))
  })

  it('searches a field submenu by Chinese and English value text', async () => {
    const { user } = renderMenu()
    fireEvent.mouseMove(screen.getByRole('option', { name: /优先级/ }))
    const list = await screen.findByRole('listbox', { name: 'Priority' })
    const search = within(list.closest('[data-level="1"]') as HTMLElement).getByRole('searchbox')
    const labels = () => within(list).queryAllByRole('option').map(row => row.textContent?.replace(/\d+ (issues?|事项)$/, '').trim())
    await user.type(search, '紧急')
    expect(labels()).toEqual(['紧急'])
    await user.clear(search)
    await user.type(search, 'urgent')
    expect(labels()).toEqual(['紧急'])
    await user.clear(search)
    await user.type(search, '不存在')
    expect(labels()).toEqual([])
  })

  it('searches the advanced editor menu in Chinese', async () => {
    const { user } = renderMenu({ variant: 'advanced', onAddGroup: vi.fn() })
    await user.type(rootInput(), '添加筛选')
    expect(rootRows()).toEqual(['添加筛选条件组'])
  })

  it('searches the documents layout in Chinese', async () => {
    const people = (field: MyIssuesFilterKey): MyIssuesFilterOption[] | undefined => field === 'creator' || field === 'project' ? [{ id: 'u1', label: 'Dev User' }] : undefined
    const { user } = renderMenu({ layout: 'documents', options: people, onAdvanced: vi.fn() })
    await user.type(rootInput(), '创建者')
    expect(rootRows()).toEqual(['创建者'])
    await user.clear(rootInput())
    await user.type(rootInput(), 'creator')
    expect(rootRows()).toEqual(['创建者'])
  })
})

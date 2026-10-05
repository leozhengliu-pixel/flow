import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider, translateToChinese } from '@/i18n/i18n'
import { zhCN } from '@/i18n/translations'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import type { SavedView, Team, User } from '@/types/flow'
import { dateFilterMenu } from './issue-date-filter'
import { SavedViewMenu } from './saved-view-editor'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))

const view = { id: 'view-1', name: 'Urgent todo', description: '', scope: 'workspace', resource: 'issues', view: 'all', filters: [], display: {}, createdAt: '', updatedAt: '' } as SavedView
const users = [{ id: 'user-1', name: 'Dev User', displayName: 'Dev User', email: 'dev@example.com', active: true }] as User[]
const teams = [{ id: 'team-1', name: 'Alpha', key: 'ALP' }] as Team[]

/** Every visible label sits in an element: a bare text node would paint under the row's highlight layer. */
function expectNoBareText(row: HTMLElement) {
  const bare = [...row.childNodes].filter(node => node.nodeType === Node.TEXT_NODE && node.textContent?.trim())
  expect(bare, row.textContent ?? '').toHaveLength(0)
}

describe("saved view '…' menu", () => {
  it("matches Linear's items, order, icons and wrapped labels (no Share view)", async () => {
    const user = userEvent.setup()
    render(<I18nProvider><SavedViewMenu view={view} users={users} teams={teams} onEdit={vi.fn()} onDuplicate={vi.fn()} onUpdate={vi.fn()} onSetSubscriptionEvents={vi.fn()} onCopy={vi.fn()} onExport={vi.fn()} onDelete={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Issue view options' }))
    const menu = screen.getByRole('menu')
    const rows = within(menu).getAllByRole('menuitem')
    expect(rows.map(row => row.textContent?.replace('▶', '').trim())).toEqual(['Edit…', 'Duplicate…', 'Owner', 'Move to', 'Subscribe', 'Configure custom view Slack notifications…', 'Copy link', 'Export issues as CSV…', 'Delete'])
    expect(within(menu).getAllByRole('separator')).toHaveLength(3)
    for (const row of rows) {
      expectNoBareText(row)
      expect(row.querySelector('svg'), row.textContent ?? '').not.toBeNull()
    }
    expect(within(menu).queryByText(/Share view|Disable public link/)).not.toBeInTheDocument()
    expect(rows.filter(row => row.textContent?.includes('▶')).map(row => row.textContent?.replace('▶', ''))).toEqual(['Owner', 'Move to', 'Subscribe'])
    expect(within(menu).getByRole('menuitem', { name: 'Configure custom view Slack notifications…' })).toHaveAttribute('href', expect.stringMatching(/\/settings\/integrations\/slack$/))
    expect(within(menu).getByRole('menuitem', { name: 'Delete' })).not.toHaveAttribute('data-danger')
  })

  it('translates every item in Chinese', async () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    const user = userEvent.setup()
    render(<I18nProvider><SavedViewMenu view={view} users={users} teams={teams} onEdit={vi.fn()} onDuplicate={vi.fn()} onUpdate={vi.fn()} onSetSubscriptionEvents={vi.fn()} onCopy={vi.fn()} onExport={vi.fn()} onDelete={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: '事项视图选项' }))
    const labels = within(screen.getByRole('menu')).getAllByRole('menuitem').map(row => row.textContent?.replace('▶', '').trim())
    expect(labels).toEqual(['编辑…', '复制…', '所有者', '移动到', '订阅', '配置自定义视图 Slack 通知…', '复制链接', '将事项导出为 CSV…', '删除'])
  })
})

const statusOptions: MyIssuesFilterOption[] = [{ id: 'todo', label: 'Todo', kind: 'status', stateType: 'unstarted', count: 2 }]
const options = (field: MyIssuesFilterKey) => field === 'dates' ? dateFilterMenu() : field === 'status' ? statusOptions : undefined

describe('filter menus', () => {
  it('aligns "Add filter group" with the other rows: a 16px glyph in the shared icon slot', async () => {
    render(<I18nProvider><MyIssuesFilterMenu open variant="advanced" trigger={<button type="button">Filter</button>} options={options} onOpenChange={vi.fn()} onToggle={vi.fn()} onAddGroup={vi.fn()}/></I18nProvider>)
    const group = await screen.findByRole('option', { name: /Add filter group/ })
    const status = screen.getByRole('option', { name: /Status/ })
    expectNoBareText(group)
    expect(group.className).toBe(status.className)
    const [groupIcon, statusIcon] = [group.firstElementChild!, status.firstElementChild!]
    expect(groupIcon.className).toBe(statusIcon.className)
    expect(groupIcon.querySelector('svg[data-linear-glyph="filterGroup"]')).not.toBeNull()
    expect(group.textContent).not.toContain('( )')
  })

  it('nests Dates › Due date › presets beside each other, one level above the parent, without phantom columns', async () => {
    render(<I18nProvider><MyIssuesFilterMenu open trigger={<button type="button">Filter</button>} options={options} onOpenChange={vi.fn()} onToggle={vi.fn()} onAdvanced={vi.fn()}/></I18nProvider>)
    fireEvent.mouseMove(await screen.findByRole('option', { name: /^Dates/ }))
    const dates = await screen.findByRole('listbox', { name: 'Dates' })
    const datesMenu = dates.closest('[data-level="1"]') as HTMLElement
    expect(datesMenu).toHaveStyle({ zIndex: '601' })
    // A category menu: search kept for typing but hidden, icon-first rows, Linear's ▶ marker, no checkbox column.
    expect(datesMenu.querySelector('[data-hidden]')).not.toBeNull()
    const due = within(dates).getByRole('option', { name: /Due date/ })
    expect(due).toHaveAttribute('data-row-kind', 'icon')
    expect(due.querySelector('[role=checkbox]')).toBeNull()
    expect(due.firstElementChild?.querySelector('svg[data-linear-glyph="dates"]')).not.toBeNull()
    expect(due.lastElementChild?.textContent).toBe('▶')
    fireEvent.mouseMove(due)
    const presets = await screen.findByRole('listbox', { name: 'Due date' })
    const presetsMenu = presets.closest('[data-level="2"]') as HTMLElement
    expect(presetsMenu).toHaveStyle({ zIndex: '602' })
    expect(presetsMenu.querySelector('[data-hidden]')).toBeNull()
    const overdue = within(presets).getByRole('option', { name: 'Overdue' })
    expect(overdue).toHaveAttribute('data-row-kind', 'plain')
    expect(overdue.querySelector('svg, [role=checkbox]')).toBeNull()
    // Hovering a sibling row closes the open nested submenu (one submenu per level).
    fireEvent.mouseMove(within(dates).getByRole('option', { name: /Created date/ }))
    expect(await screen.findByRole('listbox', { name: 'Created date' })).toBeInTheDocument()
    expect(screen.queryByRole('listbox', { name: 'Due date' })).not.toBeInTheDocument()
  })

  it('keeps the checkbox column (and counts) only on selectable values', async () => {
    const relations = (field: MyIssuesFilterKey) => field === 'relations' ? [{ id: 'parent_of', label: 'Parent issues', count: 3 }] : options(field)
    render(<I18nProvider><MyIssuesFilterMenu open trigger={<button type="button">Filter</button>} options={relations} onOpenChange={vi.fn()} onToggle={vi.fn()}/></I18nProvider>)
    fireEvent.mouseMove(await screen.findByRole('option', { name: /^Status/ }))
    const todo = within(await screen.findByRole('listbox', { name: 'Status' })).getByRole('option', { name: /Todo/ })
    expect(todo).toHaveAttribute('data-row-kind', 'check')
    expect(todo.querySelector('[role=checkbox]')).not.toBeNull()
    expect(todo).toHaveTextContent('2 issues')
    fireEvent.mouseMove(screen.getByRole('option', { name: /^Relations/ }))
    const parent = within(await screen.findByRole('listbox', { name: 'Relations' })).getByRole('option', { name: /Parent issues/ })
    expect(parent).toHaveAttribute('data-row-kind', 'icon')
    expect(parent.querySelector('[role=checkbox]')).toBeNull()
    expect(parent).not.toHaveTextContent('3 issues')
  })
})

describe('Agent stays untranslated', () => {
  it('never translates the word Agent', () => {
    expect(translateToChinese('Agent')).toBe('Agent')
    expect(translateToChinese('Agent Session')).toBe('Agent 会话')
    expect(translateToChinese('Any agent')).toBe('任意 Agent')
    expect(translateToChinese('No agent')).toBe('无 Agent')
    expect(Object.values(zhCN).filter(value => /智能助手|智能体/.test(value))).toEqual([])
  })
})

import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { User } from '@/types/flow'
import { I18nProvider } from '@/i18n/i18n'
import { AssigneePicker } from '@/components/issue/core-property-pickers'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import { explorerFilterOptions, explorerPropertyOptions } from '@/components/issue-explorer/issue-explorer-model'
import { makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const flow = { id: 'app_flow', name: 'Flow', displayName: 'Flow', email: '', active: true, app: true, builtinAgent: true, appScopes: ['read', 'write', 'app:assignable'], appTeamIds: ['team-1'] } as User
const elsewhere = { ...flow, id: 'app_other', name: 'Other agent', displayName: 'Other agent', appTeamIds: ['team-2'] } as User

beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))

describe('assignee picker agents (Linear)', () => {
  it('lists the agents that can work in the team in their own section with the Agent pill and delegates on select', async () => {
    const user = userEvent.setup(), onChange = vi.fn(), onDelegate = vi.fn()
    render(<I18nProvider><AssigneePicker value={viewer} users={[viewer, teammate, flow, elsewhere]} teamId="team-1" onChange={onChange} onDelegate={onDelegate}/></I18nProvider>)
    await user.click(screen.getByRole('combobox', { name: /Change assignee/ }))
    const option = await screen.findByRole('option', { name: /Flow/ })
    expect(within(option).getByText('Agent')).toHaveAttribute('data-agent-badge')
    expect(screen.getByText('Agents')).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /Other agent/ })).not.toBeInTheDocument()
    expect(within(screen.getByRole('option', { name: /Teammate/ })).queryByText('Agent')).not.toBeInTheDocument()
    await user.click(option)
    expect(onDelegate).toHaveBeenCalledWith(flow.id)
    expect(onChange).not.toHaveBeenCalled()
  })
})

describe('filter menu Triage Intelligence submenu', () => {
  it('replaces the top-level Suggested label with Triage Intelligence ▸ Suggested assignee ▸ users and agents', async () => {
    const user = userEvent.setup(), onToggle = vi.fn()
    const issue = makeIssue({ suggestedAssigneeIds: [teammate.id] })
    const data = makeBootstrap({ users: [viewer, teammate, flow], issues: [issue] })
    const options = explorerPropertyOptions(data)
    render(<I18nProvider><MyIssuesFilterMenu open onOpenChange={vi.fn()} onToggle={onToggle} options={field => explorerFilterOptions(field, options)} trigger={<button type="button">Filter</button>}/></I18nProvider>)
    expect(screen.queryByRole('option', { name: 'Suggested label' })).not.toBeInTheDocument()
    fireEvent.mouseMove(screen.getByRole('option', { name: 'Triage Intelligence' }))
    const submenu = await screen.findByRole('listbox', { name: 'Triage Intelligence' })
    for (const name of ['Suggested assignee', 'Suggested project', 'Suggested label', 'Suggested team']) expect(within(submenu).getByRole('option', { name })).toHaveAttribute('aria-haspopup', 'listbox')
    expect(within(submenu).getByRole('option', { name: /Suggested relations/ })).toBeInTheDocument()
    expect(within(submenu).getByRole('option', { name: /Suggested duplicates/ })).toBeInTheDocument()
    fireEvent.mouseMove(within(submenu).getByRole('option', { name: 'Suggested assignee' }))
    const people = await screen.findByRole('listbox', { name: 'Suggested assignee' })
    expect(within(people).getByRole('option', { name: /Any user/ })).toHaveTextContent(/Any user.*1.*issue/)
    expect(within(people).getByRole('option', { name: /Flow/ })).toHaveTextContent(/Flow\s*Agent/)
    // Deep search reaches the nested values: "Triage Intelligence › Suggested assignee › Teammate".
    await user.type(screen.getByLabelText('Add Filter…'), 'Teammate')
    await user.click(await screen.findByRole('option', { name: /Suggested assignee › Teammate/ }))
    expect(onToggle).toHaveBeenCalledWith('triageIntelligence', expect.objectContaining({ id: `assignee:${teammate.id}`, filterLabel: 'Suggested assignee' }))
  })
})

describe('filter menu Agent Session submenu', () => {
  const sessions = () => {
    const issue = makeIssue({ delegate: flow, agentSessionId: 'task-1', agentSessionState: 'merged' })
    const options = explorerPropertyOptions(makeBootstrap({ users: [viewer, teammate, flow], issues: [issue] }))
    return (field: Parameters<typeof explorerFilterOptions>[0]) => explorerFilterOptions(field, options)
  }

  it("lists exactly Linear's four session states with their glyphs and a search band", async () => {
    const onToggle = vi.fn()
    render(<I18nProvider><MyIssuesFilterMenu open onOpenChange={vi.fn()} onToggle={onToggle} options={sessions()} trigger={<button type="button">Filter</button>}/></I18nProvider>)
    fireEvent.mouseMove(screen.getByRole('option', { name: 'Agent Session' }))
    const submenu = await screen.findByRole('listbox', { name: 'Agent Session' })
    const rows = within(submenu).getAllByRole('option')
    expect(rows.map(row => row.querySelector('[data-i18n-ignore]')?.textContent)).toEqual(['Active', 'Error', 'Dismissed', 'Merged'])
    expect(rows.map(row => row.querySelector('[data-linear-glyph]')?.getAttribute('data-linear-glyph'))).toEqual(['agentSessionActive', 'agentSessionError', 'agentSessionDismissed', 'agentSessionMerged'])
    expect(rows[0].querySelector('svg')).toHaveStyle({ color: 'var(--agent-session-active)' })
    for (const row of rows) expect(row).toHaveAttribute('data-row-kind', 'check')
    expect(rows[3]).toHaveTextContent('1 issue')
    expect(screen.getByRole('searchbox', { name: 'Filter Agent Session' })).toBeInTheDocument()
    fireEvent.click(rows[3])
    expect(onToggle).toHaveBeenCalledWith('agentSession', expect.objectContaining({ id: 'state:merged', label: 'Merged' }))
  })

  it('translates the states but keeps "Agent" untranslated', async () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    render(<I18nProvider><MyIssuesFilterMenu open onOpenChange={vi.fn()} onToggle={vi.fn()} options={sessions()} trigger={<button type="button">Filter</button>}/></I18nProvider>)
    fireEvent.mouseMove(screen.getByRole('option', { name: 'Agent 会话' }))
    const submenu = await screen.findByRole('listbox', { name: 'Agent 会话' })
    expect(within(submenu).getAllByRole('option').map(row => row.querySelector('[data-i18n-ignore]')?.textContent)).toEqual(['进行中', '错误', '已忽略', '已合并'])
  })
})

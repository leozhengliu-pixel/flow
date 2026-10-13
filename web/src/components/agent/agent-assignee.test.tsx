import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { User } from '@/types/flow'
import { I18nProvider } from '@/i18n/i18n'
import { AssigneePicker } from '@/components/issue/core-property-pickers'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import { explorerFilterOptions, explorerPropertyOptions } from '@/components/issue-explorer/issue-explorer-model'
import { BUILTIN_AGENT_AVATAR_URL } from '@/components/ui/agent-avatar-url'
import { makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const flow = { id: 'app_flow', name: 'Flow', displayName: 'Flow', avatarUrl: BUILTIN_AGENT_AVATAR_URL, email: '', active: true, app: true, builtinAgent: true, appScopes: ['read', 'write', 'app:assignable'], appTeamIds: ['team-1'] } as User
const elsewhere = { ...flow, id: 'app_other', name: 'Other agent', displayName: 'Other agent', appTeamIds: ['team-2'] } as User

beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))

const openPicker = async (user: ReturnType<typeof userEvent.setup>) => {
  await user.click(screen.getByRole('combobox', { name: /Change assignee/ }))
  const search = await screen.findByRole('textbox', { name: 'Assign to…' })
  await waitFor(() => expect(search).toHaveFocus())
  return search
}
const names = () => within(screen.getByRole('listbox', { name: 'Assignee' })).getAllByRole('option').map(option => option.querySelector('.property-command-label')?.textContent)
const shortcuts = () => within(screen.getByRole('listbox', { name: 'Assignee' })).getAllByRole('option').map(option => option.querySelector('kbd')?.textContent ?? '')

describe('assignee picker agents', () => {
  const people = [viewer, teammate, flow, elsewhere]
  const mount = (props: Partial<Parameters<typeof AssigneePicker>[0]> = {}) => {
    const handlers = { onChange: vi.fn(), onDelegate: vi.fn(), onInvite: vi.fn() }
    render(<I18nProvider><AssigneePicker value={viewer} users={people} teamId="team-1" {...handlers} {...props}/></I18nProvider>)
    return handlers
  }

  it('lists No assignee, people, then the team\'s agents in one list with number keys, no Agents header and no pill', async () => {
    const user = userEvent.setup()
    mount()
    await openPicker(user)
    expect(names()).toEqual(['No assignee', viewer.displayName, teammate.displayName, 'Flow', 'Invite and assign…'])
    expect(shortcuts()).toEqual(['0', '1', '2', '3', ''])
    expect(screen.queryByText('Agents')).not.toBeInTheDocument()
    expect(document.querySelector('.property-command-options [data-agent-badge]')).toBeNull()
    expect(screen.queryByRole('option', { name: /Other agent/ })).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Assign to…' }).parentElement?.querySelector('kbd')).toHaveTextContent('A')
  })

  it('shows the agent avatar (logo tile) at 16px and the invite action under "New user"', async () => {
    const user = userEvent.setup()
    mount()
    await openPicker(user)
    const row = screen.getByRole('option', { name: /Flow/ })
    expect(row.querySelector('[data-agent-avatar]')).not.toBeNull()
    expect(screen.getByText('New user')).toBeInTheDocument()
    const invite = screen.getByRole('option', { name: 'Invite and assign…' })
    expect(invite.querySelector('[data-flow-glyph="invitePlane"]')).not.toBeNull()
    expect(invite.querySelector('kbd')).toBeNull()
  })

  it('delegates on select and keeps the human assignee', async () => {
    const user = userEvent.setup()
    const { onChange, onDelegate } = mount()
    await openPicker(user)
    await user.click(screen.getByRole('option', { name: /Flow/ }))
    expect(onDelegate).toHaveBeenCalledWith(flow.id)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('selects the row whose number key is pressed while the search is empty', async () => {
    const user = userEvent.setup()
    const { onChange, onDelegate } = mount()
    await openPicker(user)
    await user.keyboard('3')
    expect(onDelegate).toHaveBeenCalledWith(flow.id)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('0 picks No assignee and 2 assigns the second person', async () => {
    const user = userEvent.setup()
    const { onChange } = mount()
    await openPicker(user)
    await user.keyboard('2')
    expect(onChange).toHaveBeenCalledWith(teammate.id)
    await openPicker(user)
    await user.keyboard('0')
    expect(onChange).toHaveBeenLastCalledWith('')
  })

  it('a digit typed after other characters stays in the search', async () => {
    const user = userEvent.setup()
    const { onChange, onDelegate } = mount()
    const search = await openPicker(user)
    await user.type(search, 'Fl1')
    expect(search).toHaveValue('Fl1')
    expect(onChange).not.toHaveBeenCalled()
    expect(onDelegate).not.toHaveBeenCalled()
  })

  it('opens the invite flow from "Invite and assign…"', async () => {
    const user = userEvent.setup()
    const { onChange, onDelegate, onInvite } = mount()
    await openPicker(user)
    await user.click(screen.getByRole('option', { name: 'Invite and assign…' }))
    expect(onInvite).toHaveBeenCalledTimes(1)
    expect(onChange).not.toHaveBeenCalled()
    expect(onDelegate).not.toHaveBeenCalled()
  })

  it('shows the delegate in the trigger with both avatars, marks it selected and removes it when picked again', async () => {
    const user = userEvent.setup()
    const { onChange, onDelegate } = mount({ delegate: flow })
    const trigger = screen.getByRole('combobox', { name: /Change assignee/ })
    expect(trigger).toHaveTextContent('Flow')
    expect(trigger.querySelectorAll('.core-assignee-stack > .avatar, .core-assignee-stack > [data-agent-avatar-slot]')).toHaveLength(2)
    await openPicker(user)
    expect(screen.getByRole('option', { name: /Flow/ })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('option', { name: new RegExp(viewer.displayName) })).toHaveAttribute('aria-checked', 'true')
    await user.click(screen.getByRole('option', { name: /Flow/ }))
    expect(onDelegate).toHaveBeenCalledWith('')
    expect(onChange).not.toHaveBeenCalled()
  })

  it('"No assignee" clears the assignee, or removes the delegate when only a delegate is set', async () => {
    const user = userEvent.setup()
    const first = mount({ delegate: flow })
    await openPicker(user)
    await user.click(screen.getByRole('option', { name: /No assignee/ }))
    expect(first.onChange).toHaveBeenCalledWith('')
    expect(first.onDelegate).not.toHaveBeenCalled()
    cleanup()
    const second = mount({ value: undefined, delegate: flow })
    expect(screen.getByRole('combobox', { name: 'Change assignee. Flow is delegated' })).toHaveTextContent('Flow')
    await openPicker(user)
    await user.click(screen.getByRole('option', { name: /No assignee/ }))
    expect(second.onDelegate).toHaveBeenCalledWith('')
    expect(second.onChange).not.toHaveBeenCalled()
  })

  it('offers no agents when the workspace keeps guests away from them', async () => {
    const user = userEvent.setup()
    mount({ allowAgents: false })
    await openPicker(user)
    expect(names()).toEqual(['No assignee', viewer.displayName, teammate.displayName, 'Invite and assign…'])
  })

  it('the agent hover card has the 32px avatar, name, lowercase handle and the Agent pill', async () => {
    const user = userEvent.setup()
    mount()
    await openPicker(user)
    await user.hover(screen.getByRole('option', { name: /Flow/ }))
    const card = await screen.findByRole('tooltip', undefined, { timeout: 3000 })
    expect(card).toHaveClass('agent-hover-surface')
    expect(within(card).getByText('Flow')).toBeInTheDocument()
    expect(within(card).getByText('flow')).toBeInTheDocument()
    expect(within(card).getByText('Agent')).toHaveAttribute('data-agent-badge')
    expect(card.querySelector('.agent-hover-card__avatar [data-agent-avatar]')).not.toBeNull()
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

  it("lists the reference app's four session states plus Stale with their glyphs and a search band", async () => {
    const onToggle = vi.fn()
    render(<I18nProvider><MyIssuesFilterMenu open onOpenChange={vi.fn()} onToggle={onToggle} options={sessions()} trigger={<button type="button">Filter</button>}/></I18nProvider>)
    fireEvent.mouseMove(screen.getByRole('option', { name: 'Agent Session' }))
    const submenu = await screen.findByRole('listbox', { name: 'Agent Session' })
    const rows = within(submenu).getAllByRole('option')
    expect(rows.map(row => row.querySelector('[data-i18n-ignore]')?.textContent)).toEqual(['Active', 'Error', 'Dismissed', 'Merged', 'Stale'])
    expect(rows.map(row => row.querySelector('[data-flow-glyph]')?.getAttribute('data-flow-glyph'))).toEqual(['agentSessionActive', 'agentSessionError', 'agentSessionDismissed', 'agentSessionMerged', 'agentSessionDismissed'])
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
    expect(within(submenu).getAllByRole('option').map(row => row.querySelector('[data-i18n-ignore]')?.textContent)).toEqual(['进行中', '错误', '已忽略', '已合并', '已停滞'])
  })
})

import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { User } from '@/types/flow'
import { I18nProvider } from '@/i18n/i18n'
import { IssueAssigneeProperty } from '@/components/issue/issue-assignee-property'
import { AssigneePicker } from '@/components/issue/core-property-pickers'
import { NestingLine } from '@/components/ui/nesting-line'
import { BUILTIN_AGENT_AVATAR_URL } from '@/components/ui/agent-avatar-url'
import { teammate, viewer } from '@/test/fixtures'

const confirmAction = vi.hoisted(() => vi.fn<(title: string, options?: Record<string, unknown>) => Promise<boolean>>())
vi.mock('@/components/ui/action-dialog-service', async importOriginal => ({ ...await importOriginal<typeof import('@/components/ui/action-dialog-service')>(), confirmAction }))

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const flow = { id: 'app_flow', name: 'flow', displayName: 'Flow', avatarUrl: BUILTIN_AGENT_AVATAR_URL, email: '', active: true, app: true, appScopes: ['read', 'write', 'app:assignable'], appTeamIds: ['team-1'] } as User
const coder = { ...flow, id: 'app_coder', name: 'coder', displayName: 'Code Agent', avatarUrl: undefined } as User
const users = [viewer, teammate, flow, coder]

beforeEach(() => { localStorage.setItem('flow:locale', 'en-US'); confirmAction.mockReset() })
afterEach(cleanup)

function mount(issue: { assignee?: User; delegate?: User; agentSessionState?: 'active' }) {
  const onUpdate = vi.fn()
  const view = render(<I18nProvider><aside className="issue-properties"><IssueAssigneeProperty issue={issue} users={users} teamId="team-1" workspaceKey="acme" allowAgents hoverContext={{ workspaceName: 'Acme' }} onUpdate={onUpdate}/></aside></I18nProvider>)
  return { onUpdate, ...view }
}
const assigneeTrigger = () => screen.getByRole('combobox', { name: /Change assignee/ })
const delegateTrigger = () => screen.getByRole('combobox', { name: /Change agent/ })

describe('issue sidebar assignee + delegate (the reference app IssueDetailsPaneSidebar)', () => {
  it('hangs the delegated agent under the human assignee on a nesting line', () => {
    const { container } = mount({ assignee: viewer, delegate: flow })
    expect(assigneeTrigger()).toHaveTextContent(viewer.displayName)
    expect(assigneeTrigger()).not.toHaveTextContent('Flow')
    expect(assigneeTrigger().querySelectorAll('.avatar')).toHaveLength(1)
    expect(delegateTrigger()).toHaveTextContent('Flow')
    const row = container.querySelector('.issue-delegate-row')!
    expect(row).not.toBeNull()
    const line = row.querySelector<HTMLElement>('.issue-delegate-nesting-line')!
    expect(line).toHaveAttribute('aria-hidden', 'true')
    expect(line.style.height).toBe('10px')
    expect(line.querySelector('svg')?.style.bottom).toBe('-8px')
    expect(screen.getByRole('link', { name: 'Go to agent' })).toHaveAttribute('href', '/acme/profiles/flow')
    expect(screen.getByRole('link', { name: 'Go to user' })).toBeInTheDocument()
  })

  it('shows "Assign" above the delegate when no human is assigned', () => {
    const { container } = mount({ delegate: flow })
    expect(assigneeTrigger()).toHaveTextContent('Assign')
    expect(assigneeTrigger()).not.toHaveTextContent('Flow')
    expect(assigneeTrigger()).toHaveClass('muted')
    expect(delegateTrigger()).toHaveTextContent('Flow')
    expect(container.querySelector('.issue-delegate-nesting-line')).not.toBeNull()
    expect(screen.queryByRole('link', { name: 'Go to user' })).toBeNull()
  })

  it('renders no delegate row or line without a delegate', () => {
    const { container } = mount({ assignee: viewer })
    expect(container.querySelector('.issue-delegate-row')).toBeNull()
    expect(container.querySelector('[data-nesting-line]')).toBeNull()
    expect(screen.queryByRole('combobox', { name: /Change agent/ })).toBeNull()
  })

  it('removes the delegate from its own row with "No agent" and keeps the human', async () => {
    const user = userEvent.setup()
    const { onUpdate } = mount({ assignee: viewer, delegate: flow })
    await user.click(delegateTrigger())
    expect(await screen.findByRole('textbox', { name: 'Delegate to…' })).toBeInTheDocument()
    const listbox = screen.getByRole('listbox', { name: 'Agent' })
    expect(within(listbox).getAllByRole('option').map(option => option.querySelector('.property-command-label')?.textContent)).toEqual(['No agent', 'Flow', 'Code Agent'])
    expect(within(listbox).getByRole('option', { name: /No agent/ }).querySelector('[data-flow-glyph="noAgent"]')).not.toBeNull()
    await user.click(within(listbox).getByRole('option', { name: /No agent/ }))
    expect(onUpdate).toHaveBeenCalledTimes(1)
    expect(onUpdate).toHaveBeenCalledWith({ delegateId: '' })
  })

  it('re-delegates to another agent from the delegate row', async () => {
    const user = userEvent.setup()
    const { onUpdate } = mount({ assignee: viewer, delegate: flow })
    await user.click(delegateTrigger())
    await user.click(await screen.findByRole('option', { name: /Code Agent/ }))
    expect(onUpdate).toHaveBeenCalledWith({ delegateId: coder.id })
  })

  it('nests the delegated agent under the assignee in the assignee menu', async () => {
    const user = userEvent.setup()
    mount({ assignee: viewer, delegate: flow })
    await user.click(assigneeTrigger())
    const listbox = await screen.findByRole('listbox', { name: 'Assignee' })
    const labels = within(listbox).getAllByRole('option').map(option => option.querySelector('.property-command-label')?.textContent)
    expect(labels.indexOf('Flow')).toBe(labels.indexOf(viewer.displayName) + 1)
    const nested = within(listbox).getByRole('option', { name: /^Flow/ })
    expect(nested.querySelector('.core-nested-delegate-icon [data-nesting-line]')).not.toBeNull()
    expect(nested).toHaveAttribute('aria-selected')
  })

  it('animates a delegate that arrives while open, not one present on mount', () => {
    const onUpdate = vi.fn()
    const tree = (delegate?: User) => <I18nProvider><IssueAssigneeProperty issue={{ assignee: viewer, delegate }} users={users} teamId="team-1" workspaceKey="acme" allowAgents hoverContext={{ workspaceName: 'Acme' }} onUpdate={onUpdate}/></I18nProvider>
    const { container, rerender } = render(tree(flow))
    expect(container.querySelector('.issue-delegate-row')).not.toHaveAttribute('data-entering')
    rerender(tree())
    rerender(tree(coder))
    expect(container.querySelector('.issue-delegate-row')).toHaveAttribute('data-entering')
  })
})

describe('sidebar assignee picker data flow (delegateRow)', () => {
  const mountPicker = (props: Partial<Parameters<typeof AssigneePicker>[0]> = {}) => {
    const handlers = { onChange: vi.fn(), onDelegate: vi.fn() }
    render(<I18nProvider><AssigneePicker delegateRow value={viewer} users={users} teamId="team-1" {...handlers} {...props}/></I18nProvider>)
    return handlers
  }
  const open = async (user: ReturnType<typeof userEvent.setup>) => {
    await user.click(assigneeTrigger())
    await screen.findByRole('textbox', { name: 'Assign to…' })
  }

  it('picking an agent delegates and keeps the human assignee', async () => {
    const user = userEvent.setup()
    const { onChange, onDelegate } = mountPicker()
    await open(user)
    await user.click(screen.getByRole('option', { name: /Code Agent/ }))
    expect(onDelegate).toHaveBeenCalledWith(coder.id)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('picking an agent with nobody assigned only delegates (the server assigns the viewer)', async () => {
    const user = userEvent.setup()
    const { onChange, onDelegate } = mountPicker({ value: undefined })
    await open(user)
    await user.click(screen.getByRole('option', { name: /Code Agent/ }))
    expect(onDelegate).toHaveBeenCalledWith(coder.id)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('picking the delegated agent again changes nothing', async () => {
    const user = userEvent.setup()
    const { onChange, onDelegate } = mountPicker({ delegate: flow })
    await open(user)
    await user.click(screen.getByRole('option', { name: /^Flow/ }))
    expect(onDelegate).not.toHaveBeenCalled()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('"No assignee" unassigns, then "Remove agent" also removes the delegate', async () => {
    confirmAction.mockResolvedValue(true)
    const user = userEvent.setup()
    const { onChange, onDelegate } = mountPicker({ delegate: flow, delegateSessionActive: true })
    await open(user)
    await user.click(screen.getByRole('option', { name: /No assignee/ }))
    expect(onChange).toHaveBeenCalledWith('')
    await waitFor(() => expect(onDelegate).toHaveBeenCalledWith(''))
    const [title, options] = confirmAction.mock.calls[0]
    expect(title).toBe('Remove Flow too?')
    expect(options).toMatchObject({ confirmLabel: 'Remove agent', cancelLabel: 'Keep agent', danger: false })
    expect(String(options?.description)).toContain('archive its working session')
  })

  it('"Keep agent" leaves the delegate', async () => {
    confirmAction.mockResolvedValue(false)
    const user = userEvent.setup()
    const { onChange, onDelegate } = mountPicker({ delegate: flow })
    await open(user)
    await user.click(screen.getByRole('option', { name: /No assignee/ }))
    expect(onChange).toHaveBeenCalledWith('')
    await waitFor(() => expect(confirmAction).toHaveBeenCalled())
    expect(String(confirmAction.mock.calls[0][1]?.description)).not.toContain('archive')
    expect(onDelegate).not.toHaveBeenCalled()
  })

  it('"No assignee" without a delegate never asks', async () => {
    const user = userEvent.setup()
    const { onChange } = mountPicker()
    await open(user)
    await user.click(screen.getByRole('option', { name: /No assignee/ }))
    expect(onChange).toHaveBeenCalledWith('')
    expect(confirmAction).not.toHaveBeenCalled()
  })
})

describe('NestingLine', () => {
  it('draws a stem heightAbove + heightBelow tall with the curve heightBelow - 8 from its bottom', () => {
    const { container } = render(<NestingLine className="x" heightAbove={14} heightBelow={4}/>)
    const line = container.querySelector<HTMLElement>('.nesting-line.x')!
    expect(line.style.height).toBe('18px')
    const curve = line.querySelector('svg')!
    expect(curve.getAttribute('viewBox')).toBe('0 0 10 9')
    expect(curve.style.bottom).toBe('-4px')
  })
})

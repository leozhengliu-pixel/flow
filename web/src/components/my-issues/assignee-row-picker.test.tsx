import * as ContextMenu from '@radix-ui/react-context-menu'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { User } from '@/types/flow'
import { I18nProvider } from '@/i18n/i18n'
import { assigneeContextOptions } from '@/components/issue-explorer/issue-explorer-model'
import { BUILTIN_AGENT_AVATAR_URL } from '@/components/ui/agent-avatar-url'
import { PeopleMenuItems } from '@/components/property/people-menu-items'
import { teammate, viewer } from '@/test/fixtures'
import { MyIssuesBulkActionBar } from './my-issues-bulk-action-bar'
import { IssueContextMenu, RowCommandPicker, type MyIssuesRowData } from './my-issues-list'

const invite = vi.hoisted(() => ({ openMemberInvite: vi.fn(), viewerCanInvite: () => true }))
vi.mock('@/lib/invite-navigation', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/invite-navigation')>(), ...invite }))

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const flow = { id: 'app_flow', name: 'Flow', displayName: 'Flow', avatarUrl: BUILTIN_AGENT_AVATAR_URL, email: '', active: true, app: true, appScopes: ['app:assignable'], appTeamIds: ['team-1'] } as User
const options = assigneeContextOptions([viewer, teammate, flow])

beforeEach(() => { localStorage.setItem('flow:locale', 'en-US'); invite.openMemberInvite.mockReset() })

describe('assignee pickers in lists and boards', () => {
  const mount = (onSelect = vi.fn()) => {
    render(<I18nProvider><RowCommandPicker propertyLabel="Assignee" label="Assign to. Current assignee is Viewer" searchLabel="Assign to…" selectedIds={[viewer.id]} options={options} onSelect={onSelect} trigger={<span>trigger</span>}/></I18nProvider>)
    return onSelect
  }
  const open = async (user: ReturnType<typeof userEvent.setup>) => {
    await user.click(screen.getByRole('combobox'))
    const search = await screen.findByRole('textbox', { name: 'Assign to…' })
    await waitFor(() => expect(search).toHaveFocus())
  }

  it('lists No assignee, people and agents with number keys, the A hint, no pill and the invite row', async () => {
    const user = userEvent.setup()
    mount()
    await open(user)
    const rows = within(screen.getByRole('listbox', { name: 'Assignee' })).getAllByRole('option')
    expect(rows.map(row => row.querySelector('.property-command-label')?.textContent)).toEqual(['No assignee', viewer.displayName, teammate.displayName, 'Flow', 'Invite and assign…'])
    expect(rows.map(row => row.querySelector('kbd')?.textContent ?? '')).toEqual(['0', '1', '2', '3', ''])
    expect(screen.getByText('New user')).toBeInTheDocument()
    expect(screen.queryByText('Agents')).not.toBeInTheDocument()
    expect(document.querySelector('.property-command-options [data-agent-badge]')).toBeNull()
    expect(screen.getByRole('textbox', { name: 'Assign to…' }).parentElement?.querySelector('kbd')).toHaveTextContent('A')
    expect(rows[3].querySelector('[data-agent-avatar]')).not.toBeNull()
  })

  it('picks the row for a number key (agents continue the numbering) and opens the invite flow', async () => {
    const user = userEvent.setup()
    const onSelect = mount()
    await open(user)
    await user.keyboard('3')
    expect(onSelect).toHaveBeenCalledWith(flow.id)
    await open(user)
    await user.click(screen.getByRole('option', { name: 'Invite and assign…' }))
    expect(invite.openMemberInvite).toHaveBeenCalledTimes(1)
    expect(onSelect).toHaveBeenCalledTimes(1)
  })

  it('the explorer assignee options carry the agent flag, handle and no group', () => {
    expect(options.map(option => option.id)).toEqual(['', viewer.id, teammate.id, flow.id])
    expect(options[3]).toMatchObject({ agent: true, handle: 'Flow' })
    expect(options[3]).not.toHaveProperty('groupId')
    expect(assigneeContextOptions([viewer, flow], { allowAgents: false }).map(option => option.id)).toEqual(['', viewer.id])
  })
})

describe('bulk bar assign page', () => {
  it('shows Assign to… with numbered rows, no pill and the invite row', async () => {
    const user = userEvent.setup()
    const onAction = vi.fn()
    const issue = { id: 'issue-1', identifier: 'TST-1', title: 'One', href: '#', priority: 0, state: { id: 's', name: 'Todo', type: 'unstarted', color: '#999' }, createdAt: '', updatedAt: '' } as MyIssuesRowData
    render(<I18nProvider><MyIssuesBulkActionBar selectedIssues={[issue]} onAction={onAction} onClear={vi.fn()} actionOptions={action => action === 'assign' ? options : undefined}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Open command menu' }))
    await user.click(await screen.findByRole('option', { name: /Assign to/ }))
    const input = await screen.findByPlaceholderText('Assign to…')
    const rows = screen.getAllByRole('option')
    expect(rows.map(row => row.querySelector('kbd')?.textContent ?? '')).toEqual(['0', '1', '2', '3', ''])
    expect(screen.getByText('New user')).toBeInTheDocument()
    expect(document.querySelector('[data-agent-badge]')).toBeNull()
    await user.click(input)
    await user.keyboard('3')
    expect(onAction).toHaveBeenCalledWith('assign', [issue], flow.id)
  })
})

describe('issue context menu Assignee submenu', () => {
  it('numbers the rows, drops the pill and lists the invite action under New user', async () => {
    const user = userEvent.setup()
    const onPropertyChange = vi.fn()
    const issue = { id: 'issue-1', identifier: 'TST-1', title: 'One', href: '#', priority: 0, state: { id: 's', name: 'Todo', type: 'unstarted', color: '#999' }, createdAt: '', updatedAt: '' } as MyIssuesRowData
    const empty = { status: [], priority: [], assignee: options, dueDate: [], labels: [], project: [] }
    render(<I18nProvider><ContextMenu.Root><ContextMenu.Trigger><span>row</span></ContextMenu.Trigger><IssueContextMenu editable issue={issue} options={empty} onPropertyChange={onPropertyChange}/></ContextMenu.Root></I18nProvider>)
    fireEvent.contextMenu(screen.getByText('row'))
    const trigger = await screen.findByRole('menuitem', { name: /Assignee/ })
    trigger.focus()
    await user.keyboard('{ArrowRight}')
    const items = await screen.findAllByRole('menuitem', { name: /Teammate|Flow|Viewer|No assignee|Invite and assign/ })
    expect(items.map(item => item.querySelector('kbd')?.textContent ?? '')).toEqual(['0', '1', '2', '3', ''])
    expect(screen.getByText('New user')).toBeInTheDocument()
    expect(document.querySelector('[data-agent-badge]')).toBeNull()
    await user.click(items[3])
    expect(onPropertyChange).toHaveBeenCalledWith('assignee', flow.id)
  })
})

describe('dropdown assignee list (older bulk bar)', () => {
  it('numbers the rows, selects by number key and lists the invite action', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn(), onInvite = vi.fn()
    render(<I18nProvider><DropdownMenu.Root open><DropdownMenu.Trigger>menu</DropdownMenu.Trigger><DropdownMenu.Content><PeopleMenuItems assignee emptyLabel="No assignee" users={[viewer, teammate, flow]} onSelect={onSelect} onInvite={onInvite}/></DropdownMenu.Content></DropdownMenu.Root></I18nProvider>)
    const search = await screen.findByRole('textbox', { name: 'Assign to…' })
    await waitFor(() => expect(search).toHaveFocus())
    const items = screen.getAllByRole('menuitem')
    expect(items.map(item => item.querySelector('kbd')?.textContent ?? '')).toEqual(['0', '1', '2', '3', ''])
    expect(screen.getByText('New user')).toBeInTheDocument()
    expect(document.querySelector('[data-agent-badge]')).toBeNull()
    await user.keyboard('3')
    expect(onSelect).toHaveBeenCalledWith(flow.id)
    await user.click(screen.getByRole('menuitem', { name: 'Invite and assign…' }))
    expect(onInvite).toHaveBeenCalledTimes(1)
  })
})

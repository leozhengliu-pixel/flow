import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { CLEAR_ASSIGNEE_AND_AGENT_ID, NO_AGENT_ID } from '@/lib/agent-members'
import { IssueAssigneeSlot } from './issue-assignee-slot'

const confirmAction = vi.hoisted(() => vi.fn<(title: string, options?: Record<string, unknown>) => Promise<boolean>>())
vi.mock('@/components/ui/action-dialog-service', async importOriginal => ({ ...await importOriginal<typeof import('@/components/ui/action-dialog-service')>(), confirmAction }))
vi.mock('@/lib/invite-navigation', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/invite-navigation')>(), viewerCanInvite: () => true }))

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

beforeEach(() => { localStorage.setItem('flow:locale', 'en-US'); confirmAction.mockReset() })

const member = { id: 'usr_1', name: 'Skyler Anderson' }
const agent = { id: 'app_1', name: 'Flow QA agent' }
const other = { id: 'app_2', label: 'Other agent', kind: 'assignee' as const, agent: true }
const options = [
  { id: '', label: 'No assignee', kind: 'assignee' as const },
  { id: 'usr_1', label: 'Skyler Anderson', kind: 'assignee' as const },
  { id: 'app_1', label: 'Flow QA agent', kind: 'assignee' as const, agent: true },
]

function slot(issue: { assignee?: typeof member; delegate?: typeof agent }) {
  return render(<I18nProvider><IssueAssigneeSlot issue={issue} options={options} onChange={vi.fn()} avatarClassName="avatar" emptyClassName="empty"/></I18nProvider>).container
}

describe('IssueAssigneeSlot', () => {
  it('stacks the delegated agent behind the assignee', () => {
    const cluster = slot({ assignee: member, delegate: agent }).querySelector('.issue-assignee-cluster')
    expect(cluster?.getAttribute('data-delegated')).toBe('')
    expect(cluster?.children[0].className).toContain('issue-assignee-cluster__agent')
    expect(cluster?.children[1].className).toContain('issue-assignee-cluster__member')
  })

  it('shows the empty-assignee glyph beside an agent with no human, and on rows with neither', () => {
    const delegated = slot({ delegate: agent })
    expect(delegated.querySelector('.issue-assignee-cluster__empty')).not.toBeNull()
    expect(delegated.querySelector('.issue-assignee-cluster__agent')).not.toBeNull()
    const bare = slot({})
    expect(bare.querySelector('.issue-assignee-cluster[data-delegated]')).toBeNull()
    expect(bare.querySelector('.issue-assignee-cluster__empty')).not.toBeNull()
  })

  it('opens the same assignee menu as the issue sidebar: the agent hangs under the assignee with its pill', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><IssueAssigneeSlot issue={{ assignee: member, delegate: agent }} options={[...options, other]} onChange={onChange} avatarClassName="avatar"/></I18nProvider>)
    await user.click(screen.getByRole('combobox'))
    const rows = within(await screen.findByRole('listbox', { name: 'Assignee' })).getAllByRole('option')
    expect(rows.map(row => (row.querySelector('.core-delegate-row-name') ?? row.querySelector('.property-command-label'))?.textContent)).toEqual(['No assignee', 'Skyler Anderson', 'Flow QA agent', 'Invite and assign…'])
    expect(rows.map(row => row.getAttribute('aria-checked'))).toEqual(['false', 'true', 'false', 'false'])
    expect(rows[2].querySelector('.core-nested-delegate-icon')).not.toBeNull()
    expect(rows[2].querySelector('.core-delegate-row-pill')).toHaveTextContent('Agent')
    await user.click(rows[2])
    const submenu = await screen.findByRole('listbox', { name: 'Agent' })
    expect(within(submenu).getAllByRole('option').map(row => row.textContent)).toEqual(['No agent', expect.stringContaining('Flow QA agent'), expect.stringContaining('Other agent')])
    await user.click(within(submenu).getByRole('option', { name: /Other agent/ }))
    expect(onChange).toHaveBeenLastCalledWith(other.id)
    await user.click(screen.getByRole('combobox'))
    await user.click(await screen.findByRole('option', { name: /^Flow QA agent/ }))
    await user.click(within(await screen.findByRole('listbox', { name: 'Agent' })).getByRole('option', { name: /No agent/ }))
    expect(onChange).toHaveBeenLastCalledWith(NO_AGENT_ID)
  })

  it('"No assignee" then "Remove agent" clears both in one update; "Keep agent" only unassigns', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><IssueAssigneeSlot issue={{ assignee: member, delegate: agent }} options={options} onChange={onChange} avatarClassName="avatar"/></I18nProvider>)
    confirmAction.mockResolvedValueOnce(true)
    await user.click(screen.getByRole('combobox'))
    await user.click(await screen.findByRole('option', { name: /No assignee/ }))
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(CLEAR_ASSIGNEE_AND_AGENT_ID))
    confirmAction.mockResolvedValueOnce(false)
    await user.click(screen.getByRole('combobox'))
    await user.click(await screen.findByRole('option', { name: /No assignee/ }))
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(''))
  })

  it('lists agents flat under "Other agents" with no curve when the issue is not delegated', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><IssueAssigneeSlot issue={{ assignee: member }} options={options} onChange={vi.fn()} avatarClassName="avatar"/></I18nProvider>)
    await user.click(screen.getByRole('combobox'))
    const listbox = await screen.findByRole('listbox', { name: 'Assignee' })
    expect(within(listbox).getAllByRole('option').map(row => row.querySelector('.property-command-label')?.textContent)).toEqual(['No assignee', 'Skyler Anderson', 'Flow QA agent', 'Invite and assign…'])
    expect(screen.getByText('Other agents')).toBeInTheDocument()
    expect(listbox.querySelector('[data-nesting-line], .core-delegate-row-pill')).toBeNull()
  })

  it('labels the trigger by who is assigned and delegated', () => {
    expect(slot({ assignee: member }).querySelector('button')?.getAttribute('aria-label')).toBe('Assign to. Current assignee is Skyler Anderson')
    expect(slot({ delegate: agent }).querySelector('button')?.getAttribute('aria-label')).toBe('Change assignee. Flow QA agent is delegated')
    expect(slot({}).querySelector('button')?.getAttribute('aria-label')).toBe('Assign issue')
  })
})

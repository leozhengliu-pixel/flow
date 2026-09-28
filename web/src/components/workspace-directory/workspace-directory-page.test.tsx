import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, User } from '@/types/flow'
import { WorkspaceDirectoryPage } from './workspace-directory-page'
import { memberDirectoryStorageKey } from './use-member-directory-preferences'

const agent = { id: 'app-1', name: 'flow-agent', displayName: 'Flow Agent', email: 'agent@apps.example.test', active: true, app: true } as User

function membersData(overrides: Partial<BootstrapData> = {}) {
  return makeBootstrap({
    viewerRole: 'admin',
    users: [viewer, teammate, agent],
    members: [
      { user: viewer, role: 'owner', status: 'active', joinedAt: '2026-01-02T10:00:00.000Z', lastSeenAt: '2026-09-01T10:00:00.000Z' },
      { user: teammate, role: 'member', status: 'active', joinedAt: '2026-02-03T10:00:00.000Z', lastSeenAt: '2026-03-04T10:00:00.000Z' },
      { user: agent, role: 'app', status: 'active', joinedAt: '2026-03-05T10:00:00.000Z', lastSeenAt: '2026-04-06T10:00:00.000Z' },
    ],
    invitations: [],
    teamMembers: [{ teamId: 'team-1', userId: viewer.id, role: 'owner', joinedAt: '2026-01-02T10:00:00.000Z' }],
    customers: [], customerRequests: [], favorites: [],
    ...overrides,
  } as Partial<BootstrapData>)
}

function renderMembers(data = membersData()) {
  const noop = vi.fn()
  return render(<I18nProvider><MemoryRouter><WorkspaceDirectoryPage
    kind="members" data={data} onOpenSidebar={noop} onNavigateTeamMembers={noop} onNavigateMember={noop} onNavigateTeam={noop}
    onNavigateTeamProjects={noop} onNavigateTeamCycles={noop} onNavigateTeamsSettings={noop} onNewTeam={noop}
    onCreateCustomer={vi.fn()} onUpdateCustomer={vi.fn()} onDeleteCustomer={vi.fn()} onOpenCustomer={noop} onReload={vi.fn().mockResolvedValue(undefined)}
  /></MemoryRouter></I18nProvider>)
}

const rowFor = (name: string) => screen.getByText(name, { selector: 'strong' }).closest('.workspace-directory-member-row') as HTMLElement

beforeEach(() => localStorage.clear())

describe('workspace members directory', () => {
  it('shows a plain Members title and lists applications inline with members', () => {
    renderMembers()
    const header = screen.getByRole('banner')
    expect(within(header).getByRole('heading', { name: 'Members' })).toBeInTheDocument()
    expect(header.querySelector('small')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Applications' })).toBeNull()
    expect(within(header).getByRole('button', { name: 'Invite members' })).toHaveClass('is-ghost')

    const app = rowFor('Flow Agent')
    expect(app).toHaveTextContent('flow-agent')
    expect(within(app).getByText('Application')).toBeInTheDocument()
    expect(within(app).getByTitle('Last seen Apr 6, 2026')).toHaveTextContent('Apr 6')

    const owner = rowFor('Viewer')
    expect(within(owner).getByText('Owner')).toHaveClass('workspace-member-role')
    expect(within(owner).getByText('Online')).toBeInTheDocument()
    expect(within(owner).getByRole('button', { name: 'Open team Test team' })).toHaveTextContent('TST')
    expect(owner.querySelector('time[title^="Joined Jan 2, "]')).toHaveTextContent('Jan 2')
    expect(within(rowFor('Teammate')).getByText('Member')).not.toHaveClass('workspace-member-role')
  })

  it('persists display options that reorder rows and hide columns', async () => {
    const user = userEvent.setup()
    const first = renderMembers()
    const columns = () => document.querySelector('.workspace-members-columns') as HTMLElement
    expect(within(columns()).getByRole('button', { name: 'Order by Name, sorted ascending' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Display options' }))
    await user.click(screen.getByRole('button', { name: 'Choose ordering' }))
    await user.click(screen.getByRole('menuitem', { name: 'Joined' }))
    await user.click(screen.getByRole('button', { name: 'Teams' }))
    expect(screen.getByRole('button', { name: 'Teams' })).toHaveAttribute('aria-pressed', 'false')
    expect(within(columns()).getByRole('button', { name: 'Order by Joined, sorted ascending' })).toBeInTheDocument()
    expect(within(columns()).queryByText('Teams')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Open team Test team' })).toBeNull()
    expect([...document.querySelectorAll('.workspace-member-identity strong')].map(node => node.textContent)).toEqual(['Viewer', 'Teammate', 'Flow Agent'])
    expect(JSON.parse(localStorage.getItem(memberDirectoryStorageKey('workspace-1', viewer.id))!)).toEqual({ ordering: 'joined', descending: false, columns: ['status', 'joined'] })

    first.unmount()
    renderMembers()
    expect(within(columns()).getByRole('button', { name: 'Order by Joined, sorted ascending' })).toBeInTheDocument()
    await user.click(within(columns()).getByRole('button', { name: 'Order by Joined, sorted ascending' }))
    expect([...document.querySelectorAll('.workspace-member-identity strong')].map(node => node.textContent)).toEqual(['Flow Agent', 'Teammate', 'Viewer'])
  })

  it('filters by status from the Status submenu with per-status counts', async () => {
    const user = userEvent.setup()
    renderMembers()
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    expect(screen.getByRole('menuitem', { name: 'Advanced filter' })).toBeInTheDocument()
    await user.hover(screen.getByRole('menuitem', { name: 'Status' }))
    const admin = await screen.findByRole('menuitemcheckbox', { name: /Admin/ })
    expect(admin).toHaveTextContent('1 member')
    expect(screen.getByRole('menuitemcheckbox', { name: /Member/ })).toHaveTextContent('1 member')
    expect(screen.getByRole('menuitemcheckbox', { name: /Guest/ })).not.toHaveTextContent(/member/)
    // jsdom has no submenu geometry for Radix's pointer grace polygon, so choose with the keyboard.
    admin.focus()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(document.querySelectorAll('.workspace-directory-member-row')).toHaveLength(1))
    expect(rowFor('Viewer')).toBeInTheDocument()
  })

  it('opens the shared invite dialog from Invite members', async () => {
    const user = userEvent.setup()
    renderMembers()
    await user.click(screen.getByRole('button', { name: 'Invite members' }))
    expect(await screen.findByRole('dialog', { name: 'Invite to your workspace' })).toBeInTheDocument()
  })
})

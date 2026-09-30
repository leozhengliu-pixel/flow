import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, User } from '@/types/flow'
import { WorkspaceDirectoryPage } from './workspace-directory-page'
import { memberDirectoryStorageKey } from './use-member-directory-preferences'

const agent = { id: 'app-1', name: 'flow-agent', displayName: 'Flow Agent', username: 'flow-agent', email: 'agent@apps.example.test', active: true, app: true } as User

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

  it('shows full name over username and Linear status badges', () => {
    const skyler = { id: 'user-3', name: 'Skyler Anderson', displayName: 'Skyler Anderson', username: 'bcgroupdev', email: 'bcgroupdev@gmail.com', active: true } as User
    const guest = { id: 'user-4', name: 'Gia Guest', displayName: 'Gia Guest', email: 'Gia.Guest@example.test', active: true } as User
    const gone = { id: 'user-5', name: 'Sam Suspended', displayName: 'Sam Suspended', email: 'sam@example.test', active: true } as User
    renderMembers(membersData({
      users: [viewer, teammate, agent, skyler, guest, gone],
      members: [
        { user: viewer, role: 'owner', status: 'active', joinedAt: '2026-01-02T10:00:00.000Z' },
        { user: teammate, role: 'admin', status: 'active', joinedAt: '2026-01-02T10:00:00.000Z' },
        { user: agent, role: 'app', status: 'active', joinedAt: '2026-01-02T10:00:00.000Z' },
        { user: skyler, role: 'member', status: 'active', joinedAt: '2026-01-02T10:00:00.000Z' },
        { user: guest, role: 'guest', status: 'active', joinedAt: '2026-01-02T10:00:00.000Z' },
        { user: gone, role: 'member', status: 'suspended', joinedAt: '2026-01-02T10:00:00.000Z' },
      ],
      invitations: [{ id: 'invite-1', workspaceId: 'workspace-1', email: 'new@example.test', role: 'member', teamIds: [], status: 'pending', inviterId: viewer.id, expiresAt: '', createdAt: '2026-01-02T10:00:00.000Z' }],
    } as Partial<BootstrapData>))
    const identity = (name: string) => rowFor(name).querySelector('.workspace-member-identity small')
    expect(identity('Skyler Anderson')).toHaveTextContent(/^bcgroupdev$/)
    expect(identity('Gia Guest')).toHaveTextContent(/^gia.guest$/)
    const badge = (name: string, label: string) => within(rowFor(name)).getByText(label)
    expect(badge('Viewer', 'Owner')).toHaveClass('workspace-member-role')
    expect(badge('Teammate', 'Admin')).toHaveClass('workspace-member-role')
    expect(badge('Flow Agent', 'Application')).toHaveClass('workspace-member-role')
    expect(badge('Gia Guest', 'Guest')).toHaveClass('workspace-member-role')
    expect(within(rowFor('Sam Suspended')).getAllByText('Suspended')[0]).toHaveClass('workspace-member-role')
    expect(within(rowFor('new@example.test')).getByText('Invited')).toHaveClass('workspace-member-role')
    expect(within(rowFor('new@example.test')).getAllByText('new@example.test')).toHaveLength(1)
    expect(within(rowFor('Skyler Anderson')).getByText('Member')).not.toHaveClass('workspace-member-role')
  })

  it('shows team keys with a +N chip and sizes the Teams column to the longest key', () => {
    const teams = [
      { ...makeBootstrap().teams[0], id: 'team-1', key: 'DESIGN', name: 'Design' },
      { ...makeBootstrap().teams[0], id: 'team-2', key: 'FLO', name: 'Flow' },
      { ...makeBootstrap().teams[0], id: 'team-3', key: 'OPS', name: 'Operations' },
    ]
    renderMembers(membersData({
      teams,
      teamMembers: teams.map(team => ({ teamId: team.id, userId: viewer.id, role: 'member' as const, joinedAt: '' })),
    } as Partial<BootstrapData>))
    const teamsCell = rowFor('Viewer').querySelector('.workspace-member-teams') as HTMLElement
    expect(within(teamsCell).getByRole('button', { name: 'Open team Design' })).toHaveTextContent(/^DESIGN$/)
    const more = within(teamsCell).getByText('+2')
    expect(more).toHaveClass('workspace-member-teams__more')
    expect(more).toHaveAttribute('title', 'DESIGN · Design, FLO · Flow, OPS · Operations')
    const table = document.querySelector('.workspace-members-table') as HTMLElement
    expect(table.style.getPropertyValue('--member-columns')).toContain('max(71px, calc(6ch + 76px))')
  })

  it('keeps the Teams column at Linear\'s 71px when nobody needs a +N chip', () => {
    const teams = [{ ...makeBootstrap().teams[0], id: 'team-1', key: 'OPS', name: 'Operations' }]
    renderMembers(membersData({
      teams,
      teamMembers: [{ teamId: 'team-1', userId: viewer.id, role: 'member' as const, joinedAt: '' }],
    } as Partial<BootstrapData>))
    const table = document.querySelector('.workspace-members-table') as HTMLElement
    expect(table.style.getPropertyValue('--member-columns')).toContain('max(71px, calc(3ch + 34px))')
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

describe('workspace teams directory', () => {
  const teamsData = () => {
    const base = makeBootstrap()
    const teams = [
      { ...base.teams[0], id: 'team-1', key: 'ENG', name: 'Engineering' },
      { ...base.teams[0], id: 'team-2', key: 'DES', name: 'Design' },
    ]
    return makeBootstrap({
      viewerRole: 'admin',
      teams,
      teamMembers: [{ teamId: 'team-1', userId: viewer.id, role: 'owner', joinedAt: '2026-01-02T10:00:00.000Z' }],
      teamSettings: { 'team-1': { teamId: 'team-1', description: 'Builds the product' } },
      customers: [], customerRequests: [], favorites: [], invitations: [], members: [],
    } as unknown as Partial<BootstrapData>)
  }
  const renderTeams = (data = teamsData()) => {
    const noop = vi.fn()
    return render(<I18nProvider><MemoryRouter><WorkspaceDirectoryPage
      kind="teams" data={data} onOpenSidebar={noop} onNavigateTeamMembers={noop} onNavigateMember={noop} onNavigateTeam={noop}
      onNavigateTeamProjects={noop} onNavigateTeamCycles={noop} onNavigateTeamsSettings={noop} onNewTeam={noop}
      onCreateCustomer={vi.fn()} onUpdateCustomer={vi.fn()} onDeleteCustomer={vi.fn()} onOpenCustomer={noop} onReload={vi.fn().mockResolvedValue(undefined)}
    /></MemoryRouter></I18nProvider>)
  }

  it('uses Linear\'s header, search and default columns', () => {
    renderTeams()
    expect(screen.getByRole('button', { name: 'New team' })).toHaveClass('is-ghost')
    expect(screen.getByRole('searchbox', { name: 'Find teams' })).toHaveAttribute('placeholder', 'Find teams…')
    expect(screen.queryByText(/^\d+ teams?$/)).not.toBeInTheDocument()
    const header = document.querySelector('.workspace-team-columns') as HTMLElement
    expect(within(header).getByRole('button', { name: 'Order by Name' })).toBeInTheDocument()
    expect([...header.querySelectorAll('span')].map(span => span.textContent).filter(Boolean)).toEqual(['Description', 'Membership', 'Members', 'Active projects'])
    expect(screen.getByText('Builds the product')).toHaveClass('workspace-team-description')
    expect(screen.getByText('ENG')).toBeInTheDocument()
  })

  it('filters teams by name or key from the search field', async () => {
    const user = userEvent.setup()
    renderTeams()
    await user.type(screen.getByRole('searchbox', { name: 'Find teams' }), 'des')
    expect(screen.getByText('Design', { selector: 'strong' })).toBeInTheDocument()
    expect(screen.queryByText('Engineering', { selector: 'strong' })).not.toBeInTheDocument()
    await user.clear(screen.getByRole('searchbox', { name: 'Find teams' }))
    await user.type(screen.getByRole('searchbox', { name: 'Find teams' }), 'eng')
    expect(screen.getByText('Engineering', { selector: 'strong' })).toBeInTheDocument()
    expect(screen.queryByText('Design', { selector: 'strong' })).not.toBeInTheDocument()
  })

  it('migrates saved v1 column preferences to show the ID and drop the Cycle default', () => {
    localStorage.setItem('flow:team-directory:workspace-1:' + viewer.id, JSON.stringify({ ordering: 'name', descending: false, columns: ['membership', 'members', 'cycle', 'projects'] }))
    renderTeams()
    const header = document.querySelector('.workspace-team-columns') as HTMLElement
    expect(within(header).queryByText('Cycle')).not.toBeInTheDocument()
    expect(screen.getByText('ENG')).toBeInTheDocument()
  })
})

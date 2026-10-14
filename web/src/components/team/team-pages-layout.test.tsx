import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ActiveTeamProvider } from '@/lib/active-team'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { BootstrapData, TeamMember, WorkspaceRole } from '@/types/flow'

import { TeamPagesLayout } from './team-pages-layout'

const secret = { id: 'team-1', key: 'SEC', name: 'Secret', color: '#111', private: true }

function privateTeamData({ role = 'admin' as WorkspaceRole, members = [] as TeamMember[] } = {}): BootstrapData {
  return makeBootstrap({
    teams: [secret],
    teamMembers: members,
    teamSettings: { 'team-1': { teamId: 'team-1', access: 'private' } as never },
    viewer,
    viewerRole: role,
  })
}

function renderLayout(data: BootstrapData, onNavigate = vi.fn()) {
  render(
    <I18nProvider>
      <ActiveTeamProvider>
        <TeamPagesLayout data={data} teamKey="SEC" onNavigate={onNavigate}>
          <div data-testid="children">visible</div>
        </TeamPagesLayout>
      </ActiveTeamProvider>
    </I18nProvider>,
  )
  return onNavigate
}

afterEach(() => localStorage.clear())

describe('TeamPagesLayout', () => {
  it('shows the reference app’s private team page to a workspace admin who is not a member', async () => {
    const onNavigate = renderLayout(privateTeamData())
    const empty = screen.getByTestId('private-team-empty')
    expect(screen.queryByTestId('children')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Private team' })).toBeInTheDocument()
    // The admin line says what the role grants (settings), not content access.
    expect(empty.querySelector('p')?.textContent).toBe(
      'This is a private team and you are not a team member.As an admin you have access to the team’s settings.',
    )
    const link = screen.getByRole('link', { name: 'team’s settings' })
    expect(link).toHaveAttribute('href', `/${makeBootstrap().workspace.urlKey}/settings/teams/SEC`)
    await userEvent.click(link)
    expect(onNavigate).toHaveBeenCalledWith(`/${makeBootstrap().workspace.urlKey}/settings/teams/SEC`)
    expect(empty.querySelector('svg')).toHaveAttribute('width', '92')
    // No Join or membership button: admins add themselves from the team's settings.
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('treats a workspace owner like an admin', () => {
    renderLayout(privateTeamData({ role: 'owner' }))
    expect(screen.getByRole('link', { name: 'team’s settings' })).toBeInTheDocument()
  })

  it('leaves the admin line out for viewers without the admin role', () => {
    renderLayout(privateTeamData({ role: 'member' }))
    expect(screen.getByText('This is a private team and you are not a team member.')).toBeInTheDocument()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })

  it('renders children for members, admins included', () => {
    renderLayout(privateTeamData({ members: [{ teamId: 'team-1', userId: viewer.id, role: 'member', joinedAt: '2026-01-01' }] }))
    expect(screen.getByTestId('children')).toBeInTheDocument()
    expect(screen.queryByTestId('private-team-empty')).not.toBeInTheDocument()
  })

  it('gates a public sub-team inside a private team the admin has not joined', () => {
    const data = makeBootstrap({
      teams: [secret, { id: 'team-2', key: 'SUB', name: 'Sub', color: '#222' }],
      teamMembers: [],
      teamSettings: {
        'team-1': { teamId: 'team-1', access: 'private' } as never,
        'team-2': { teamId: 'team-2', access: 'public', parentTeamId: 'team-1' } as never,
      },
      viewer,
      viewerRole: 'admin',
    })
    render(
      <I18nProvider>
        <ActiveTeamProvider>
          <TeamPagesLayout data={data} teamKey="SUB" onNavigate={vi.fn()}>
            <div data-testid="children">visible</div>
          </TeamPagesLayout>
        </ActiveTeamProvider>
      </I18nProvider>,
    )
    expect(screen.getByTestId('private-team-empty')).toBeInTheDocument()
  })

  it('is localized in zh-CN', () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    renderLayout(privateTeamData())
    expect(screen.getByRole('heading', { name: '私有团队' })).toBeInTheDocument()
    expect(screen.getByTestId('private-team-empty').querySelector('p')?.textContent).toBe(
      '这是一个私有团队，你不是团队成员。作为管理员，你可以访问团队设置。',
    )
  })
})

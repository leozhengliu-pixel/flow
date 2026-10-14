import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'

import { WorkspaceTeamsSettings } from './workspace-teams-settings'

const teams = [
  { id: 'public', key: 'PUB', name: 'Public', color: '#111' },
  { id: 'private', key: 'QPV', name: 'QA private vis', color: '#222', private: true },
]

function renderTeams(joinedPrivate: boolean) {
  const data = makeBootstrap({
    teams,
    teamSettings: {
      public: { teamId: 'public', access: 'public' } as never,
      private: { teamId: 'private', access: 'private' } as never,
    },
    teamMembers: joinedPrivate ? [{ teamId: 'private', userId: viewer.id, role: 'member', joinedAt: '2026-01-01' }] : [],
    issues: [],
    viewer,
    viewerRole: 'admin',
  })
  render(
    <MemoryRouter>
      <I18nProvider>
        <WorkspaceTeamsSettings data={data} onCreate={vi.fn()} onOpen={vi.fn()} onReload={vi.fn()} />
      </I18nProvider>
    </MemoryRouter>,
  )
}

describe('WorkspaceTeamsSettings private teams', () => {
  it('lists a private team to an admin outside it with settings and delete, but no way into its issues', async () => {
    const user = userEvent.setup()
    renderTeams(false)
    expect(screen.getByText('QA private vis')).toBeInTheDocument()
    expect(screen.getByText('Private')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Open menu QA private vis' }))
    expect(screen.getByRole('menuitem', { name: 'Team settings' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Delete team…' })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: 'Go to all issues' })).not.toBeInTheDocument()
  })

  it('keeps the issues shortcut for teams the admin can read', async () => {
    const user = userEvent.setup()
    renderTeams(true)
    await user.click(screen.getByRole('button', { name: 'Open menu QA private vis' }))
    expect(screen.getByRole('menuitem', { name: 'Go to all issues' })).toBeInTheDocument()
  })
})

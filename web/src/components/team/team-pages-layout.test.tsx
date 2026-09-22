import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { ActiveTeamProvider } from '@/lib/active-team'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'

import { TeamPagesLayout } from './team-pages-layout'

describe('TeamPagesLayout', () => {
  it('shows private empty when viewer is not a member', () => {
    const data = makeBootstrap({
      teams: [{ id: 'team-1', key: 'SEC', name: 'Secret', color: '#111', private: true }],
      teamMembers: [],
      teamSettings: { 'team-1': { teamId: 'team-1', access: 'private' } as never },
      viewer,
      viewerRole: 'admin',
    })
    render(
      <I18nProvider>
        <ActiveTeamProvider>
          <TeamPagesLayout data={data} teamKey="SEC" onNavigate={vi.fn()}>
            <div data-testid="children">visible</div>
          </TeamPagesLayout>
        </ActiveTeamProvider>
      </I18nProvider>,
    )
    expect(screen.getByTestId('private-team-empty')).toBeInTheDocument()
    expect(screen.queryByTestId('children')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Private team' })).toBeInTheDocument()
  })

  it('renders children when viewer is a member of a private team', () => {
    const data = makeBootstrap({
      teams: [{ id: 'team-1', key: 'SEC', name: 'Secret', color: '#111', private: true }],
      teamMembers: [{ teamId: 'team-1', userId: viewer.id, role: 'member', joinedAt: '2026-01-01' }],
      teamSettings: { 'team-1': { teamId: 'team-1', access: 'private' } as never },
      viewer,
    })
    render(
      <I18nProvider>
        <ActiveTeamProvider>
          <TeamPagesLayout data={data} teamKey="SEC" onNavigate={vi.fn()}>
            <div data-testid="children">visible</div>
          </TeamPagesLayout>
        </ActiveTeamProvider>
      </I18nProvider>,
    )
    expect(screen.getByTestId('children')).toBeInTheDocument()
    expect(screen.queryByTestId('private-team-empty')).not.toBeInTheDocument()
  })
})

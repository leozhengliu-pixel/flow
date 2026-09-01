import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { BootstrapData } from '@/types/flow'

const api = vi.hoisted(() => ({
  fetchTeamResources: vi.fn(),
  setTeamMembership: vi.fn(),
}))

vi.mock('@/lib/api', async importOriginal => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  ...api,
}))

import { TeamOverviewPage } from './team-overview-page'

function renderOverview() {
  const data = makeBootstrap({
    documents: [],
    favorites: [],
    subscriptions: [],
    teamMembers: [{ teamId: 'team-1', userId: viewer.id, role: 'owner', joinedAt: '2026-08-01T00:00:00Z' }],
    teamSettings: {
      'team-1': { teamId: 'team-1', description: '' } as unknown as BootstrapData['teamSettings'][string],
    },
  })
  return render(
    <I18nProvider>
      <TeamOverviewPage
        data={data}
        onNavigate={vi.fn()}
        onOpenSidebar={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
        team={data.teams[0]}
        view="overview"
      />
    </I18nProvider>,
  )
}

describe('team overview', () => {
  beforeEach(() => {
    api.fetchTeamResources.mockReset().mockResolvedValue({
      resources: [],
      sections: [],
    })
    api.setTeamMembership.mockReset().mockResolvedValue(undefined)
  })

  it('renders the measured team header, tabs, resources, members, and shortcuts', async () => {
    renderOverview()
    await waitFor(() => expect(api.fetchTeamResources).toHaveBeenCalledWith('team-1'))

    expect(screen.getByRole('heading', { level: 2, name: 'Test team' })).toBeVisible()
    expect(screen.getByRole('navigation', { name: 'Team views' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('button', { name: 'Choose team icon' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Team resources' })).toBeVisible()
    expect(screen.getByText('Add documents and links. Organize by creating sections.')).toBeVisible()
    expect(screen.getByText('Go to')).toBeVisible()
    expect(screen.getByRole('link', { name: 'Team settings' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Cycles' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Views' })).toBeVisible()
  })

  it('uses the Linear resource menu and creates sections inline', async () => {
    const user = userEvent.setup()
    renderOverview()

    await user.click(screen.getByRole('button', { name: 'Add resources' }))
    expect(screen.getByRole('menuitem', { name: 'New document' })).toBeVisible()
    expect(screen.getByRole('menuitem', { name: 'Existing documents' })).toBeVisible()
    expect(screen.getByRole('menuitem', { name: 'New link…' })).toBeVisible()
    await user.keyboard('{Escape}')

    await user.click(screen.getByRole('button', { name: 'Add section' }))
    expect(screen.getByRole('textbox', { name: 'Section name' })).toBeVisible()
    expect(screen.queryByRole('dialog', { name: 'Add section' })).not.toBeInTheDocument()
  })

  it('opens the member picker and persists selected workspace members', async () => {
    const user = userEvent.setup()
    renderOverview()

    await user.click(screen.getByRole('button', { name: 'Add members' }))
    expect(screen.getByRole('dialog', { name: 'Add members to Test team' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Select members' }))
    await user.click(screen.getByRole('menuitemcheckbox', { name: 'Teammate' }))
    await user.keyboard('{Escape}')
    await user.click(screen.getByRole('button', { name: 'Add members' }))

    await waitFor(() =>
      expect(api.setTeamMembership).toHaveBeenCalledWith(
        'workspace',
        'team-1',
        'user-2',
        true,
      ),
    )
  })
})

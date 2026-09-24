import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nProvider } from '@/i18n/i18n'
import type { BootstrapData, Team } from '@/types/flow'
import {
  getProjectStatusInheritanceConflicts,
  projectStatusInheritanceSource,
  TeamProjectStatusesSettingsPage,
} from './team-project-statuses-settings'

vi.mock('@/lib/api', () => ({
  updateStructuredTeamSettings: vi.fn(async () => ({})),
  reorderProjectStatuses: vi.fn(async () => ({})),
  createProjectStatus: vi.fn(async () => ({})),
  updateProjectStatus: vi.fn(async () => ({})),
  deleteProjectStatus: vi.fn(async () => ({})),
}))

function fixture(inherit = false, parentTeamId?: string): { data: BootstrapData; team: Team } {
  const team = { id: 'team-1', key: 'ENG', name: 'Engineering', color: '#5E6AD2', icon: 'Team' } as Team
  const parent = { id: 'team-0', key: 'PLT', name: 'Platform', color: '#5E6AD2', icon: 'Team' } as Team
  const data = {
    workspace: { urlKey: 'acme' },
    teams: parentTeamId ? [parent, team] : [team],
    teamSettings: {
      'team-1': {
        teamId: 'team-1',
        inheritProjectStatuses: inherit,
        parentTeamId: parentTeamId ?? '',
      },
    },
    projectStatuses: [
      { id: 's1', name: 'Backlog', color: '#ccc', type: 'backlog', position: 0 },
      { id: 's2', name: 'In Progress', color: '#5E6AD2', type: 'started', position: 1 },
    ],
    projects: [],
  } as unknown as BootstrapData
  return { data, team }
}

describe('TeamProjectStatusesSettingsPage (LS-0595)', () => {
  it('detects inheritance source from parent vs workspace', () => {
    expect(projectStatusInheritanceSource({ parentTeamId: 'p' } as never)).toBe('parent')
    expect(projectStatusInheritanceSource({ parentTeamId: '' } as never)).toBe('workspace')
    expect(getProjectStatusInheritanceConflicts({} as BootstrapData).mismatchStatusCount).toBe(0)
  })

  it('renders inherit toggle and status list', async () => {
    const user = userEvent.setup()
    const { data, team } = fixture(false)
    render(
      <I18nProvider>
        <TeamProjectStatusesSettingsPage data={data} team={team} onBack={vi.fn()} onReload={vi.fn(async () => {})} />
      </I18nProvider>,
    )
    expect(screen.getByTestId('team-project-statuses-settings')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Team project statuses' })).toBeInTheDocument()
    expect(screen.getByText('Inherit statuses from workspace')).toBeInTheDocument()
    expect(screen.getByRole('list', { name: 'Project statuses' })).toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: 'Inherit statuses from workspace' }))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByText(/Inherit project statuses from workspace/)).toBeInTheDocument()
  })

  it('shows inherited banner when inherit is on', () => {
    const { data, team } = fixture(true, 'team-0')
    render(
      <I18nProvider>
        <TeamProjectStatusesSettingsPage data={data} team={team} onBack={vi.fn()} onReload={vi.fn(async () => {})} />
      </I18nProvider>,
    )
    expect(screen.getByText('Project statuses are inherited')).toBeInTheDocument()
    expect(screen.getByText('Inherit statuses from parent team')).toBeInTheDocument()
  })
})

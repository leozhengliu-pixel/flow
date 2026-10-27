import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { BootstrapData, TeamSettings } from '@/types/flow'

const api = vi.hoisted(() => ({ updateStructuredTeamSettings: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { TeamWorkflowSettings } from './team-workflow-settings'

function mount(settings: Partial<TeamSettings>) {
  const base = makeBootstrap()
  const team = base.teams[0]
  const data = makeBootstrap({
    viewerRole: 'admin',
    teamMembers: [{ teamId: team.id, userId: viewer.id, role: 'member' }],
    emailIntakeAddresses: [],
    teamSettings: { [team.id]: { teamId: team.id, timezone: 'Etc/UTC', defaultStateId: 'state-started', estimateType: 'notUsed', ...settings } as TeamSettings },
  } as unknown as Partial<BootstrapData>)
  render(<MemoryRouter><I18nProvider><TeamWorkflowSettings data={data} team={team} section="general" onNavigate={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider></MemoryRouter>)
  return team
}

describe('team estimate settings', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    api.updateStructuredTeamSettings.mockReset()
    api.updateStructuredTeamSettings.mockImplementation(async (teamId: string, patch: Partial<TeamSettings>) => ({ teamId, timezone: 'Etc/UTC', ...patch }))
  })

  it('offers the scales with their values as a muted hint and links the docs', async () => {
    const user = userEvent.setup()
    const team = mount({ estimateType: 'notUsed' })
    expect(screen.getByText('Used to estimate issue complexity and plan cycle capacity.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Docs' })).toHaveAttribute('target', '_blank')
    expect(screen.queryByText('Allow zero estimates')).toBeNull()
    await user.click(screen.getByRole('combobox', { name: 'Issue estimation' }))
    const options = await screen.findAllByRole('option')
    expect(options.map(option => option.querySelector('.settings-select-option-hint')?.textContent ?? option.textContent)).toEqual(['Not in use', '(1, 2, 4, 8, 16 Points)', '(1, 2, 3, 5, 8 Points)', '(1, 2, 3, 4, 5 Points)', '(XS, S, M, L, XL)'])
    expect(options[2]).toHaveTextContent(/^Fibonacci/)
    expect(options[2].querySelector('.settings-select-option-hint')).toHaveTextContent('(1, 2, 3, 5, 8 Points)')
    await user.click(options[2])
    expect(api.updateStructuredTeamSettings).toHaveBeenCalledWith(team.id, { estimateType: 'fibonacci' })
  })

  it('nests the three options under a scale: zero and extended off, count unestimated on by default', async () => {
    const user = userEvent.setup()
    const team = mount({ estimateType: 'fibonacci' })
    const toggle = (name: string) => screen.getByRole('checkbox', { name })
    expect(toggle('Allow zero estimates')).not.toBeChecked()
    expect(toggle('Extended estimate scale')).not.toBeChecked()
    expect(toggle('Count unestimated issues')).toBeChecked()
    expect(screen.getByText('When enabled, issues without an estimate will count as 1 estimate point. When disabled, they count as 0 estimate points.')).toBeInTheDocument()
    const nested = toggle('Allow zero estimates').closest('.team-estimate-options')
    expect(nested && within(nested as HTMLElement).getAllByRole('checkbox')).toHaveLength(3)
    await user.click(toggle('Count unestimated issues'))
    expect(api.updateStructuredTeamSettings).toHaveBeenCalledWith(team.id, { estimateCountUnestimated: false })
  })
})

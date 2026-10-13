import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nProvider } from '@/i18n/i18n'
import { updateStructuredTeamSettings } from '@/lib/api'
import type { BootstrapData, Team } from '@/types/flow'
import { TeamAgentGuidanceSettings } from './team-agent-guidance'
import { inheritedTeamAgentGuidance } from './team-agent-guidance-model'

vi.mock('@/lib/api', () => ({
  updateStructuredTeamSettings: vi.fn(async () => ({})),
}))

const team = { id: 'team-web', key: 'WEB', name: 'Web' } as Team

function fixture({ role = 'admin', settingsPermission = 'allMembers', member = false }: { role?: string; settingsPermission?: string; member?: boolean } = {}): BootstrapData {
  return {
    viewer: { id: 'user-1' },
    viewerRole: role,
    workspace: { urlKey: 'acme' },
    workspaceSettings: { agentInstructions: 'Workspace rule' },
    teams: [{ id: 'team-eng', key: 'ENG', name: 'Engineering' }, team],
    teamMembers: member ? [{ teamId: 'team-web', userId: 'user-1', role: 'member' }] : [],
    teamSettings: {
      'team-eng': { teamId: 'team-eng', agentGuidance: 'Engineering rule', settingsPermission: 'owners' },
      'team-web': { teamId: 'team-web', parentTeamId: 'team-eng', agentGuidance: 'Web rule', settingsPermission },
    },
  } as unknown as BootstrapData
}

function renderGuidance(data: BootstrapData, onReload = vi.fn(async () => {})) {
  render(
    <I18nProvider>
      <TeamAgentGuidanceSettings data={data} team={team} onReload={onReload} />
    </I18nProvider>,
  )
  return onReload
}

afterEach(() => {
  localStorage.clear()
  vi.mocked(updateStructuredTeamSettings).mockClear()
})

describe('TeamAgentGuidanceSettings', () => {
  it('loads the team guidance and saves the trimmed edit', async () => {
    const user = userEvent.setup()
    const onReload = renderGuidance(fixture())
    const editor = screen.getByRole('textbox', { name: 'Team agent guidance' })
    expect(editor).toHaveValue('Web rule')
    expect(editor).toHaveAttribute('maxLength', '8000')
    const save = screen.getByRole('button', { name: 'Save' })
    expect(save).toBeDisabled()
    await user.clear(editor)
    await user.type(editor, '  Always end answers with a rocket  ')
    expect(save).toBeEnabled()
    await user.click(save)
    await waitFor(() => expect(updateStructuredTeamSettings).toHaveBeenCalledWith('team-web', { agentGuidance: 'Always end answers with a rocket' }))
    expect(onReload).toHaveBeenCalled()
  })

  it('shows inherited workspace and parent team guidance read-only, collapsed', async () => {
    const user = userEvent.setup()
    renderGuidance(fixture())
    expect(screen.getByText(/wins when they conflict/)).toBeInTheDocument()
    const summary = screen.getByText('Inherited guidance')
    const details = summary.closest('details')
    expect(details).not.toHaveAttribute('open')
    await user.click(summary)
    expect(details).toHaveAttribute('open')
    expect(screen.getByText('Workspace rule')).toBeVisible()
    expect(screen.getByText('Engineering rule')).toBeVisible()
    expect(inheritedTeamAgentGuidance(fixture(), 'team-web').map((item) => item.body)).toEqual(['Workspace rule', 'Engineering rule'])
  })

  it('is read-only for team members when settings are owner-managed', () => {
    renderGuidance(fixture({ role: 'member', settingsPermission: 'owners', member: true }))
    expect(screen.getByRole('textbox', { name: 'Team agent guidance' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(screen.getByText('Only admins and team owners can modify the team’s agent guidance prompts')).toBeInTheDocument()
  })

  it('lets team members edit when every member may manage settings', () => {
    renderGuidance(fixture({ role: 'member', settingsPermission: 'allMembers', member: true }))
    expect(screen.getByRole('textbox', { name: 'Team agent guidance' })).toBeEnabled()
  })

  it('is localized in zh-CN', () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    renderGuidance(fixture())
    expect(screen.getByRole('textbox', { name: '团队 Agent 指导' })).toBeInTheDocument()
    expect(screen.getByText('附加指导')).toBeInTheDocument()
    expect(screen.getByText('继承的指导')).toBeInTheDocument()
  })
})

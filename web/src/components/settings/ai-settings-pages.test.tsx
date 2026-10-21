import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { updateWorkspaceAgentGuidance, updateWorkspacePreferences } from '@/lib/api'
import { parseAppRoute, settingsPath, type SettingsPageId } from '@/lib/app-routes'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, WorkspaceSettings } from '@/types/flow'
import { FeatureSettingsPage } from './feature-settings'

vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), updateWorkspacePreferences: vi.fn(), updateWorkspaceAgentGuidance: vi.fn() }))
vi.mock('@/lib/application-agents', async original => ({
  ...await original<typeof import('@/lib/application-agents')>(),
  listApplications: vi.fn().mockResolvedValue([{ id: 'app1', clientId: 'client1', name: 'Flow QA OAuth app', active: true, allPublicTeams: true, teamIds: [], builtin: false, scopes: [] }]),
}))
vi.mock('@/lib/toast', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))
vi.mock('./coding-sessions/use-coding-settings', () => ({
  useCodingStatus: () => ({ status: { codeHosts: [{ codeHost: 'github', hostName: 'github.com', connected: true }] }, loaded: true }),
}))

const settings = { sessionDurationDays: 30, featureFlags: {}, featureSettings: {}, agentInstructions: 'Be brief.', mcpConnectorsEnabled: true } as unknown as WorkspaceSettings

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.setItem('flow:locale', 'en-US')
})

function renderPage(feature: Parameters<typeof FeatureSettingsPage>[0]['page'], overrides: Partial<BootstrapData> = {}, onNavigateSettings = vi.fn()) {
  render(
    <MemoryRouter>
      <I18nProvider>
        <FeatureSettingsPage page={feature} data={makeBootstrap({ workspaceSettings: settings, viewerRole: 'admin', integrationConnections: [], ...overrides })} onCreateReleasePipeline={vi.fn()} onOpenReleasePipeline={vi.fn()} onOpenIntegration={vi.fn()} onNavigateSettings={onNavigateSettings} onReload={vi.fn()} />
      </I18nProvider>
    </MemoryRouter>,
  )
  return onNavigateSettings
}

describe('AI & agents hub', () => {
  it('lists the reference sections in order with a link row for each feature', async () => {
    const navigate = renderPage('ai')
    expect(screen.getByRole('heading', { level: 1, name: 'AI & agents' })).toBeVisible()
    expect(screen.getAllByRole('heading', { level: 2 }).map(item => item.textContent)).toEqual(['Flow Agent', 'Flow Agent integrations', 'Installed Agents', 'AI'])
    expect(screen.getByText('$0.00 remaining')).toBeVisible()
    expect(screen.getByRole('checkbox', { name: 'Enable usage feedback' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Suggest branch name in prompts' })).toBeChecked()
    expect(await screen.findByText('Flow QA OAuth app')).toBeVisible()
    expect(screen.getByText('Access to all public teams')).toBeVisible()

    const user = userEvent.setup()
    for (const [name, page] of [
      ['Flow AgentConfigure for your workspace', 'ai-agent'],
      ['Coding sessionsAssign or ask Flow to make code changes', 'coding-sessions'],
      ['Code IntelligenceAllow Flow Agent and Loops', 'code-intelligence'],
      ['Triage IntelligenceFind related issues', 'triage-intelligence'],
      ['Installed agents guidanceProvide context and instructions for installed agents', 'installed-agents'],
      ['Resolved thread summariesControl AI summaries', 'ai-summaries'],
    ] as Array<[string, SettingsPageId]>) {
      const row = screen.getAllByRole('button').find(item => item.textContent?.replace(/\s+/g, ' ').startsWith(name))
      expect(row, name).toBeDefined()
      await user.click(row as HTMLElement)
      expect(navigate).toHaveBeenLastCalledWith(page)
    }
  })

  it('saves the usage feedback and branch name switches as feature flags', async () => {
    vi.mocked(updateWorkspacePreferences).mockResolvedValue(settings)
    renderPage('ai')
    const user = userEvent.setup()
    await user.click(screen.getByRole('checkbox', { name: 'Enable usage feedback' }))
    await waitFor(() => expect(updateWorkspacePreferences).toHaveBeenCalledWith({ featureFlags: { 'usage-feedback': false } }, 'workspace'))
    await user.click(screen.getByRole('checkbox', { name: 'Suggest branch name in prompts' }))
    await waitFor(() => expect(updateWorkspacePreferences).toHaveBeenLastCalledWith({ featureFlags: { 'suggest-branch-name': false } }, 'workspace'))
  })

  it('keeps the switches read-only for members', () => {
    renderPage('ai', { viewerRole: 'member' })
    expect(screen.getByRole('checkbox', { name: 'Enable usage feedback' })).toBeDisabled()
  })
})

describe('Flow Agent page', () => {
  it('shows the agent, web access and MCP connector controls and saves them', async () => {
    vi.mocked(updateWorkspacePreferences).mockResolvedValue(settings)
    const navigate = renderPage('ai-agent')
    expect(screen.getByRole('heading', { level: 1, name: 'Flow Agent' })).toBeVisible()
    expect(screen.getAllByRole('heading', { level: 2 }).map(item => item.textContent)).toEqual(['MCP connectors', 'Workspace guidance'])
    expect(screen.getByRole('checkbox', { name: 'Enable Flow Agent' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Enable web access' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Enable MCP connectors' })).toBeChecked()
    const user = userEvent.setup()
    await user.click(screen.getByRole('checkbox', { name: 'Enable web access' }))
    await waitFor(() => expect(updateWorkspacePreferences).toHaveBeenCalledWith({ agentWebAccess: false, agentWebSearch: false, agentWebSearchDisabled: true }, 'workspace'))
    await user.click(screen.getByRole('button', { name: /^Manage/ }))
    expect(navigate).toHaveBeenLastCalledWith('security')
    await user.click(screen.getByRole('button', { name: 'Agent personalization' }))
    expect(navigate).toHaveBeenLastCalledWith('agents')
    await user.click(screen.getByRole('button', { name: /^Updates/ }))
    expect(navigate).toHaveBeenLastCalledWith('ai-updates')
  })

  it('saves the workspace guidance when the field loses focus', async () => {
    vi.mocked(updateWorkspaceAgentGuidance).mockResolvedValue({ instructions: 'Be brief and kind.' })
    renderPage('ai-agent')
    const field = screen.getByRole('textbox', { name: 'Workspace guidance' })
    expect(field).toHaveValue('Be brief.')
    const user = userEvent.setup()
    await user.clear(field)
    await user.type(field, 'Be brief and kind.')
    await user.tab()
    await waitFor(() => expect(updateWorkspaceAgentGuidance).toHaveBeenCalledWith('Be brief and kind.'))
  })

  it('does not save unchanged guidance', async () => {
    renderPage('ai-agent')
    const user = userEvent.setup()
    await user.click(screen.getByRole('textbox', { name: 'Workspace guidance' }))
    await user.tab()
    expect(updateWorkspaceAgentGuidance).not.toHaveBeenCalled()
  })
})

describe('sub-pages', () => {
  it('turns resolved thread summaries on and off', async () => {
    vi.mocked(updateWorkspacePreferences).mockResolvedValue(settings)
    renderPage('ai-summaries')
    expect(screen.getByRole('heading', { level: 1, name: 'Resolved thread summaries' })).toBeVisible()
    await userEvent.setup().click(screen.getByRole('checkbox', { name: 'Enable thread summaries' }))
    await waitFor(() => expect(updateWorkspacePreferences).toHaveBeenCalledWith({ featureFlags: { 'thread-summaries': false } }, 'workspace'))
  })

  it('saves initiative and project update guidance separately', async () => {
    vi.mocked(updateWorkspacePreferences).mockResolvedValue(settings)
    renderPage('ai-updates')
    expect(screen.getByRole('heading', { level: 1, name: 'Initiative and project updates' })).toBeVisible()
    const user = userEvent.setup()
    await user.type(screen.getByRole('textbox', { name: 'Initiative updates' }), 'Lead with risks.')
    await user.tab()
    await waitFor(() => expect(updateWorkspacePreferences).toHaveBeenCalledWith({ featureSettings: { initiativeUpdateGuidance: 'Lead with risks.' } }, 'workspace'))
    await user.type(screen.getByRole('textbox', { name: 'Project updates' }), 'Keep it short.')
    await user.tab()
    await waitFor(() => expect(updateWorkspacePreferences).toHaveBeenLastCalledWith({ featureSettings: { projectUpdateGuidance: 'Keep it short.' } }, 'workspace'))
  })

  it('lists installed agents with their guidance on the Agents page', async () => {
    renderPage('installed-agents')
    expect(screen.getByRole('heading', { level: 1, name: 'Agents' })).toBeVisible()
    expect(await screen.findByText('1 agent enabled')).toBeVisible()
    expect(screen.getByText('Flow QA OAuth app')).toBeVisible()
    expect(within(screen.getByRole('textbox', { name: 'Installed agents guidance' }).closest('section') as HTMLElement).getByRole('heading', { name: 'Workspace guidance' })).toBeVisible()
  })

  it('shows Triage Intelligence behavior options as Show, Auto-apply and Hide', async () => {
    renderPage('triage-intelligence', { workspaceSettings: { ...settings, featureFlags: { 'triage-intelligence': true } } as WorkspaceSettings })
    const user = userEvent.setup()
    await user.click(screen.getByRole('combobox', { name: 'Assignee is suggested' }))
    expect((await screen.findAllByRole('menuitem')).map(item => item.textContent)).toEqual(['Show', 'Auto-apply', 'Hide'])
  })
})

describe('routes', () => {
  it('maps the reference URLs to the AI pages and back', () => {
    for (const [path, page] of [
      ['/ws/settings/ai/agent', 'ai-agent'],
      ['/ws/settings/ai/code-intelligence', 'code-intelligence'],
      ['/ws/settings/ai/triage', 'triage-intelligence'],
      ['/ws/settings/ai/summaries', 'ai-summaries'],
      ['/ws/settings/ai/updates', 'ai-updates'],
      ['/ws/settings/agents', 'installed-agents'],
    ] as Array<[string, SettingsPageId]>) {
      expect(parseAppRoute(path)).toMatchObject({ kind: 'settings', page })
      expect(settingsPath('ws', page)).toBe(path)
    }
    expect(parseAppRoute('/ws/settings/ai/constructor').kind).not.toBe('settings')
  })
})

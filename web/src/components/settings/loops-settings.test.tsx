import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { LoopConfig, WorkspaceSettings } from '@/types/flow'
import { getLoopConfig, updateLoopSettings } from '@/lib/api'
import { FeatureSettingsPage } from './feature-settings'

vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), getLoopConfig: vi.fn(), updateLoopSettings: vi.fn(), updateWorkspacePreferences: vi.fn() }))

const config: LoopConfig = {
  webSearchAvailable: false, webSearchProvider: '', agentWebSearch: false, codeAccessAvailable: true, externalLoopTriggers: false,
  trustedSourcesMode: 'none', trustedSourcesAllowlist: [], trustedSourceOptions: [],
}
const settings = { sessionDurationDays: 30, featureFlags: { loops: true }, featureSettings: {}, trustedSourcesMode: 'none', trustedSourcesAllowlist: [] } as unknown as WorkspaceSettings

function renderLoops() {
  return render(<MemoryRouter><I18nProvider><FeatureSettingsPage page="loops" data={makeBootstrap({ workspaceSettings: settings, integrationConnections: [], viewerRole: 'admin' })} onCreateReleasePipeline={vi.fn()} onOpenReleasePipeline={vi.fn()} onOpenIntegration={vi.fn()} onReload={vi.fn()}/></I18nProvider></MemoryRouter>)
}

beforeEach(() => { vi.clearAllMocks(); localStorage.setItem('flow:locale', 'en-US') })

it('keeps trusted sources in the workspace Loops settings', async () => {
  vi.mocked(getLoopConfig).mockResolvedValue(config)
  renderLoops()
  expect(screen.getByTestId('agent-trusted-sources-settings')).toHaveTextContent('Trusted sources')
})

it('explains that web search is not configured and disables the chat toggle', async () => {
  vi.mocked(getLoopConfig).mockResolvedValue(config)
  renderLoops()
  const section = document.getElementById('web-search')!
  expect(await within(section).findByText('Not configured')).toBeVisible()
  expect(section).toHaveTextContent("Web search isn't configured for this workspace.")
  expect(within(section).getByRole('checkbox', { name: 'Web search in Flow Agent chat' })).toBeDisabled()
})

it('shows the provider and saves the chat web search setting when available', async () => {
  const user = userEvent.setup()
  vi.mocked(getLoopConfig).mockResolvedValue({ ...config, webSearchAvailable: true, webSearchProvider: 'tavily' })
  vi.mocked(updateLoopSettings).mockResolvedValue({ ...config, webSearchAvailable: true, webSearchProvider: 'tavily', agentWebSearch: true })
  renderLoops()
  const section = document.getElementById('web-search')!
  expect(await within(section).findByText('Available')).toBeVisible()
  expect(section).toHaveTextContent('Configured (tavily)')
  const toggle = within(section).getByRole('checkbox', { name: 'Web search in Flow Agent chat' })
  await waitFor(() => expect(toggle).toBeEnabled())
  await user.click(toggle)
  expect(updateLoopSettings).toHaveBeenCalledWith({ agentWebSearch: true })
  await waitFor(() => expect(toggle).toBeChecked())
})

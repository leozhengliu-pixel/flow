import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { WorkspaceSettings } from '@/types/flow'
import { FeatureSettingsPage } from './feature-settings'

it('offers only supported integrations and routes code connection buttons to setup', async () => {
  localStorage.setItem('flow:locale', 'en-US')
  const user = userEvent.setup(), open = vi.fn()
  render(<I18nProvider><FeatureSettingsPage page="integrations" data={makeBootstrap({ workspaceSettings: { featureFlags: {}, featureSettings: {} } as WorkspaceSettings, integrationConnections: [{ id: 'legacy', provider: 'codex', status: 'connected' }] as never[] })} onCreateReleasePipeline={vi.fn()} onOpenReleasePipeline={vi.fn()} onOpenIntegration={open} onReload={vi.fn()}/></I18nProvider>)
  expect(screen.getAllByRole('heading', { level: 3 }).map(node => node.textContent)).toEqual(['GitHub', 'Slack', 'GitLab'])
  expect(screen.queryByRole('tab', { name: 'Agents' })).not.toBeInTheDocument()
  for (const provider of ['GitHub', 'GitLab']) {
    const card = screen.getByRole('heading', { name: provider }).closest('article')!
    await user.click(within(card).getByRole('button', { name: 'Connect' }))
    expect(open).toHaveBeenLastCalledWith(provider.toLowerCase())
    expect(card.querySelector('svg path')).toBeTruthy()
  }
})

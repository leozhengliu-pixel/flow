import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { WorkspaceSettings } from '@/types/flow'
import { FeatureSettingsPage } from './feature-settings'

it('lists only working integrations as cards grouped by category and opens them', async () => {
  localStorage.setItem('flow:locale', 'en-US')
  const user = userEvent.setup(), open = vi.fn()
  render(
    <I18nProvider>
      <FeatureSettingsPage
        page="integrations"
        data={makeBootstrap({
          workspaceSettings: { featureFlags: {}, featureSettings: {} } as WorkspaceSettings,
          integrationConnections: [{ id: 'legacy', provider: 'codex', status: 'connected' }] as never[],
        })}
        onCreateReleasePipeline={vi.fn()}
        onOpenReleasePipeline={vi.fn()}
        onOpenIntegration={open}
        onReload={vi.fn()}
      />
    </I18nProvider>,
  )

  expect(screen.getByRole('heading', { name: 'Essentials' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /GitHub/ })).toBeInTheDocument()
  expect(screen.queryByText('Coming soon')).not.toBeInTheDocument()
  expect(screen.queryByText('Not supported')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /Figma/ })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: /Enabled/ })).toBeInTheDocument()

  await user.click(screen.getByRole('button', { name: /GitHub/ }))
  expect(open).toHaveBeenLastCalledWith('github')
  await user.click(screen.getByRole('button', { name: /Jira/ }))
  expect(open).toHaveBeenLastCalledWith('jira')

  await user.type(screen.getByRole('textbox', { name: 'Search integrations' }), 'gitlab')
  expect(screen.getByRole('button', { name: /GitLab/ })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /Slack/ })).not.toBeInTheDocument()
})

import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { WorkspaceSettings } from '@/types/flow'
import { FeatureSettingsPage } from './feature-settings'

it('shows an honest catalog with Coming soon / Not supported and routes supported connects', async () => {
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

  expect(screen.getByRole('heading', { name: 'GitHub' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: /Jira/ })).toBeInTheDocument()
  expect(screen.getAllByText('Coming soon').length).toBeGreaterThan(0)
  expect(screen.getAllByText('Not supported').length).toBeGreaterThan(0)
  expect(screen.getByRole('button', { name: /Enabled/ })).toBeInTheDocument()

  const github = screen.getByRole('heading', { name: 'GitHub' }).closest('article')!
  await user.click(within(github).getByRole('button', { name: 'Connect' }))
  expect(open).toHaveBeenLastCalledWith('github')

  const jira = screen.getByRole('heading', { name: /Jira/ }).closest('article')!
  await user.click(within(jira).getByRole('button', { name: 'Connect' }))
  expect(open).toHaveBeenLastCalledWith('jira')
})

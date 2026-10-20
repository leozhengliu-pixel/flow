import { render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { EnabledIntegrationsSettingsPage } from './enabled-integrations-page'

it('lists enabled integrations only, not authorized OAuth applications', () => {
  localStorage.setItem('flow:locale', 'en-US')
  render(
    <I18nProvider>
      <EnabledIntegrationsSettingsPage
        data={makeBootstrap({
          integrationConnections: [{ id: 'gh', provider: 'github', name: 'GitHub', status: 'connected' }] as never[],
          oauthAuthorizations: [{ id: 'grant', clientId: 'custom', clientName: 'Custom OAuth app', scopes: ['read'], createdAt: '2026-10-20T00:00:00Z' }] as never[],
        })}
        onBack={vi.fn()}
        onOpenSlug={vi.fn()}
        onReload={vi.fn()}
      />
    </I18nProvider>,
  )

  expect(screen.getByRole('button', { name: 'GitHub' })).toBeInTheDocument()
  expect(screen.queryByText('Custom OAuth app')).not.toBeInTheDocument()
})

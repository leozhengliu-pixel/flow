import { render, screen } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { listSCIMTokens } from '@/lib/api'
import { IdentityProviderSettingsPage } from './identity-provider-settings-page'

vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), listSCIMTokens: vi.fn() }))
vi.mock('@/lib/toast', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))
beforeEach(() => { vi.clearAllMocks(); localStorage.setItem('flow:locale', 'en-US') })

it('shows the SCIM base URL and only tokens that still work, with their last use', async () => {
  vi.mocked(listSCIMTokens).mockResolvedValue([
    { id: 'active', workspaceId: 'workspace-1', name: 'Okta SCIM', createdAt: '2026-01-01T00:00:00Z', lastUsedAt: '2026-02-01T00:00:00Z' },
    { id: 'revoked', workspaceId: 'workspace-1', name: 'Old SCIM', createdAt: '2025-01-01T00:00:00Z', revokedAt: '2025-06-01T00:00:00Z' },
  ])
  const data = makeBootstrap({ identityProviders: [{ id: 'idp', workspaceId: 'workspace-1', type: 'saml', name: 'Okta', issuer: 'https://idp.example.com', scopes: [], domains: [], enabled: true, enforced: false, discoveryStatus: 'verified', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z' }] })
  render(<I18nProvider><IdentityProviderSettingsPage data={data} providerId="idp" onReload={async () => {}} onBack={() => {}}/></I18nProvider>)
  expect(screen.getByRole('textbox', { name: 'SCIM base URL' })).toHaveValue(`${window.location.origin}/scim/v2/workspace`)
  expect(await screen.findByText('Okta SCIM')).toBeInTheDocument()
  expect(screen.getByText(/Last used/)).toBeInTheDocument()
  expect(screen.queryByText('Old SCIM')).not.toBeInTheDocument()
  expect(screen.queryByText('No SCIM tokens')).not.toBeInTheDocument()
})

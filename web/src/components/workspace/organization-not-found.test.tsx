import { render, screen, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { OrganizationNotFound } from './organization-not-found'
import { fetchWorkspaceAccessStatus } from '@/lib/api'
import { I18nProvider } from '@/i18n/i18n'

vi.mock('@/lib/api', async (original) => ({
  ...(await original<typeof import('@/lib/api')>()),
  fetchWorkspaceAccessStatus: vi.fn(),
  logoutAccount: vi.fn(),
}))

it('shows not-found copy when workspace does not exist', async () => {
  vi.mocked(fetchWorkspaceAccessStatus).mockResolvedValueOnce({
    exists: false,
    hasMembership: false,
    reason: 'not_found',
    allowedAuthServices: [],
    allowedAuthLabels: [],
  })
  render(
    <I18nProvider>
      <OrganizationNotFound orgKey="missing" />
    </I18nProvider>,
  )
  await waitFor(() => expect(screen.getByRole('heading', { name: /does not exist|不存在/i })).toBeVisible())
})

it('lists allowed auth services and logout for auth restriction', async () => {
  vi.mocked(fetchWorkspaceAccessStatus).mockResolvedValueOnce({
    exists: true,
    hasMembership: true,
    authRestricted: true,
    reason: 'auth_restricted',
    allowedAuthServices: ['saml', 'google'],
    allowedAuthLabels: ['SAML/SSO', 'Google'],
  })
  render(
    <I18nProvider>
      <OrganizationNotFound orgKey="locked" />
    </I18nProvider>,
  )
  await waitFor(() => expect(screen.getByRole('heading', { name: /Login method not allowed|不允许/i })).toBeVisible())
  expect(screen.getByText('SAML/SSO')).toBeVisible()
  expect(screen.getByRole('button', { name: /Log out|退出/i })).toBeVisible()
})

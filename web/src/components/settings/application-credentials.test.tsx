import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { createApplicationDeveloperToken, rotateOAuthApplicationSecret, updateOAuthApplication } from '@/lib/api'
import type { OAuthApplication } from '@/types/flow'
import { ApplicationCredentials } from './application-credentials'
import { TooltipProvider } from '@/components/ui/tooltip'

vi.mock('@/lib/api', () => ({ createApplicationDeveloperToken: vi.fn(), rotateOAuthApplicationSecret: vi.fn(), updateOAuthApplication: vi.fn() }))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: vi.fn().mockResolvedValue(true) }))
const app: OAuthApplication = { id: 'application-1', clientId: 'client-1', name: 'Agent', creatorId: 'user-1', scopes: ['read', 'write', 'app:assignable'], redirectUris: [], createdAt: '', updatedAt: '' }
beforeEach(() => {
  vi.clearAllMocks()
  localStorage.setItem('flow:locale', 'en-US')
  vi.mocked(createApplicationDeveloperToken).mockResolvedValue({ access_token: 'one-time-token', token_type: 'Bearer', expires_in: 3600, scope: 'read', actor: 'app', app_user_id: 'app-agent' })
  vi.mocked(rotateOAuthApplicationSecret).mockResolvedValue({ clientSecret: 'replacement-secret' })
  vi.mocked(updateOAuthApplication).mockResolvedValue({ ...app, supportsClientCredentials: true })
})

it('creates an explicitly scoped app token and clears the one-time value after closing', async () => {
  const user = userEvent.setup()
  const reload = vi.fn().mockResolvedValue(undefined)
  render(<I18nProvider><TooltipProvider><ApplicationCredentials app={app} onReload={reload}/></TooltipProvider></I18nProvider>)
  await user.click(screen.getByRole('button', { name: 'Create & copy token' }))
  expect(screen.getByRole('radio', { name: 'Application' })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: 'write' })).not.toBeChecked()
  await user.click(screen.getByRole('button', { name: 'Create token' }))
  await waitFor(() => expect(createApplicationDeveloperToken).toHaveBeenCalledWith(app.id, 'app', ['read']))
  expect(await screen.findByRole('textbox', { name: 'Access token' })).toHaveValue('one-time-token')
  await user.click(screen.getByRole('button', { name: 'Done' }))
  expect(screen.queryByRole('textbox', { name: 'Access token' })).toBeNull()
  expect(reload).toHaveBeenCalledOnce()
})

it('separates personal actor scopes from agent scopes and enables the machine grant', async () => {
  const user = userEvent.setup()
  render(<I18nProvider><TooltipProvider><ApplicationCredentials app={app} onReload={vi.fn().mockResolvedValue(undefined)}/></TooltipProvider></I18nProvider>)
  await user.click(screen.getByRole('checkbox', { name: 'Client credentials' }))
  await waitFor(() => expect(updateOAuthApplication).toHaveBeenCalledWith(app.id, { supportsClientCredentials: true }))
  await user.click(screen.getByRole('button', { name: 'Create & copy token' }))
  await user.click(screen.getByRole('radio', { name: 'User' }))
  expect(screen.queryByRole('checkbox', { name: 'app:assignable' })).toBeNull()
  await user.click(screen.getByRole('button', { name: 'Create token' }))
  await waitFor(() => expect(createApplicationDeveloperToken).toHaveBeenCalledWith(app.id, 'user', ['read']))
})

it('rotates through the real endpoint and allows copying the replacement', async () => {
  const user = userEvent.setup()
  render(<I18nProvider><TooltipProvider><ApplicationCredentials app={app} onReload={vi.fn().mockResolvedValue(undefined)}/></TooltipProvider></I18nProvider>)
  await user.click(screen.getByRole('button', { name: 'Rotate secret' }))
  expect(await screen.findByText('replacement-secret')).toBeVisible()
  expect(rotateOAuthApplicationSecret).toHaveBeenCalledWith(app.id)
  expect(screen.getByRole('button', { name: 'Copy client secret' })).toBeVisible()
})

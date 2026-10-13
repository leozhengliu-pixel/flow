import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { createApplicationDeveloperToken, fetchOAuthApplicationClientSecret, rotateOAuthApplicationSecret } from '@/lib/api'
import { confirmAction } from '@/components/ui/action-dialog-service'
import type { OAuthApplication } from '@/types/flow'
import { ApplicationCredentials } from './application-credentials'
import { TooltipProvider } from '@/components/ui/tooltip'

vi.mock('@/lib/api', () => ({ createApplicationDeveloperToken: vi.fn(), rotateOAuthApplicationSecret: vi.fn(), fetchOAuthApplicationClientSecret: vi.fn() }))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: vi.fn().mockResolvedValue(true) }))
const app: OAuthApplication = { id: 'application-1', clientId: 'client-1', name: 'Agent', creatorId: 'user-1', scopes: ['read', 'write', 'app:assignable'], redirectUris: [], createdAt: '', updatedAt: '' }
const writeText = vi.fn()
const setupUser = () => {
  const user = userEvent.setup()
  // userEvent installs its own clipboard; replace it so the copies can be asserted.
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  return user
}
const renderCredentials = (reload = vi.fn().mockResolvedValue(undefined)) => render(<I18nProvider><TooltipProvider><ApplicationCredentials app={app} onReload={reload}/></TooltipProvider></I18nProvider>)

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.setItem('flow:locale', 'en-US')
  writeText.mockResolvedValue(undefined)
  vi.mocked(createApplicationDeveloperToken).mockResolvedValue({ access_token: 'one-time-token', token_type: 'Bearer', expires_in: 3600, scope: 'read', actor: 'app', app_user_id: 'app-agent' })
  vi.mocked(rotateOAuthApplicationSecret).mockResolvedValue({ clientSecret: 'replacement-secret' })
  vi.mocked(fetchOAuthApplicationClientSecret).mockResolvedValue({ clientSecret: 'current-secret' })
})

it('shows the reference app\'s OAuth credentials: the client ID, a masked secret and the developer token', async () => {
  const user = setupUser()
  renderCredentials()
  expect(screen.getByRole('heading', { name: 'OAuth credentials' })).toBeVisible()
  expect(screen.getByText('client-1')).toBeVisible()
  expect(screen.getByLabelText('Client secret (hidden)')).toHaveTextContent('•'.repeat(32))
  expect(screen.queryByText('current-secret')).toBeNull()
  await user.click(screen.getByRole('button', { name: 'Copy client ID' }))
  expect(writeText).toHaveBeenCalledWith('client-1')
  await user.click(screen.getByRole('button', { name: 'Copy client secret' }))
  await waitFor(() => expect(writeText).toHaveBeenCalledWith('current-secret'))
  expect(fetchOAuthApplicationClientSecret).toHaveBeenCalledWith(app.id)
  expect(screen.queryByText('current-secret')).toBeNull()
})

it('rotates the secret after the reference app\'s confirmation and keeps it masked', async () => {
  const user = setupUser()
  const reload = vi.fn().mockResolvedValue(undefined)
  renderCredentials(reload)
  await user.click(screen.getByRole('button', { name: 'Rotate secret' }))
  expect(confirmAction).toHaveBeenCalledWith('Rotate your client secret?', expect.objectContaining({ description: 'You cannot undo this action.', confirmLabel: 'Rotate' }))
  await waitFor(() => expect(rotateOAuthApplicationSecret).toHaveBeenCalledWith(app.id))
  expect(reload).toHaveBeenCalled()
  expect(screen.queryByText('replacement-secret')).toBeNull()
})

it('creates an explicitly scoped developer token and clears the one-time value after closing', async () => {
  const user = setupUser()
  const reload = vi.fn().mockResolvedValue(undefined)
  renderCredentials(reload)
  await user.click(screen.getByRole('button', { name: 'Create & copy token' }))
  expect(screen.getByRole('radio', { name: 'Application' })).toBeChecked()
  await user.click(screen.getByRole('radio', { name: 'User' }))
  expect(screen.queryByRole('checkbox', { name: 'app:assignable' })).toBeNull()
  await user.click(screen.getByRole('radio', { name: 'Application' }))
  await user.click(screen.getByRole('button', { name: 'Create token' }))
  await waitFor(() => expect(createApplicationDeveloperToken).toHaveBeenCalledWith(app.id, 'app', ['read']))
  expect(await screen.findByRole('textbox', { name: 'Access token' })).toHaveValue('one-time-token')
  expect(writeText).toHaveBeenCalledWith('one-time-token')
  await user.click(screen.getByRole('button', { name: 'Done' }))
  expect(screen.queryByRole('textbox', { name: 'Access token' })).toBeNull()
  expect(reload).toHaveBeenCalledOnce()
})

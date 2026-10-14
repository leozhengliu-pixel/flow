import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { createApplicationDeveloperToken, fetchApplicationDeveloperToken, fetchOAuthApplicationClientSecret, rotateOAuthApplicationSecret } from '@/lib/api'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { toast } from '@/lib/toast'
import type { OAuthApplication } from '@/types/flow'
import { ApplicationCredentials } from './application-credentials'
import { TooltipProvider } from '@/components/ui/tooltip'

vi.mock('@/lib/api', () => ({ createApplicationDeveloperToken: vi.fn(), fetchApplicationDeveloperToken: vi.fn(), rotateOAuthApplicationSecret: vi.fn(), fetchOAuthApplicationClientSecret: vi.fn() }))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: vi.fn().mockResolvedValue(true) }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() } }))
const app: OAuthApplication = { id: 'application-1', clientId: 'client-1', name: 'Agent', creatorId: 'user-1', scopes: ['read', 'write', 'app:assignable', 'app:mentionable', 'customer:read', 'customer:write', 'initiative:read', 'initiative:write'], redirectUris: [], createdAt: '', updatedAt: '' }
const writeText = vi.fn()
const setupUser = () => {
  const user = userEvent.setup()
  // userEvent installs its own clipboard; replace it so the copies can be asserted.
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  return user
}
const renderCredentials = (reload = vi.fn().mockResolvedValue(undefined)) => render(<MemoryRouter><I18nProvider><TooltipProvider><ApplicationCredentials app={app} workspace="acme" onReload={reload}/></TooltipProvider></I18nProvider></MemoryRouter>)
const token = (input: Partial<Awaited<ReturnType<typeof createApplicationDeveloperToken>>>) => ({ access_token: 'token', token_type: 'Bearer' as const, expires_in: 3600, scope: 'read write', actor: 'app' as const, ...input })

beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  Element.prototype.scrollIntoView ??= () => {}
  localStorage.setItem('flow:locale', 'en-US')
  writeText.mockResolvedValue(undefined)
  vi.mocked(fetchApplicationDeveloperToken).mockResolvedValue(undefined)
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

it('Create & copy token › User copies a new read/write token without asking for scopes', async () => {
  const user = setupUser()
  vi.mocked(createApplicationDeveloperToken).mockResolvedValue(token({ access_token: 'user-token', actor: 'user' }))
  renderCredentials()
  await user.click(screen.getByRole('button', { name: 'Create & copy token' }))
  const menu = screen.getByRole('dialog')
  expect(within(menu).getByPlaceholderText('Create developer access token…')).toBeVisible()
  expect(within(menu).getAllByRole('option').map(option => option.textContent)).toEqual(['User', 'App'])
  await user.click(within(menu).getByRole('option', { name: 'User' }))
  await waitFor(() => expect(createApplicationDeveloperToken).toHaveBeenCalledWith(app.id, 'user'))
  expect(writeText).toHaveBeenCalledWith('user-token')
  expect(toast.success).toHaveBeenCalledWith('Developer access token created and copied to your clipboard')
  expect(screen.queryByRole('dialog')).toBeNull()
})

it('Create & copy token › App asks for scopes (read and write on) and creates the app user token', async () => {
  const user = setupUser()
  const reload = vi.fn().mockResolvedValue(undefined)
  vi.mocked(createApplicationDeveloperToken).mockResolvedValue(token({ access_token: 'app-token', scope: 'initiative:read read write' }))
  renderCredentials(reload)
  await user.click(screen.getByRole('button', { name: 'Create & copy token' }))
  await user.click(screen.getByRole('option', { name: 'App' }))
  const dialog = await screen.findByRole('dialog', { name: 'Select scopes' })
  expect(within(dialog).getByText('Choose which OAuth scopes to grant for this app user token.')).toBeVisible()
  const switches = within(dialog).getAllByRole<HTMLInputElement>('checkbox')
  expect(switches.map(item => item.getAttribute('aria-label'))).toEqual(['read', 'write', 'app:mentionable', 'app:assignable', 'customer:read', 'customer:write', 'initiative:read', 'initiative:write'])
  expect(switches.map(item => item.checked)).toEqual([true, true, false, false, false, false, false, false])
  expect(within(dialog).getByText('Delegate issues to the app or add the app as a project member')).toBeVisible()
  await user.click(within(dialog).getByRole('checkbox', { name: 'read' }))
  await user.click(within(dialog).getByRole('checkbox', { name: 'write' }))
  expect(within(dialog).getByRole('button', { name: 'Create token' })).toBeDisabled()
  await user.click(within(dialog).getByRole('checkbox', { name: 'initiative:read' }))
  await user.click(within(dialog).getByRole('checkbox', { name: 'read' }))
  await user.click(within(dialog).getByRole('checkbox', { name: 'write' }))
  await user.click(within(dialog).getByRole('button', { name: 'Create token' }))
  await waitFor(() => expect(createApplicationDeveloperToken).toHaveBeenCalledWith(app.id, 'app', ['read', 'write', 'initiative:read']))
  expect(writeText).toHaveBeenCalledWith('app-token')
  expect(toast.success).toHaveBeenCalledWith('Developer access token created and copied to your clipboard')
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Select scopes' })).toBeNull())
  expect(reload).toHaveBeenCalledOnce()
})

it('Create & copy token › App copies the existing app token and lists its scopes', async () => {
  const user = setupUser()
  vi.mocked(fetchApplicationDeveloperToken).mockResolvedValue(token({ access_token: 'existing-token', scope: 'customer:read initiative:read read write', existing: true }))
  renderCredentials()
  await user.click(screen.getByRole('button', { name: 'Create & copy token' }))
  await user.click(screen.getByRole('option', { name: 'App' }))
  await waitFor(() => expect(writeText).toHaveBeenCalledWith('existing-token'))
  expect(createApplicationDeveloperToken).not.toHaveBeenCalled()
  expect(screen.queryByRole('dialog', { name: 'Select scopes' })).toBeNull()
  const [title, options] = vi.mocked(toast.success).mock.calls[0]
  expect(title).toBe('Existing app token copied to clipboard')
  render(<MemoryRouter><>{options?.description}</></MemoryRouter>)
  expect(screen.getByText('customer:read')).toBeVisible()
  expect(screen.getByRole('link', { name: 'app details page' })).toHaveAttribute('href', '/acme/settings/applications/client-1')
  expect(screen.getByRole('link', { name: /OAuth API/ })).toHaveAttribute('target', '_blank')
})
